package integration

import (
	"database/sql"
	"os"
	"reflect"
	"testing"

	_ "github.com/lib/pq"
	fq "github.com/raushanrk5/filtersql"
)

// pgReg exercises the Postgres-specific corners: native text[] arrays and a
// jsonb map column.
var pgReg = fq.Registry{
	"id":            {Type: fq.TypeID, Column: "id", Sortable: true},
	"name":          {Type: fq.TypeString, Column: "name", Sortable: true},
	"status":        {Type: fq.TypeEnum, Column: "status", Enum: []string{"ACTIVE", "ARCHIVED"}, Sortable: true},
	"severity":      {Type: fq.TypeInt, Column: "severity", Sortable: true},
	"owner":         {Type: fq.TypeString, Column: "owner", Nullable: true},
	"tags":          {Type: fq.TypeArray, Column: "tags"},
	"labels":        {Type: fq.TypeMap, Column: "labels"},
	"finding_count": {Type: fq.TypeInt, Column: "count(*)", Having: true},
}

// pgSetup connects to the Postgres given by FILTERSQL_POSTGRES_DSN (skipping the
// test when unset — that's how it stays green locally and runs in CI), then
// creates and seeds a fresh asset table.
//
//	id  name    status    severity  owner  tags            labels
//	a1  web-01  ACTIVE    9         alice  {prod,crit}     {"env":"prod"}
//	a2  web-02  ACTIVE    5         NULL   {prod}          {"env":"stage"}
//	a3  db-01   ARCHIVED  8         bob    {db}            {"env":"prod"}
//	a4  api-01  ACTIVE    7         alice  {prod,api}      {"env":"prod"}
func pgSetup(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("FILTERSQL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set FILTERSQL_POSTGRES_DSN to run Postgres integration tests")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`DROP TABLE IF EXISTS asset`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE asset (
		id text, name text, status text, severity int, owner text,
		tags text[], labels jsonb)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	rows := []struct {
		id, name, status string
		sev              int
		owner            any
		tags, labels     string
	}{
		{"a1", "web-01", "ACTIVE", 9, "alice", `{prod,crit}`, `{"env":"prod"}`},
		{"a2", "web-02", "ACTIVE", 5, nil, `{prod}`, `{"env":"stage"}`},
		{"a3", "db-01", "ARCHIVED", 8, "bob", `{db}`, `{"env":"prod"}`},
		{"a4", "api-01", "ACTIVE", 7, "alice", `{prod,api}`, `{"env":"prod"}`},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO asset VALUES ($1,$2,$3,$4,$5,$6::text[],$7::jsonb)`,
			r.id, r.name, r.status, r.sev, r.owner, r.tags, r.labels); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
	return db
}

func pgIDs(t *testing.T, db *sql.DB, query string, args []any) []string {
	t.Helper()
	rs, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var id string
		if err := rs.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, id)
	}
	return out
}

func TestPostgres_ScalarAndLike(t *testing.T) {
	db := pgSetup(t)
	where, args, _ := pgReg.Compile(fq.Postgres{}, []fq.Condition{
		{Key: "status", Op: fq.OpEq, Values: []any{"ACTIVE"}},
		{Key: "name", Op: fq.OpLike, Values: []any{"web"}},
	})
	got := pgIDs(t, db, "SELECT id FROM asset WHERE "+where+" ORDER BY id", args)
	if want := []string{"a1", "a2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPostgres_ArrayOperators(t *testing.T) {
	db := pgSetup(t)

	// && (contains any) — the ::text[] cast path.
	w1, a1, _ := pgReg.Compile(fq.Postgres{}, []fq.Condition{{Key: "tags", Op: fq.OpContainsAny, Values: []any{"api", "db"}}})
	if got, want := pgIDs(t, db, "SELECT id FROM asset WHERE "+w1+" ORDER BY id", a1), []string{"a3", "a4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("contains_any: got %v, want %v", got, want)
	}

	// @> (contains all).
	w2, a2, _ := pgReg.Compile(fq.Postgres{}, []fq.Condition{{Key: "tags", Op: fq.OpContains, Values: []any{"prod", "crit"}}})
	if got, want := pgIDs(t, db, "SELECT id FROM asset WHERE "+w2+" ORDER BY id", a2), []string{"a1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("contains-all: got %v, want %v", got, want)
	}
}

func TestPostgres_JsonbMapOperators(t *testing.T) {
	db := pgSetup(t)

	// has key/value: labels ->> 'env' = 'prod'  -> a1, a3, a4
	w1, a1, _ := pgReg.Compile(fq.Postgres{}, []fq.Condition{
		{Key: "labels", Op: fq.OpHasKeyValues, Pairs: []fq.KeyValue{{Key: "env", Values: []string{"prod"}}}}})
	if got, want := pgIDs(t, db, "SELECT id FROM asset WHERE "+w1+" ORDER BY id", a1), []string{"a1", "a3", "a4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("has_key_values: got %v, want %v", got, want)
	}

	// has key: labels ? 'env'  -> all four (the jsonb ? operator vs $N placeholder)
	w2, a2, _ := pgReg.Compile(fq.Postgres{}, []fq.Condition{
		{Key: "labels", Op: fq.OpHasKeys, Pairs: []fq.KeyValue{{Key: "env", Values: nil}}}})
	if got := pgIDs(t, db, "SELECT id FROM asset WHERE "+w2+" ORDER BY id", a2); len(got) != 4 {
		t.Errorf("has_keys: got %v, want 4 rows", got)
	}
}

func TestPostgres_NullAndKeyset(t *testing.T) {
	db := pgSetup(t)

	// NULL owner -> a2
	wn, an, _ := pgReg.Compile(fq.Postgres{}, []fq.Condition{{Key: "owner", Op: fq.OpIsNull}})
	if got, want := pgIDs(t, db, "SELECT id FROM asset WHERE "+wn, an), []string{"a2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("is_null: got %v, want %v", got, want)
	}

	// Keyset: severity desc, id asc -> a1(9),a3(8),a4(7),a2(5); page after a3.
	sorts := []fq.Sort{{Key: "severity", Desc: true}, {Key: "id"}}
	order, _ := pgReg.OrderBy(fq.Postgres{}, sorts)
	seek, sargs, _ := pgReg.KeysetWhere(fq.Postgres{}, sorts, fq.Cursor{"severity": 8, "id": "a3"})
	got := pgIDs(t, db, "SELECT id FROM asset WHERE "+seek+" ORDER BY "+order, sargs)
	if want := []string{"a4", "a2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("keyset page: got %v, want %v", got, want)
	}
}

func TestPostgres_ScalarInAnyExecutes(t *testing.T) {
	db := pgSetup(t)
	// String _in binds one text[] param (= ANY) — no per-value placeholders.
	where, args, err := pgReg.Compile(fq.Postgres{}, []fq.Condition{
		{Key: "name", Op: fq.OpIn, Values: []any{"web-01", "web-02"}}})
	if err != nil {
		t.Fatal(err)
	}
	got := pgIDs(t, db, "SELECT id FROM asset WHERE "+where+" ORDER BY id", args)
	if want := []string{"a1", "a2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (where: %s)", got, want, where)
	}
}

func TestPostgres_BuilderContinuity(t *testing.T) {
	db := pgSetup(t)
	// Combine a filter WHERE and a keyset seek in one Postgres query — this is
	// the case where separate compiles would collide on $1. The Builder shares
	// the counter so $N stays continuous and the args line up.
	b := pgReg.Builder(fq.Postgres{})
	where, err := b.Where([]fq.Condition{{Key: "status", Op: fq.OpEq, Values: []any{"ACTIVE"}}})
	if err != nil {
		t.Fatal(err)
	}
	sort := []fq.Sort{{Key: "severity", Desc: true}, {Key: "id"}}
	seek, err := b.Keyset(sort, fq.Cursor{"severity": 9, "id": "a1"})
	if err != nil {
		t.Fatal(err)
	}
	order, _ := b.OrderBy(sort)

	query := "SELECT id FROM asset WHERE " + where + " AND " + seek + " ORDER BY " + order
	got := pgIDs(t, db, query, b.Args())
	// ACTIVE, severity < 9 -> a4(7), a2(5)
	if want := []string{"a4", "a2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (query: %s)", got, want, query)
	}
}

func TestPostgres_Having(t *testing.T) {
	db := pgSetup(t)
	_, having, args, _ := pgReg.CompileWhereHaving(fq.Postgres{}, nil,
		[]fq.Condition{{Key: "finding_count", Op: fq.OpGt, Values: []any{1}}})
	// ACTIVE=3, ARCHIVED=1 -> only ACTIVE survives count(*) > 1
	got := pgIDs(t, db, "SELECT status FROM asset GROUP BY status HAVING "+having+" ORDER BY status", args)
	if want := []string{"ACTIVE"}; !reflect.DeepEqual(got, want) {
		t.Errorf("having: got %v, want %v", got, want)
	}
}

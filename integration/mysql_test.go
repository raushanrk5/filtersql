package integration

import (
	"database/sql"
	"os"
	"reflect"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	fq "github.com/raushanrk5/filtersql"
)

// myReg exercises the MySQL-specific corners: JSON arrays and JSON object maps.
var myReg = fq.Registry{
	"id":            {Type: fq.TypeID, Column: "id", Sortable: true},
	"name":          {Type: fq.TypeString, Column: "name", Sortable: true},
	"status":        {Type: fq.TypeEnum, Column: "status", Enum: []string{"ACTIVE", "ARCHIVED"}, Sortable: true},
	"severity":      {Type: fq.TypeInt, Column: "severity", Sortable: true},
	"owner":         {Type: fq.TypeString, Column: "owner", Nullable: true},
	"tags":          {Type: fq.TypeArray, Column: "tags"},
	"labels":        {Type: fq.TypeMap, Column: "labels"},
	"finding_count": {Type: fq.TypeInt, Column: "count(*)", Having: true},
}

// mySetup connects to the MySQL given by FILTERSQL_MYSQL_DSN (skipping when
// unset), then creates and seeds a fresh asset table.
func mySetup(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("FILTERSQL_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set FILTERSQL_MYSQL_DSN to run MySQL integration tests")
	}
	db, err := sql.Open("mysql", dsn)
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
		id VARCHAR(16), name VARCHAR(64), status VARCHAR(16), severity INT,
		owner VARCHAR(64) NULL, tags JSON, labels JSON)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	rows := []struct {
		id, name, status string
		sev              int
		owner            any
		tags, labels     string
	}{
		{"a1", "web-01", "ACTIVE", 9, "alice", `["prod","crit"]`, `{"env":"prod"}`},
		{"a2", "web-02", "ACTIVE", 5, nil, `["prod"]`, `{"env":"stage"}`},
		{"a3", "db-01", "ARCHIVED", 8, "bob", `["db"]`, `{"env":"prod"}`},
		{"a4", "api-01", "ACTIVE", 7, "alice", `["prod","api"]`, `{"env":"prod"}`},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO asset VALUES (?,?,?,?,?,?,?)`,
			r.id, r.name, r.status, r.sev, r.owner, r.tags, r.labels); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
	return db
}

func TestMySQL_ScalarAndLike(t *testing.T) {
	db := mySetup(t)
	where, args, _ := myReg.Compile(fq.MySQL{}, []fq.Condition{
		{Key: "status", Op: fq.OpEq, Values: []any{"ACTIVE"}},
		{Key: "name", Op: fq.OpLike, Values: []any{"WEB"}}, // case-insensitive
	})
	if got := ids(t, db, "SELECT id FROM asset WHERE "+where+" ORDER BY id", args); !reflect.DeepEqual(got, []string{"a1", "a2"}) {
		t.Errorf("got %v, want [a1 a2]", got)
	}
}

func TestMySQL_JSONArrayAndMap(t *testing.T) {
	db := mySetup(t)

	// contains_any ["api","db"] -> a3, a4
	w1, a1, _ := myReg.Compile(fq.MySQL{}, []fq.Condition{{Key: "tags", Op: fq.OpContainsAny, Values: []any{"api", "db"}}})
	if got := ids(t, db, "SELECT id FROM asset WHERE "+w1+" ORDER BY id", a1); !reflect.DeepEqual(got, []string{"a3", "a4"}) {
		t.Errorf("contains_any: got %v", got)
	}
	// contains all ["prod","crit"] -> a1
	w2, a2, _ := myReg.Compile(fq.MySQL{}, []fq.Condition{{Key: "tags", Op: fq.OpContains, Values: []any{"prod", "crit"}}})
	if got := ids(t, db, "SELECT id FROM asset WHERE "+w2+" ORDER BY id", a2); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Errorf("contains-all: got %v", got)
	}
	// labels env=prod -> a1, a3, a4
	w3, a3, _ := myReg.Compile(fq.MySQL{}, []fq.Condition{
		{Key: "labels", Op: fq.OpHasKeyValues, Pairs: []fq.KeyValue{{Key: "env", Values: []string{"prod"}}}}})
	if got := ids(t, db, "SELECT id FROM asset WHERE "+w3+" ORDER BY id", a3); !reflect.DeepEqual(got, []string{"a1", "a3", "a4"}) {
		t.Errorf("has_key_values: got %v", got)
	}
}

func TestMySQL_NullKeysetAndNullsEmulation(t *testing.T) {
	db := mySetup(t)

	// NULL owner -> a2
	wn, an, _ := myReg.Compile(fq.MySQL{}, []fq.Condition{{Key: "owner", Op: fq.OpIsNull}})
	if got := ids(t, db, "SELECT id FROM asset WHERE "+wn, an); !reflect.DeepEqual(got, []string{"a2"}) {
		t.Errorf("is_null: got %v", got)
	}

	// Keyset (severity desc, id asc) after a3(8) -> a4(7), a2(5), via the Builder.
	b := myReg.Builder(fq.MySQL{})
	where, _ := b.Where([]fq.Condition{{Key: "status", Op: fq.OpEq, Values: []any{"ACTIVE"}}})
	sort := []fq.Sort{{Key: "severity", Desc: true, Nulls: fq.NullsLast}, {Key: "id"}}
	seek, _ := b.Keyset(sort, fq.Cursor{"severity": 9, "id": "a1"})
	order, _ := b.OrderBy(sort)
	q := "SELECT id FROM asset WHERE " + where + " AND " + seek + " ORDER BY " + order
	if got := ids(t, db, q, b.Args()); !reflect.DeepEqual(got, []string{"a4", "a2"}) {
		t.Errorf("keyset: got %v (query: %s)", got, q)
	}
}

func TestMySQL_Having(t *testing.T) {
	db := mySetup(t)
	_, having, args, _ := myReg.CompileWhereHaving(fq.MySQL{}, nil,
		[]fq.Condition{{Key: "finding_count", Op: fq.OpGt, Values: []any{1}}})
	if got := ids(t, db, "SELECT status FROM asset GROUP BY status HAVING "+having+" ORDER BY status", args); !reflect.DeepEqual(got, []string{"ACTIVE"}) {
		t.Errorf("having: got %v", got)
	}
}

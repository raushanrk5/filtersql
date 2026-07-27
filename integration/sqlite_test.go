package integration

import (
	"database/sql"
	"reflect"
	"testing"

	fq "github.com/raushanrk5/filtersql"
	_ "modernc.org/sqlite"
)

var reg = fq.Registry{
	"id":            {Type: fq.TypeID, Column: "id", Sortable: true},
	"name":          {Type: fq.TypeString, Column: "name", Sortable: true},
	"status":        {Type: fq.TypeEnum, Column: "status", Enum: []string{"ACTIVE", "ARCHIVED"}},
	"severity":      {Type: fq.TypeInt, Column: "severity", Sortable: true},
	"owner":         {Type: fq.TypeString, Column: "owner", Nullable: true},
	"tags":          {Type: fq.TypeArray, Column: "tags"},
	"finding_count": {Type: fq.TypeInt, Column: "count(*)", Having: true},
}

// setup builds an in-memory database with four assets and returns the handle.
//
//	id  name    status    severity  owner  tags
//	a1  web-01  ACTIVE    9         alice  ["prod","crit"]
//	a2  web-02  ACTIVE    5         NULL   ["prod"]
//	a3  db-01   ARCHIVED  8         bob    ["db"]
//	a4  api-01  ACTIVE    7         alice  ["prod","api"]
func setup(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`CREATE TABLE asset (
		id TEXT, name TEXT, status TEXT, severity INTEGER, owner TEXT, tags TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	rows := []struct {
		id, name, status string
		sev              int
		owner            any
		tags             string
	}{
		{"a1", "web-01", "ACTIVE", 9, "alice", `["prod","crit"]`},
		{"a2", "web-02", "ACTIVE", 5, nil, `["prod"]`},
		{"a3", "db-01", "ARCHIVED", 8, "bob", `["db"]`},
		{"a4", "api-01", "ACTIVE", 7, "alice", `["prod","api"]`},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO asset VALUES (?,?,?,?,?,?)`,
			r.id, r.name, r.status, r.sev, r.owner, r.tags); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
	return db
}

func ids(t *testing.T, db *sql.DB, query string, args []any) []string {
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
	if err := rs.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func TestFilter_ReturnsRightRows(t *testing.T) {
	db := setup(t)
	where, args, err := reg.Compile(fq.SQLite{}, []fq.Condition{
		{Key: "status", Op: fq.OpEq, Values: []any{"ACTIVE"}},
		{Key: "severity", Op: fq.OpGte, Values: []any{7}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(t, db, "SELECT id FROM asset WHERE "+where+" ORDER BY id", args)
	if want := []string{"a1", "a4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFilter_NullOperator(t *testing.T) {
	db := setup(t)
	where, args, _ := reg.Compile(fq.SQLite{}, []fq.Condition{{Key: "owner", Op: fq.OpIsNull}})
	got := ids(t, db, "SELECT id FROM asset WHERE "+where, args)
	if want := []string{"a2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFilter_ArrayContainsViaJSON(t *testing.T) {
	db := setup(t)

	// contains_any ["api","db"] -> a3 (db), a4 (api)
	w1, a1, _ := reg.Compile(fq.SQLite{}, []fq.Condition{{Key: "tags", Op: fq.OpContainsAny, Values: []any{"api", "db"}}})
	if got, want := ids(t, db, "SELECT id FROM asset WHERE "+w1+" ORDER BY id", a1), []string{"a3", "a4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("contains_any: got %v, want %v", got, want)
	}

	// contains ALL ["prod","crit"] -> a1 only
	w2, a2, _ := reg.Compile(fq.SQLite{}, []fq.Condition{{Key: "tags", Op: fq.OpContains, Values: []any{"prod", "crit"}}})
	if got, want := ids(t, db, "SELECT id FROM asset WHERE "+w2+" ORDER BY id", a2), []string{"a1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("contains-all: got %v, want %v", got, want)
	}
}

func TestKeysetPagination_PagesCorrectly(t *testing.T) {
	db := setup(t)
	sorts := []fq.Sort{{Key: "severity", Desc: true}, {Key: "id"}}
	order, _ := reg.OrderBy(fq.SQLite{}, sorts)
	limit, _ := fq.LimitOffset(2, 0)

	// Full order by severity desc: a1(9), a3(8), a4(7), a2(5).
	page1 := ids(t, db, "SELECT id FROM asset ORDER BY "+order+" "+limit, nil)
	if want := []string{"a1", "a3"}; !reflect.DeepEqual(page1, want) {
		t.Fatalf("page1 got %v, want %v", page1, want)
	}

	// Cursor = last row of page1 (a3, severity 8). Seek strictly after it.
	seek, sargs, err := reg.KeysetWhere(fq.SQLite{}, sorts, fq.Cursor{"severity": 8, "id": "a3"})
	if err != nil {
		t.Fatal(err)
	}
	page2 := ids(t, db, "SELECT id FROM asset WHERE "+seek+" ORDER BY "+order+" "+limit, sargs)
	if want := []string{"a4", "a2"}; !reflect.DeepEqual(page2, want) {
		t.Errorf("page2 got %v, want %v", page2, want)
	}
	// No overlap between pages — the core keyset guarantee.
	for _, id := range page2 {
		if id == "a1" || id == "a3" {
			t.Errorf("page2 leaked a page1 row: %s", id)
		}
	}
}

func TestAggregation_HavingFiltersGroups(t *testing.T) {
	db := setup(t)
	// Group by status; keep groups with more than one asset.
	// ACTIVE has 3 (a1,a2,a4), ARCHIVED has 1 (a3) -> only ACTIVE survives.
	_, having, args, err := reg.CompileWhereHaving(fq.SQLite{}, nil,
		[]fq.Condition{{Key: "finding_count", Op: fq.OpGt, Values: []any{1}}})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := db.Query("SELECT status, count(*) FROM asset GROUP BY status HAVING "+having, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rs.Close()
	var statuses []string
	for rs.Next() {
		var s string
		var n int
		if err := rs.Scan(&s, &n); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, s)
	}
	if want := []string{"ACTIVE"}; !reflect.DeepEqual(statuses, want) {
		t.Errorf("got %v, want %v", statuses, want)
	}
}

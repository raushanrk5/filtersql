package bind

import (
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"reflect"
	"testing"
)

type assetRow struct {
	ID       string `db:"id"`
	Name     string `db:"name"`
	Severity int    `db:"severity"`
	Ignored  string // no db tag -> not selected
}

var typedReg = Registry{
	"status":   {Type: TypeEnum, Column: "status", Enum: []string{"ACTIVE", "ARCHIVED"}},
	"severity": {Type: TypeInt, Column: "severity", Sortable: true},
	"id":       {Type: TypeID, Column: "id", Sortable: true},
}

func TestTyped_SQLAssembly(t *testing.T) {
	tt := For[assetRow](typedReg, "asset")
	q, args, err := tt.Select(Postgres{}).
		Scope(func(b *Builder) string { return "tenant_id = " + b.Arg("t1") }).
		Where([]Condition{{Key: "status", Op: OpEq, Values: []any{"ACTIVE"}}}).
		Sort([]Sort{{Key: "severity", Desc: true}, {Key: "id"}}).
		Limit(20).
		SQL()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT id, name, severity FROM asset WHERE tenant_id = $1 AND "status" = $2 ORDER BY severity DESC, id ASC LIMIT 20`
	if q != want {
		t.Errorf("sql:\n got %q\nwant %q", q, want)
	}
	if !reflect.DeepEqual(args, []any{"t1", "ACTIVE"}) {
		t.Errorf("args = %#v", args)
	}
}

func TestTyped_ColumnsFromDBTags(t *testing.T) {
	info := infoFor[assetRow]()
	if !reflect.DeepEqual(info.columns, []string{"id", "name", "severity"}) {
		t.Errorf("columns = %v", info.columns)
	}
	if _, ok := info.colIndex["ignored"]; ok {
		t.Error("untagged field should not be a column")
	}
}

func TestTyped_CursorAfter(t *testing.T) {
	// With a cursor, the seek predicate is appended and placeholders continue.
	tt := For[assetRow](typedReg, "asset")
	tok, _ := EncodeCursor(Cursor{"severity": 5, "id": "a3"})
	q, args, err := tt.Select(Postgres{}).
		Where([]Condition{{Key: "status", Op: OpEq, Values: []any{"ACTIVE"}}}).
		Sort([]Sort{{Key: "severity", Desc: true}, {Key: "id"}}).
		After(tok).
		SQL()
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT id, name, severity FROM asset WHERE "status" = $1 AND ((severity < $2) OR (severity = $3 AND id > $4)) ORDER BY severity DESC, id ASC`
	if q != want {
		t.Errorf("sql:\n got %q\nwant %q", q, want)
	}
	// Cursor values round-trip through JSON, so 5 comes back as float64(5).
	if !reflect.DeepEqual(args, []any{"ACTIVE", float64(5), float64(5), "a3"}) {
		t.Errorf("args = %#v", args)
	}
}

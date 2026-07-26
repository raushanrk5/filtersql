package filtersql

import (
	"reflect"
	"testing"
)

// projReg exercises ValueExpr defaulting and the round-trip invariant: the
// bool field displays Yes/No via ValueExpr, and its Column applies the same
// mapping so a filter value of "Yes" matches on the WHERE side.
var projReg = Registry{
	"asset_name": {Type: TypeString, Column: "a.name"},
	"active": {
		Type:      TypeBool,
		Column:    "if(a.is_active, 'Yes', 'No')",
		ValueExpr: "if(a.is_active, 'Yes', 'No')",
	},
	"policy_name": {Type: TypeString, Column: "p.name", ValueExpr: "p.display_name", Joins: []string{"policy"}},
	"raw":         {Type: TypeString}, // no Column, no ValueExpr → falls back to key
}

func TestProjectExprDefaulting(t *testing.T) {
	cases := map[string]string{
		"asset_name":  "a.name",         // ValueExpr empty → Column
		"policy_name": "p.display_name", // ValueExpr set → wins over Column
		"raw":         "raw",            // both empty → key
	}
	for key, want := range cases {
		p, err := projReg.Project(key)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if p.Expr != want {
			t.Errorf("%s: expr = %q, want %q", key, p.Expr, want)
		}
	}
}

func TestProjectUnknownField(t *testing.T) {
	if _, err := projReg.Project("ghost"); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestValuesQuery_ExcludesSelfFacet(t *testing.T) {
	// Values for asset_name should reflect the active-status filter but NOT the
	// asset_name filter itself.
	conds := []Condition{
		{Key: "asset_name", Op: OpLike, Values: []any{"web"}},
		{Key: "active", Op: OpEq, Values: []any{"Yes"}},
	}
	vq, err := projReg.ValuesQuery(ClickHouse{}, nil, "asset_name", conds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vq.Expr != "a.name" {
		t.Errorf("expr = %q", vq.Expr)
	}
	// asset_name's own ILIKE must be gone; only the active filter remains.
	if vq.Where != "if(a.is_active, 'Yes', 'No') = ?" {
		t.Errorf("where = %q", vq.Where)
	}
	if !reflect.DeepEqual(vq.Args, []any{true}) {
		t.Errorf("args = %#v, want [true]", vq.Args)
	}
}

func TestValuesQuery_MergesProjectionAndFilterJoins(t *testing.T) {
	// Projecting policy_name needs the policy join (which requires finding),
	// even though the only active filter is on finding_type.
	vq, err := joinReg.ValuesQuery(ClickHouse{}, joinDefs, "policy_name",
		[]Condition{{Key: "finding_type", Op: OpEq, Values: []any{"CVE"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vq.Expr != "p.name" {
		t.Errorf("expr = %q", vq.Expr)
	}
	if vq.Where != "f.type = ?" {
		t.Errorf("where = %q", vq.Where)
	}
	wantJoin := "INNER JOIN finding f ON f.asset_id = a.id\nINNER JOIN policy p ON p.finding_id = f.id"
	if vq.JoinSQL != wantJoin {
		t.Errorf("joinSQL:\n got %q\nwant %q", vq.JoinSQL, wantJoin)
	}
}

func TestCompileExcluding(t *testing.T) {
	conds := []Condition{
		{Key: "asset_name", Op: OpEq, Values: []any{"x"}},
		{Key: "active", Op: OpEq, Values: []any{"No"}},
	}
	sql, args, err := projReg.CompileExcluding(ClickHouse{}, conds, "active")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql != "a.name = ?" {
		t.Errorf("sql = %q", sql)
	}
	if !reflect.DeepEqual(args, []any{"x"}) {
		t.Errorf("args = %#v", args)
	}
}

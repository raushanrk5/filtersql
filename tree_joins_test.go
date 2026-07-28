package filtersql_test

import (
	"encoding/json"
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"reflect"
	"testing"
)

func TestNestedTree(t *testing.T) {
	// (status = OPEN AND severity >= 7) OR NOT (tags hasAny [ignored])
	tree := []Condition{{
		Or: []Condition{
			{And: []Condition{
				{Key: "status", Op: OpEq, Values: []any{"OPEN"}},
				{Key: "severity", Op: OpGte, Values: []any{7}},
			}},
			{Not: &Condition{Key: "tags", Op: OpContainsAny, Values: []any{"ignored"}}},
		},
	}}

	sql, args, err := reg.Compile(ClickHouse{}, tree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "((a.status = ? AND f.severity >= ?) OR NOT (hasAny(a.tags, ?)))"
	if sql != want {
		t.Errorf("sql:\n got %q\nwant %q", sql, want)
	}
	wantArgs := []any{"OPEN", float64(7), []string{"ignored"}}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args:\n got %#v\nwant %#v", args, wantArgs)
	}
}

func TestTreeFromJSON(t *testing.T) {
	// The wire shape a filter-builder UI would send.
	raw := `[{"or":[
		{"key":"status","op":"_eq","values":["OPEN"]},
		{"key":"severity","op":"_gte","values":[9]}
	]}]`
	var conds []Condition
	if err := json.Unmarshal([]byte(raw), &conds); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sql, args, err := reg.Compile(Postgres{}, conds)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := `("a"."status" = $1 OR "f"."severity" >= $2)`
	if sql != want {
		t.Errorf("sql:\n got %q\nwant %q", sql, want)
	}
	if !reflect.DeepEqual(args, []any{"OPEN", float64(9)}) {
		t.Errorf("args: %#v", args)
	}
}

// joinReg carries fields that require joins.
var joinReg = Registry{
	"asset_name":   {Type: TypeString, Column: "a.name"},
	"finding_type": {Type: TypeEnum, Column: "f.type", Enum: []string{"CVE", "MISCONFIG"}, Joins: []string{"finding"}},
	"policy_name":  {Type: TypeString, Column: "p.name", Joins: []string{"policy"}},
}

var joinDefs = Joins{
	// policy requires finding, finding requires nothing.
	"finding": {SQL: "INNER JOIN finding f ON f.asset_id = a.id"},
	"policy":  {SQL: "INNER JOIN policy p ON p.finding_id = f.id", Requires: []string{"finding"}},
}

func TestCompileWithJoins_OrdersDependencies(t *testing.T) {
	// Filter on policy_name alone must still pull finding in, before policy.
	where, joinSQL, args, err := joinReg.CompileWithJoins(ClickHouse{}, joinDefs,
		[]Condition{{Key: "policy_name", Op: OpEq, Values: []any{"waf"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if where != "p.name = ?" {
		t.Errorf("where = %q", where)
	}
	wantJoin := "INNER JOIN finding f ON f.asset_id = a.id\nINNER JOIN policy p ON p.finding_id = f.id"
	if joinSQL != wantJoin {
		t.Errorf("joinSQL:\n got %q\nwant %q", joinSQL, wantJoin)
	}
	if !reflect.DeepEqual(args, []any{"waf"}) {
		t.Errorf("args = %#v", args)
	}
}

func TestCompileWithJoins_NoJoinWhenFilterResolvesEmpty(t *testing.T) {
	// An empty _in list resolves to nothing, so its join must NOT be emitted.
	_, joinSQL, _, err := joinReg.CompileWithJoins(ClickHouse{}, joinDefs,
		[]Condition{{Key: "finding_type", Op: OpIn, Values: []any{}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if joinSQL != "" {
		t.Errorf("expected no joins, got %q", joinSQL)
	}
}

func TestCompileWithJoins_Deterministic(t *testing.T) {
	// Two fields needing the same base join must emit it once, deterministically.
	conds := []Condition{
		{Key: "asset_name", Op: OpEq, Values: []any{"x"}},
		{Key: "policy_name", Op: OpEq, Values: []any{"y"}},
		{Key: "finding_type", Op: OpEq, Values: []any{"CVE"}},
	}
	var first string
	for i := 0; i < 5; i++ {
		_, joinSQL, _, err := joinReg.CompileWithJoins(Postgres{}, joinDefs, conds)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if i == 0 {
			first = joinSQL
			continue
		}
		if joinSQL != first {
			t.Fatalf("non-deterministic join SQL:\n%q\nvs\n%q", first, joinSQL)
		}
	}
	// finding appears exactly once even though two fields need it.
	wantJoin := "INNER JOIN finding f ON f.asset_id = a.id\nINNER JOIN policy p ON p.finding_id = f.id"
	if first != wantJoin {
		t.Errorf("joinSQL:\n got %q\nwant %q", first, wantJoin)
	}
}

func TestOrderJoins_CycleDetected(t *testing.T) {
	reg := Registry{"x": {Type: TypeString, Column: "x", Joins: []string{"a"}}}
	cyclic := Joins{
		"a": {SQL: "JOIN a", Requires: []string{"b"}},
		"b": {SQL: "JOIN b", Requires: []string{"a"}},
	}
	_, _, _, err := reg.CompileWithJoins(ClickHouse{}, cyclic, []Condition{{Key: "x", Op: OpEq, Values: []any{"v"}}})
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

func TestOrderJoins_UndefinedKey(t *testing.T) {
	reg := Registry{"x": {Type: TypeString, Column: "x", Joins: []string{"ghost"}}}
	_, _, _, err := reg.CompileWithJoins(ClickHouse{}, Joins{}, []Condition{{Key: "x", Op: OpEq, Values: []any{"v"}}})
	if err == nil {
		t.Fatal("expected undefined-key error, got nil")
	}
}

package filtersql_test

import (
	"errors"
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"reflect"
	"strings"
	"testing"
)

// --- Raw-expression vs identifier ---

func TestRawExpressionNotQuoted(t *testing.T) {
	reg := Registry{
		"active": {Type: TypeBool, Column: "if(a.active,'Yes','No')", Raw: true},
		"name":   {Type: TypeString, Column: "a.name"},
	}
	// Raw column passes through verbatim under Postgres (no identifier quoting).
	sql, _, err := reg.Compile(Postgres{}, []Condition{{Key: "active", Op: OpEq, Values: []any{"Yes"}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql != "if(a.active,'Yes','No') = $1" {
		t.Errorf("raw expr should not be quoted, got %q", sql)
	}
	// A plain identifier IS still quoted.
	sql2, _, _ := reg.Compile(Postgres{}, []Condition{{Key: "name", Op: OpEq, Values: []any{"x"}}})
	if sql2 != `"a"."name" = $1` {
		t.Errorf("identifier should be quoted, got %q", sql2)
	}
}

// --- HAVING ---

var whReg = Registry{
	"severity":      {Type: TypeInt, Column: "f.severity"},
	"finding_count": {Type: TypeInt, Column: "count()", Having: true},
	"total_score":   {Type: TypeFloat, Column: "sum(f.score)", Having: true},
}

func TestCompileWhereHaving_ContinuousPlaceholders(t *testing.T) {
	where := []Condition{{Key: "severity", Op: OpGte, Values: []any{5}}}
	having := []Condition{
		{Key: "finding_count", Op: OpGt, Values: []any{3}},
		{Key: "total_score", Op: OpGte, Values: []any{100}},
	}
	w, h, args, err := whReg.CompileWhereHaving(Postgres{}, where, having)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != `"f"."severity" >= $1` {
		t.Errorf("where = %q", w)
	}
	// Placeholders continue across the clause boundary: $2, $3 (not restarting at $1).
	if h != "count() > $2 AND sum(f.score) >= $3" {
		t.Errorf("having = %q", h)
	}
	if !reflect.DeepEqual(args, []any{float64(5), float64(3), float64(100)}) {
		t.Errorf("args = %#v", args)
	}
}

func TestCompileWhereHaving_RejectsMisplacedFields(t *testing.T) {
	// A HAVING field in the where list.
	_, _, _, err := whReg.CompileWhereHaving(ClickHouse{},
		[]Condition{{Key: "finding_count", Op: OpGt, Values: []any{3}}}, nil)
	if !errors.Is(err, ErrInvalidCondition) {
		t.Errorf("want ErrInvalidCondition for having-field-in-where, got %v", err)
	}
	// A WHERE field in the having list.
	_, _, _, err = whReg.CompileWhereHaving(ClickHouse{}, nil,
		[]Condition{{Key: "severity", Op: OpGte, Values: []any{5}}})
	if !errors.Is(err, ErrInvalidCondition) {
		t.Errorf("want ErrInvalidCondition for where-field-in-having, got %v", err)
	}
}

// --- Registry.Validate ---

func TestValidate_OK(t *testing.T) {
	joins := Joins{
		"finding": {SQL: "INNER JOIN finding f ON f.asset_id = a.id"},
		"policy":  {SQL: "INNER JOIN policy p ON p.finding_id = f.id", Requires: []string{"finding"}},
	}
	reg := Registry{"p": {Type: TypeString, Column: "p.name", Joins: []string{"policy"}}}
	if err := reg.Validate(joins); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidate_CatchesProblems(t *testing.T) {
	cases := []struct {
		name  string
		reg   Registry
		joins Joins
	}{
		{"undefined join", Registry{"a": {Type: TypeString, Joins: []string{"ghost"}}}, nil},
		{"unknown type", Registry{"a": {Type: "weird"}}, nil},
		{"sortable and having", Registry{"a": {Type: TypeInt, Column: "count()", Sortable: true, Having: true}}, nil},
		{"cyclic joins", Registry{}, Joins{
			"a": {SQL: "JOIN a", Requires: []string{"b"}},
			"b": {SQL: "JOIN b", Requires: []string{"a"}},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.reg.Validate(c.joins); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestValidate_ReportsAllProblems(t *testing.T) {
	reg := Registry{
		"a": {Type: "weird"},
		"b": {Type: TypeString, Joins: []string{"ghost"}},
	}
	err := reg.Validate(nil)
	if err == nil {
		t.Fatal("expected errors")
	}
	// errors.Join yields a multiline message covering both fields.
	msg := err.Error()
	if !strings.Contains(msg, `"a"`) || !strings.Contains(msg, `"b"`) {
		t.Errorf("expected both fields reported, got: %s", msg)
	}
}

// --- Typed errors ---

func TestTypedErrors(t *testing.T) {
	cases := []struct {
		name   string
		cond   Condition
		target error
	}{
		{"unknown field", Condition{Key: "nope", Op: OpEq, Values: []any{"x"}}, ErrUnknownField},
		{"bad operator", Condition{Key: "severity", Op: OpLike, Values: []any{"x"}}, ErrBadOperator},
		{"enum value", Condition{Key: "status", Op: OpEq, Values: []any{"BOGUS"}}, ErrBadValue},
		{"coercion", Condition{Key: "severity", Op: OpEq, Values: []any{"abc"}}, ErrBadValue},
		{"both leaf and group", Condition{Key: "name", Op: OpEq, Values: []any{"x"}, Or: []Condition{{Key: "name", Op: OpEq, Values: []any{"y"}}}}, ErrInvalidCondition},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := reg.Compile(ClickHouse{}, []Condition{c.cond})
			if !errors.Is(err, c.target) {
				t.Errorf("want errors.Is(_, %v), got %v", c.target, err)
			}
		})
	}
}

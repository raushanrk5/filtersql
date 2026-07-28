package filtersql_test

import (
	"errors"
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"reflect"
	"strings"
	"testing"
)

func TestScalarIn(t *testing.T) {
	reg := Registry{
		"name": {Type: TypeString, Column: "a.name"},
		"sev":  {Type: TypeInt, Column: "sev"},
	}

	// Postgres: string _in -> one text[] array param.
	pg, pgArgs, _ := reg.Compile(Postgres{}, []Condition{{Key: "name", Op: OpIn, Values: []any{"a", "b"}}})
	if pg != `"a"."name" = ANY($1::text[])` || !reflect.DeepEqual(pgArgs, []any{`{"a","b"}`}) {
		t.Errorf("postgres in: %q %v", pg, pgArgs)
	}
	pgn, _, _ := reg.Compile(Postgres{}, []Condition{{Key: "name", Op: OpNin, Values: []any{"a"}}})
	if pgn != `"a"."name" <> ALL($1::text[])` {
		t.Errorf("postgres nin: %q", pgn)
	}

	// SQLite: string _in -> single JSON-array param via json_each.
	sq, sqArgs, _ := reg.Compile(SQLite{}, []Condition{{Key: "name", Op: OpIn, Values: []any{"a", "b"}}})
	if sq != `"a"."name" IN (SELECT value FROM json_each(?))` || !reflect.DeepEqual(sqArgs, []any{`["a","b"]`}) {
		t.Errorf("sqlite in: %q %v", sq, sqArgs)
	}

	// ClickHouse (no scalarInDialect): classic IN list.
	ch, _, _ := reg.Compile(ClickHouse{}, []Condition{{Key: "name", Op: OpIn, Values: []any{"a", "b"}}})
	if ch != "a.name IN (?, ?)" {
		t.Errorf("clickhouse in: %q", ch)
	}

	// Numeric _in is unaffected (keeps the IN list) even on Postgres.
	num, _, _ := reg.Compile(Postgres{}, []Condition{{Key: "sev", Op: OpIn, Values: []any{1, 2}}})
	if num != `"sev" IN ($1, $2)` {
		t.Errorf("numeric in: %q", num)
	}
}

func TestScalarIn_BigListIsOneParam(t *testing.T) {
	// 100k values would exceed Postgres's 65535 placeholder limit as an IN list;
	// with = ANY it is a single bound parameter.
	vals := make([]any, 100_000)
	for i := range vals {
		vals[i] = "x"
	}
	reg := Registry{"name": {Type: TypeString, Column: "name"}}
	sql, args, err := reg.Compile(Postgres{}, []Condition{{Key: "name", Op: OpIn, Values: vals}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(sql, "$") != 1 || len(args) != 1 {
		t.Errorf("expected a single param, got %d placeholders / %d args", strings.Count(sql, "$"), len(args))
	}
}

func TestLimits(t *testing.T) {
	reg := Registry{
		"a": {Type: TypeString, Column: "a"},
		"b": {Type: TypeString, Column: "b"},
	}

	// MaxValues: an oversized _in is rejected.
	err := Limits{MaxValues: 3}.Check([]Condition{{Key: "a", Op: OpIn, Values: []any{1, 2, 3, 4}}})
	if !errors.Is(err, ErrTooComplex) {
		t.Errorf("MaxValues: want ErrTooComplex, got %v", err)
	}

	// MaxConditions: too many leaves.
	many := []Condition{{Key: "a", Op: OpEq, Values: []any{"x"}}, {Key: "b", Op: OpEq, Values: []any{"y"}}}
	if err := (Limits{MaxConditions: 1}).Check(many); !errors.Is(err, ErrTooComplex) {
		t.Errorf("MaxConditions: want ErrTooComplex, got %v", err)
	}

	// MaxDepth: deep nesting.
	deep := []Condition{{And: []Condition{{Or: []Condition{{Not: &Condition{Key: "a", Op: OpEq, Values: []any{"x"}}}}}}}}
	if err := (Limits{MaxDepth: 2}).Check(deep); !errors.Is(err, ErrTooComplex) {
		t.Errorf("MaxDepth: want ErrTooComplex, got %v", err)
	}

	// Within limits -> ok, and CompileWithLimits proceeds.
	lim := Limits{MaxDepth: 5, MaxConditions: 10, MaxValues: 100}
	if _, _, err := reg.CompileWithLimits(ClickHouse{}, many, lim); err != nil {
		t.Errorf("within limits should compile: %v", err)
	}
}

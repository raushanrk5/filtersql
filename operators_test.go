package filtersql_test

import (
	"errors"
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"reflect"
	"testing"
)

func TestBetween(t *testing.T) {
	reg := Registry{
		"severity": {Type: TypeInt, Column: "f.severity"},
		"seen_at":  {Type: TypeTimestamp, Column: "a.seen_at"},
	}

	// numeric between -> two float args
	sql, args, err := reg.Compile(Postgres{}, []Condition{{Key: "severity", Op: OpBetween, Values: []any{3, 8}}})
	if err != nil {
		t.Fatal(err)
	}
	if sql != `"f"."severity" BETWEEN $1 AND $2` {
		t.Errorf("sql = %q", sql)
	}
	if !reflect.DeepEqual(args, []any{float64(3), float64(8)}) {
		t.Errorf("args = %#v", args)
	}

	// timestamp between -> two string args
	tsql, targs, _ := reg.Compile(ClickHouse{}, []Condition{{Key: "seen_at", Op: OpBetween, Values: []any{"2026-01-01", "2026-02-01"}}})
	if tsql != "a.seen_at BETWEEN ? AND ?" {
		t.Errorf("ts sql = %q", tsql)
	}
	if !reflect.DeepEqual(targs, []any{"2026-01-01", "2026-02-01"}) {
		t.Errorf("ts args = %#v", targs)
	}
}

func TestBetween_WrongArity(t *testing.T) {
	reg := Registry{"severity": {Type: TypeInt, Column: "f.severity"}}
	for _, vals := range [][]any{{5}, {1, 2, 3}, {}} {
		_, _, err := reg.Compile(ClickHouse{}, []Condition{{Key: "severity", Op: OpBetween, Values: vals}})
		if !errors.Is(err, ErrBadValue) {
			t.Errorf("values %v: want ErrBadValue, got %v", vals, err)
		}
	}
}

func TestEndsWith(t *testing.T) {
	reg := Registry{"name": {Type: TypeString, Column: "a.name"}}
	cases := []struct {
		d       Dialect
		wantSQL string
		wantArg any
	}{
		{ClickHouse{}, "a.name ILIKE ?", "%web"},
		{Postgres{}, `"a"."name" ILIKE $1`, "%web"},
		{SQLite{}, `"a"."name" LIKE ? ESCAPE '\'`, "%web"},
	}
	for _, c := range cases {
		sql, args, err := reg.Compile(c.d, []Condition{{Key: "name", Op: OpEndsWith, Values: []any{"web"}}})
		if err != nil {
			t.Fatalf("%T: %v", c.d, err)
		}
		if sql != c.wantSQL || !reflect.DeepEqual(args, []any{c.wantArg}) {
			t.Errorf("%T: got %q %v", c.d, sql, args)
		}
	}
}

func TestOnlyAllowlist(t *testing.T) {
	reg := Registry{"body": {Type: TypeString, Column: "a.body", Only: []Operator{OpEq}}}

	// _eq allowed
	if _, _, err := reg.Compile(ClickHouse{}, []Condition{{Key: "body", Op: OpEq, Values: []any{"x"}}}); err != nil {
		t.Errorf("_eq should be allowed: %v", err)
	}
	// _like denied (not in Only)
	_, _, err := reg.Compile(ClickHouse{}, []Condition{{Key: "body", Op: OpLike, Values: []any{"x"}}})
	if !errors.Is(err, ErrBadOperator) {
		t.Errorf("_like should be denied, got %v", err)
	}
	// schema advertises exactly the allowlist
	for _, fs := range reg.Schema() {
		if fs.Key == "body" && !reflect.DeepEqual(fs.Operators, []Operator{OpEq}) {
			t.Errorf("schema operators = %v, want [_eq]", fs.Operators)
		}
	}
}

func TestExceptDenylist(t *testing.T) {
	reg := Registry{"name": {Type: TypeString, Column: "a.name", Except: []Operator{OpLike, OpEndsWith, OpStartsWith}}}

	// _eq still allowed
	if _, _, err := reg.Compile(ClickHouse{}, []Condition{{Key: "name", Op: OpEq, Values: []any{"x"}}}); err != nil {
		t.Errorf("_eq should be allowed: %v", err)
	}
	// _like removed
	_, _, err := reg.Compile(ClickHouse{}, []Condition{{Key: "name", Op: OpLike, Values: []any{"x"}}})
	if !errors.Is(err, ErrBadOperator) {
		t.Errorf("_like should be denied, got %v", err)
	}
	// schema must not advertise the denied ops
	for _, fs := range reg.Schema() {
		if fs.Key == "name" {
			for _, o := range fs.Operators {
				if o == OpLike || o == OpEndsWith || o == OpStartsWith {
					t.Errorf("schema advertised denied op %q", o)
				}
			}
		}
	}
}

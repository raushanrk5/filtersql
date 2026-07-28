package filtersql_test

import (
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"testing"
)

// FuzzInjectionInvariant is the executable form of the safety claim: the
// compiled SQL structure is *independent of the value*. For a fixed field and
// operator, no matter what bytes the user sends, the SQL string is byte-for-byte
// constant and the value appears only in the bound args. If that holds for
// arbitrary input, a value can never alter SQL structure — i.e. no injection.
func FuzzInjectionInvariant(f *testing.F) {
	reg := Registry{"c": {Type: TypeString, Column: "t.c"}}

	seeds := []string{
		"", "plain", "O'Brien",
		"'; DROP TABLE t;--",
		`" OR "1"="1`,
		"100% off_ma\\tch", // LIKE wildcards + backslash
		"$1", "?", ") OR (1=1",
		"a\x00b", "🙂", "\n\t;",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	// Per dialect, the exact SQL a scalar _eq / _like must always produce,
	// regardless of the value.
	type want struct {
		d        Dialect
		eq, like string
	}
	wants := []want{
		{ClickHouse{}, "t.c = ?", "t.c ILIKE ?"},
		{Postgres{}, `"t"."c" = $1`, `"t"."c" ILIKE $1`},
		{SQLite{}, `"t"."c" = ?`, `"t"."c" LIKE ? ESCAPE '\'`},
		{MySQL{}, "`t`.`c` = ?", "LOWER(`t`.`c`) LIKE ?"},
	}

	f.Fuzz(func(t *testing.T, v string) {
		for _, w := range wants {
			// _eq: SQL constant; the raw value is the sole bind arg.
			sql, args, err := reg.Compile(w.d, []Condition{{Key: "c", Op: OpEq, Values: []any{v}}})
			if err != nil {
				t.Fatalf("%T eq: %v", w.d, err)
			}
			if sql != w.eq {
				t.Fatalf("%T eq SQL varied with value: got %q want %q", w.d, sql, w.eq)
			}
			if len(args) != 1 || args[0] != v {
				t.Fatalf("%T eq value not parameterized: %#v", w.d, args)
			}

			// _like: SQL constant; value goes to args (wrapped/escaped), never inline.
			lsql, largs, err := reg.Compile(w.d, []Condition{{Key: "c", Op: OpLike, Values: []any{v}}})
			if err != nil {
				t.Fatalf("%T like: %v", w.d, err)
			}
			if lsql != w.like {
				t.Fatalf("%T like SQL varied with value: got %q want %q", w.d, lsql, w.like)
			}
			if len(largs) != 1 {
				t.Fatalf("%T like arg count: %#v", w.d, largs)
			}
		}
	})
}

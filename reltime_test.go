package filtersql

import (
	"errors"
	"testing"
)

func TestRelativeTime(t *testing.T) {
	reg := Registry{"created": {Type: TypeTimestamp, Column: "a.created"}}

	cases := []struct {
		name string
		d    Dialect
		op   Operator
		val  string
		want string
	}{
		{"clickhouse last 7d", ClickHouse{}, OpLast, "7d",
			"a.created BETWEEN now() - INTERVAL 7 DAY AND now()"},
		{"postgres last 24h", Postgres{}, OpLast, "24h",
			`"a"."created" BETWEEN now() - interval '24 hours' AND now()`},
		{"sqlite last 2w", SQLite{}, OpLast, "2w",
			`"a"."created" BETWEEN datetime('now', '-14 days') AND datetime('now')`},
		{"clickhouse within 30m", ClickHouse{}, OpWithin, "30m",
			"a.created BETWEEN now() AND now() + INTERVAL 30 MINUTE"},
		{"postgres within 1w", Postgres{}, OpWithin, "1w",
			`"a"."created" BETWEEN now() AND now() + interval '1 weeks'`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sql, args, err := reg.Compile(c.d, []Condition{{Key: "created", Op: c.op, Values: []any{c.val}}})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sql != c.want {
				t.Errorf("sql:\n got %q\nwant %q", sql, c.want)
			}
			// Time expressions are inlined, so no bind args.
			if len(args) != 0 {
				t.Errorf("expected no args, got %#v", args)
			}
		})
	}
}

func TestRelativeTime_BadInterval(t *testing.T) {
	reg := Registry{"created": {Type: TypeTimestamp, Column: "created"}}
	for _, v := range []string{"", "7", "d", "7x", "-3d", "0d", "abc"} {
		_, _, err := reg.Compile(ClickHouse{}, []Condition{{Key: "created", Op: OpLast, Values: []any{v}}})
		if !errors.Is(err, ErrBadValue) {
			t.Errorf("interval %q: want ErrBadValue, got %v", v, err)
		}
	}
}

func TestParseInterval(t *testing.T) {
	ok := map[string]struct {
		n int
		u TimeUnit
	}{
		"30m": {30, Minute}, "24h": {24, Hour}, "7d": {7, Day}, "2w": {2, Week},
	}
	for in, want := range ok {
		n, u, err := parseInterval(in)
		if err != nil || n != want.n || u != want.u {
			t.Errorf("parseInterval(%q) = %d,%v,%v", in, n, u, err)
		}
	}
}

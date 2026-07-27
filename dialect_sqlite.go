package filtersql

import (
	"fmt"
	"strings"
)

// SQLite renders filters for SQLite: "?" placeholders, LIKE (ASCII
// case-insensitive by default) with an explicit ESCAPE, and JSON1 functions for
// array/map columns stored as JSON text. It doubles as a zero-setup engine for
// executing tests and runnable examples.
//
// Array columns are assumed to be JSON arrays (e.g. '["prod","crit"]'); map
// columns JSON objects (e.g. '{"env":"prod"}'). Both are queried with json_each.
type SQLite struct{}

func (SQLite) Placeholder(int) string { return "?" }

// QuoteIdent double-quotes each dotted segment (table."column").
func (SQLite) QuoteIdent(ident string) string {
	segs := strings.Split(ident, ".")
	for i, s := range segs {
		segs[i] = `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return strings.Join(segs, ".")
}

// Like uses LIKE with an explicit ESCAPE, since SQLite has no default escape
// character. escapeLike (shared) neutralizes % and _ in the literal value.
func (SQLite) Like(q *Query, col, pattern string, match LikeMatch) string {
	return fmt.Sprintf(`%s LIKE %s ESCAPE '\'`, col, q.Arg(likeNeedle(pattern, match)))
}

// ArrayContains queries a JSON-array column via json_each. "any" is a simple
// EXISTS; "all" counts the distinct matches and compares to the wanted size.
func (SQLite) ArrayContains(q *Query, col string, values []string, all bool) string {
	marks := make([]string, len(values))
	for i, v := range values {
		marks[i] = q.Arg(v)
	}
	in := strings.Join(marks, ", ")
	if all {
		// every wanted value must appear at least once
		return fmt.Sprintf(
			"(SELECT count(DISTINCT value) FROM json_each(%s) WHERE value IN (%s)) = %s",
			col, in, q.Arg(len(values)),
		)
	}
	return fmt.Sprintf("EXISTS (SELECT 1 FROM json_each(%s) WHERE value IN (%s))", col, in)
}

// MapHasKeys tests a JSON-object column for the presence of each key.
func (SQLite) MapHasKeys(q *Query, col string, keys []string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("EXISTS (SELECT 1 FROM json_each(%s) WHERE key = %s)", col, q.Arg(k))
	}
	return strings.Join(parts, " AND ")
}

// MapHasKeyValues tests a JSON-object column for each key/value pair.
func (SQLite) MapHasKeyValues(q *Query, col string, pairs []KeyValue) string {
	var parts []string
	for _, p := range pairs {
		for _, v := range p.Values {
			parts = append(parts, fmt.Sprintf(
				"EXISTS (SELECT 1 FROM json_each(%s) WHERE key = %s AND value = %s)",
				col, q.Arg(p.Key), q.Arg(v),
			))
		}
	}
	return strings.Join(parts, " AND ")
}

// Aggregate renders an aggregate call; SQLite spells "count all" as count(*).
func (SQLite) Aggregate(fn AggFunc, expr string) string {
	return aggCall(fn, expr, "count(*)")
}

// OrderTerm uses SQLite's native NULLS FIRST/LAST support (3.30+).
func (SQLite) OrderTerm(expr string, desc bool, nulls NullsOrder) string {
	return stdOrderTerm(expr, desc, nulls)
}

// ScalarIn binds one JSON-array param and expands it with json_each, so a large
// _in list uses a single placeholder instead of one per value.
func (SQLite) ScalarIn(q *Query, col string, values []string, negate bool) string {
	kw := "IN"
	if negate {
		kw = "NOT IN"
	}
	return fmt.Sprintf("%s %s (SELECT value FROM json_each(%s))", col, kw, q.Arg(jsonArrayLiteral(values)))
}

func (SQLite) Now() string { return "datetime('now')" }

func (SQLite) RelativeTime(amount int, unit TimeUnit) string {
	// SQLite datetime modifiers have no 'weeks' — express weeks as days.
	mod := "days"
	switch unit {
	case Minute:
		mod = "minutes"
	case Hour:
		mod = "hours"
	case Week:
		amount *= 7
	}
	sign := "+"
	if amount < 0 {
		sign, amount = "-", -amount
	}
	return fmt.Sprintf("datetime('now', '%s%d %s')", sign, amount, mod)
}

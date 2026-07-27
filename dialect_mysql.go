package filtersql

import (
	"fmt"
	"strings"
)

// MySQL renders filters for MySQL 8+. It diverges from the other dialects on
// almost every axis, which makes it the sternest test of the Dialect seam:
//   - "?" placeholders, backtick-quoted identifiers;
//   - LOWER(col) LIKE ? for guaranteed case-insensitivity (no ILIKE);
//   - JSON columns for arrays (JSON_CONTAINS / JSON_OVERLAPS) and maps
//     (JSON_CONTAINS_PATH / JSON_EXTRACT);
//   - no native NULLS FIRST/LAST — emulated with a leading "IS NULL" sort key.
type MySQL struct{}

func (MySQL) Placeholder(int) string { return "?" }

// QuoteIdent backtick-quotes each dotted segment.
func (MySQL) QuoteIdent(ident string) string {
	segs := strings.Split(ident, ".")
	for i, s := range segs {
		segs[i] = "`" + strings.ReplaceAll(s, "`", "``") + "`"
	}
	return strings.Join(segs, ".")
}

// Like lowercases both sides so the match is case-insensitive regardless of the
// column's collation. MySQL uses backslash as the default LIKE escape, so the
// shared escapeLike (via likeNeedle) needs no ESCAPE clause.
func (MySQL) Like(q *Query, col, pattern string, match LikeMatch) string {
	needle := strings.ToLower(likeNeedle(pattern, match))
	return fmt.Sprintf("LOWER(%s) LIKE %s", col, q.Arg(needle))
}

// ArrayContains treats the column as a JSON array. JSON_CONTAINS(target, cand)
// requires all of cand present (superset); JSON_OVERLAPS is intersection.
func (MySQL) ArrayContains(q *Query, col string, values []string, all bool) string {
	arg := q.Arg(jsonArrayLiteral(values))
	if all {
		return fmt.Sprintf("JSON_CONTAINS(%s, %s)", col, arg)
	}
	return fmt.Sprintf("JSON_OVERLAPS(%s, %s)", col, arg)
}

// MapHasKeys tests a JSON object for each key via a bound JSON path.
func (MySQL) MapHasKeys(q *Query, col string, keys []string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("JSON_CONTAINS_PATH(%s, 'one', %s)", col, q.Arg(jsonPath(k)))
	}
	return strings.Join(parts, " AND ")
}

// MapHasKeyValues compares the unquoted JSON value at each key's path.
func (MySQL) MapHasKeyValues(q *Query, col string, pairs []KeyValue) string {
	var parts []string
	for _, p := range pairs {
		for _, v := range p.Values {
			parts = append(parts, fmt.Sprintf(
				"JSON_UNQUOTE(JSON_EXTRACT(%s, %s)) = %s", col, q.Arg(jsonPath(p.Key)), q.Arg(v)))
		}
	}
	return strings.Join(parts, " AND ")
}

// Aggregate renders an aggregate call; MySQL spells "count all" as count(*).
func (MySQL) Aggregate(fn AggFunc, expr string) string {
	return aggCall(fn, expr, "count(*)")
}

// OrderTerm emulates NULLS FIRST/LAST, which MySQL has no syntax for, with a
// leading boolean sort key. MySQL's default is NULLs-first on ASC / last on DESC.
func (MySQL) OrderTerm(expr string, desc bool, nulls NullsOrder) string {
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	switch nulls {
	case NullsFirst:
		return fmt.Sprintf("%s IS NOT NULL, %s %s", expr, expr, dir)
	case NullsLast:
		return fmt.Sprintf("%s IS NULL, %s %s", expr, expr, dir)
	default:
		return fmt.Sprintf("%s %s", expr, dir)
	}
}

func (MySQL) Now() string { return "NOW()" }

func (MySQL) RelativeTime(amount int, unit TimeUnit) string {
	op := "+"
	if amount < 0 {
		op, amount = "-", -amount
	}
	kw := map[TimeUnit]string{Minute: "MINUTE", Hour: "HOUR", Day: "DAY", Week: "WEEK"}[unit]
	return fmt.Sprintf("NOW() %s INTERVAL %d %s", op, amount, kw)
}

package dialects

import (
	"fmt"
	fq "github.com/raushanrk5/filtersql"
	"strings"
)

// ClickHouse renders filters for ClickHouse: "?" placeholders, ILIKE for text,
// hasAll/hasAny for arrays, and mapContains / col[key] for maps.
type ClickHouse struct{}

func (ClickHouse) Placeholder(int) string { return "?" }

// QuoteIdent leaves identifiers unquoted. ClickHouse dotted paths like
// `nested.field` are field accessors, not table-qualified names, and must not
// be back-quoted per segment.
func (ClickHouse) QuoteIdent(ident string) string { return ident }

func (ClickHouse) Like(q *fq.Query, col, pattern string, match fq.LikeMatch) string {
	return fmt.Sprintf("%s ILIKE %s", col, q.Arg(likeNeedle(pattern, match)))
}

func (ClickHouse) ArrayContains(q *fq.Query, col string, values []string, all bool) string {
	fn := "hasAny"
	if all {
		fn = "hasAll"
	}
	// The ClickHouse driver binds a []string as a single array argument.
	return fmt.Sprintf("%s(%s, %s)", fn, col, q.Arg(values))
}

func (ClickHouse) MapHasKeys(q *fq.Query, col string, keys []string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("mapContains(%s, %s)", col, q.Arg(k))
	}
	return strings.Join(parts, " AND ")
}

// Aggregate renders an aggregate call; ClickHouse spells "count all" as count().
func (ClickHouse) Aggregate(fn fq.AggFunc, expr string) string {
	return aggCall(fn, expr, "count()")
}

// OrderTerm uses ClickHouse's native NULLS FIRST/LAST support.
func (ClickHouse) OrderTerm(expr string, desc bool, nulls fq.NullsOrder) string {
	return stdOrderTerm(expr, desc, nulls)
}

func (ClickHouse) Now() string { return "now()" }

func (ClickHouse) RelativeTime(amount int, unit fq.TimeUnit) string {
	op := "+"
	if amount < 0 {
		op, amount = "-", -amount
	}
	kw := map[fq.TimeUnit]string{fq.Minute: "MINUTE", fq.Hour: "HOUR", fq.Day: "DAY", fq.Week: "WEEK"}[unit]
	return fmt.Sprintf("now() %s INTERVAL %d %s", op, amount, kw)
}

func (ClickHouse) MapHasKeyValues(q *fq.Query, col string, pairs []fq.KeyValue) string {
	var parts []string
	for _, p := range pairs {
		for _, v := range p.Values {
			parts = append(parts, fmt.Sprintf("%s[%s] = %s", col, q.Arg(p.Key), q.Arg(v)))
		}
	}
	return strings.Join(parts, " AND ")
}

package dialects

import (
	"fmt"
	fq "github.com/raushanrk5/filtersql"
	"strconv"
	"strings"
)

// Postgres renders filters for PostgreSQL: "$N" placeholders, ILIKE for text,
// array overlap/containment operators, and jsonb key/value tests. Array-typed
// columns are assumed to be native arrays; map-typed columns are assumed jsonb.
type Postgres struct{}

func (Postgres) Placeholder(n int) string { return "$" + strconv.Itoa(n) }

// QuoteIdent double-quotes each dotted segment (schema.table.column).
func (Postgres) QuoteIdent(ident string) string {
	segs := strings.Split(ident, ".")
	for i, s := range segs {
		segs[i] = `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return strings.Join(segs, ".")
}

func (Postgres) Like(q *fq.Query, col, pattern string, match fq.LikeMatch) string {
	return fmt.Sprintf("%s ILIKE %s", col, q.Arg(likeNeedle(pattern, match)))
}

func (Postgres) ArrayContains(q *fq.Query, col string, values []string, all bool) string {
	op := "&&" // overlap: contains any
	if all {
		op = "@>" // contains: superset
	}
	// Explicit ::text[] cast: the arg is bound as an array *literal string*, so
	// without the cast Postgres must guess its type from context — which fails on
	// some operator/driver combinations. The cast makes it unambiguous.
	return fmt.Sprintf("%s %s %s::text[]", col, op, q.Arg(pgTextArray(values)))
}

func (Postgres) MapHasKeys(q *fq.Query, col string, keys []string) string {
	if len(keys) == 1 {
		return fmt.Sprintf("%s ? %s", col, q.Arg(keys[0]))
	}
	// jsonb ?& text[] : has all keys
	return fmt.Sprintf("%s ?& %s::text[]", col, q.Arg(pgTextArray(keys)))
}

// Aggregate renders an aggregate call; Postgres spells "count all" as count(*).
func (Postgres) Aggregate(fn fq.AggFunc, expr string) string {
	return aggCall(fn, expr, "count(*)")
}

// OrderTerm uses Postgres's native NULLS FIRST/LAST support.
func (Postgres) OrderTerm(expr string, desc bool, nulls fq.NullsOrder) string {
	return stdOrderTerm(expr, desc, nulls)
}

// ScalarIn binds one text[] param, so a large _in list can't blow the 65535
// parameter limit. col = ANY($1::text[]) / col <> ALL($1::text[]).
func (Postgres) ScalarIn(q *fq.Query, col string, values []string, negate bool) string {
	arg := q.Arg(pgTextArray(values))
	if negate {
		return fmt.Sprintf("%s <> ALL(%s::text[])", col, arg)
	}
	return fmt.Sprintf("%s = ANY(%s::text[])", col, arg)
}

func (Postgres) Now() string { return "now()" }

func (Postgres) RelativeTime(amount int, unit fq.TimeUnit) string {
	op := "+"
	if amount < 0 {
		op, amount = "-", -amount
	}
	kw := map[fq.TimeUnit]string{fq.Minute: "minutes", fq.Hour: "hours", fq.Day: "days", fq.Week: "weeks"}[unit]
	return fmt.Sprintf("now() %s interval '%d %s'", op, amount, kw)
}

func (Postgres) MapHasKeyValues(q *fq.Query, col string, pairs []fq.KeyValue) string {
	var parts []string
	for _, p := range pairs {
		for _, v := range p.Values {
			parts = append(parts, fmt.Sprintf("%s ->> %s = %s", col, q.Arg(p.Key), q.Arg(v)))
		}
	}
	return strings.Join(parts, " AND ")
}

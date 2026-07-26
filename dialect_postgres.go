package filtersql

import (
	"fmt"
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

func (Postgres) Like(q *Query, col, pattern string, prefix bool) string {
	needle := escapeLike(pattern) + "%"
	if !prefix {
		needle = "%" + needle
	}
	return fmt.Sprintf("%s ILIKE %s", col, q.Arg(needle))
}

func (Postgres) ArrayContains(q *Query, col string, values []string, all bool) string {
	op := "&&" // overlap: contains any
	if all {
		op = "@>" // contains: superset
	}
	return fmt.Sprintf("%s %s %s", col, op, q.Arg(pgTextArray(values)))
}

func (Postgres) MapHasKeys(q *Query, col string, keys []string) string {
	if len(keys) == 1 {
		return fmt.Sprintf("%s ? %s", col, q.Arg(keys[0]))
	}
	// jsonb ?& array : has all keys
	return fmt.Sprintf("%s ?& %s", col, q.Arg(pgTextArray(keys)))
}

func (Postgres) MapHasKeyValues(q *Query, col string, pairs []KeyValue) string {
	var parts []string
	for _, p := range pairs {
		for _, v := range p.Values {
			parts = append(parts, fmt.Sprintf("%s ->> %s = %s", col, q.Arg(p.Key), q.Arg(v)))
		}
	}
	return strings.Join(parts, " AND ")
}

// pgTextArray formats a Go slice as a Postgres text-array literal. Values are
// escaped so they cannot break out of the literal.
func pgTextArray(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

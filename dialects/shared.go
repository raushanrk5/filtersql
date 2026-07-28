package dialects

import (
	"encoding/json"
	fq "github.com/raushanrk5/filtersql"
	"strings"
)

// Helpers shared by the concrete Dialect implementations. They live together so
// the dialect files stay focused on the Dialect interface methods themselves.

// escapeLike escapes LIKE wildcards so a literal value can't inject patterns.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// likeNeedle escapes the pattern's wildcards and wraps it for the requested
// anchoring: substring (%x%), prefix (x%), or suffix (%x).
func likeNeedle(pattern string, m fq.LikeMatch) string {
	p := escapeLike(pattern)
	switch m {
	case fq.MatchPrefix:
		return p + "%"
	case fq.MatchSuffix:
		return "%" + p
	default:
		return "%" + p + "%"
	}
}

// stdOrderTerm renders a standard-SQL ORDER BY term with native NULLS FIRST/LAST
// (ClickHouse, Postgres, SQLite); MySQL emulates it instead.
func stdOrderTerm(expr string, desc bool, nulls fq.NullsOrder) string {
	s := expr
	if desc {
		s += " DESC"
	} else {
		s += " ASC"
	}
	switch nulls {
	case fq.NullsFirst:
		s += " NULLS FIRST"
	case fq.NullsLast:
		s += " NULLS LAST"
	}
	return s
}

// aggCall renders the SQL for an aggregate. countAll is the dialect's "count all
// rows" form used when fn is Count with no expr.
func aggCall(fn fq.AggFunc, expr, countAll string) string {
	switch fn {
	case fq.Count:
		if expr == "" {
			return countAll
		}
		return "count(" + expr + ")"
	case fq.CountDistinct:
		return "count(DISTINCT " + expr + ")"
	default: // Sum, Avg, Min, Max — the const value is the SQL function name
		return string(fn) + "(" + expr + ")"
	}
}

// pgTextArray formats a slice as a Postgres text-array literal (e.g. {"a","b"}),
// escaping each value so it can't break out of the literal.
func pgTextArray(vals []string) string {
	quoted := make([]string, len(vals))
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	for i, v := range vals {
		quoted[i] = `"` + esc.Replace(v) + `"`
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

// jsonArrayLiteral renders values as a JSON array literal, e.g. ["a","b"].
// Used by the MySQL and SQLite dialects.
func jsonArrayLiteral(vals []string) string {
	b, _ := json.Marshal(vals)
	return string(b)
}

// jsonPath renders a JSON path to a top-level key, e.g. $."env". Used by MySQL.
func jsonPath(key string) string {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(key)
	return `$."` + esc + `"`
}

package filtersql

// Dialect renders the dialect-specific corners of a filter. Everything that is
// portable — column = ?, IN (...), numeric comparisons — is handled by the
// core Compiler using nothing but Placeholder and QuoteIdent. Only the genuinely
// divergent operations are delegated here.
//
// A Dialect must be stateless; per-query argument state lives on *Query.
type Dialect interface {
	// Placeholder returns the bind marker for the n-th argument (1-based).
	// e.g. "?" for ClickHouse/MySQL, "$1"/"$2" for Postgres.
	Placeholder(n int) string

	// QuoteIdent quotes an identifier that may be a qualified path (a.b.c).
	// A dialect may return the identifier unchanged if quoting is unnecessary.
	QuoteIdent(ident string) string

	// Like renders a case-insensitive text match. pattern is the raw needle;
	// match selects substring / prefix / suffix anchoring. The dialect is
	// responsible for wildcard escaping and for choosing ILIKE vs lower()/LIKE.
	Like(q *Query, col, pattern string, match LikeMatch) string

	// ArrayContains renders array membership. all=true means "contains every
	// value" (superset); all=false means "contains any value" (intersection).
	ArrayContains(q *Query, col string, values []string, all bool) string

	// MapHasKeys renders a "has all these keys" test.
	MapHasKeys(q *Query, col string, keys []string) string

	// MapHasKeyValues renders a "for each key, value matches" test.
	MapHasKeyValues(q *Query, col string, pairs []KeyValue) string

	// Aggregate renders an aggregate function call over expr. When fn is Count
	// and expr is empty, the dialect emits its "count all rows" form (count()
	// for ClickHouse, count(*) elsewhere). Other functions wrap expr, e.g.
	// avg(expr) or count(DISTINCT expr).
	Aggregate(fn AggFunc, expr string) string

	// OrderTerm renders one ORDER BY term: expr with ASC/DESC and NULLS handling.
	// ClickHouse/Postgres/SQLite support "NULLS FIRST|LAST" natively; a dialect
	// without it (MySQL) emulates via a leading "expr IS NULL" term.
	OrderTerm(expr string, desc bool, nulls NullsOrder) string

	// Now renders the current-timestamp expression (now(), datetime('now'), …).
	Now() string
	// RelativeTime renders "now shifted by amount of unit"; a negative amount is
	// in the past. amount is a validated integer and unit a fixed keyword, so the
	// result is safe to inline. e.g. Postgres now() - interval '7 days'.
	RelativeTime(amount int, unit TimeUnit) string
}

// likeNeedle escapes the pattern's wildcards and wraps it for the requested
// anchoring: substring (%x%), prefix (x%), or suffix (%x). Shared by dialects.
func likeNeedle(pattern string, m LikeMatch) string {
	p := escapeLike(pattern)
	switch m {
	case MatchPrefix:
		return p + "%"
	case MatchSuffix:
		return "%" + p
	default:
		return "%" + p + "%"
	}
}

// stdOrderTerm renders a standard-SQL ORDER BY term with native NULLS FIRST/LAST.
// Shared by dialects that support that syntax.
func stdOrderTerm(expr string, desc bool, nulls NullsOrder) string {
	s := expr
	if desc {
		s += " DESC"
	} else {
		s += " ASC"
	}
	switch nulls {
	case NullsFirst:
		s += " NULLS FIRST"
	case NullsLast:
		s += " NULLS LAST"
	}
	return s
}

// Query accumulates bind arguments while a filter is compiled and hands back
// the right placeholder for each. Dialects call Arg to bind a value. It also
// records which join keys were referenced by conditions that actually emitted.
type Query struct {
	d        Dialect
	args     []any
	joinKeys map[string]bool
	exclude  string // when set, leaves on this field key are skipped (facet self-exclusion)
}

func newQuery(d Dialect) *Query {
	return &Query{d: d, joinKeys: map[string]bool{}}
}

// Arg binds v and returns its placeholder (e.g. "?" or "$3").
func (q *Query) Arg(v any) string {
	q.args = append(q.args, v)
	return q.d.Placeholder(len(q.args))
}

// Args returns the accumulated bind arguments in order.
func (q *Query) Args() []any { return q.args }

// Ident quotes an identifier via the active dialect.
func (q *Query) Ident(name string) string { return q.d.QuoteIdent(name) }

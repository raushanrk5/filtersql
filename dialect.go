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

	// Like renders a case-insensitive text match. pattern is the raw needle
	// (already stripped of wildcards); prefix=true means anchored prefix match,
	// otherwise substring. The dialect is responsible for wildcard escaping and
	// for choosing ILIKE vs lower()/LIKE.
	Like(q *Query, col, pattern string, prefix bool) string

	// ArrayContains renders array membership. all=true means "contains every
	// value" (superset); all=false means "contains any value" (intersection).
	ArrayContains(q *Query, col string, values []string, all bool) string

	// MapHasKeys renders a "has all these keys" test.
	MapHasKeys(q *Query, col string, keys []string) string

	// MapHasKeyValues renders a "for each key, value matches" test.
	MapHasKeyValues(q *Query, col string, pairs []KeyValue) string
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

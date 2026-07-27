package filtersql

// Builder assembles the fragments of a single SQL statement through one shared
// placeholder counter. That is what keeps the args from WHERE, the keyset seek,
// and HAVING lined up — and, for $N dialects like Postgres, keeps their
// placeholder numbers continuous. (Rendering those pieces with separate calls
// each restarts at $1, so their placeholders collide when combined.)
//
// The caller still owns SELECT / FROM and tenant scoping — bind those values
// with Arg so they interleave in the right positions. Call the render methods in
// the order the fragments appear in the final statement (all WHERE parts first,
// then HAVING) so Args comes back in matching order.
//
//	b := reg.Builder(filtersql.Postgres{})
//	tenant := b.Arg(tenantID)          // $1
//	where, _ := b.Where(filters)       // $2 …
//	seek, _  := b.Keyset(sort, cursor) // continues …
//	order, _ := b.OrderBy(sort)
//	page, _  := b.Limit(50, 0)
//
//	sql := "SELECT id FROM asset WHERE tenant_id = " + tenant
//	if where != "" { sql += " AND " + where }
//	if seek  != "" { sql += " AND " + seek }
//	sql += " ORDER BY " + order + " " + page
//	rows, _ := db.Query(sql, b.Args()...)
type Builder struct {
	r Registry
	q *Query
}

// Builder starts a new statement builder for the given dialect.
func (r Registry) Builder(d Dialect) *Builder {
	return &Builder{r: r, q: newQuery(d)}
}

// Arg binds a caller-supplied value (tenant id, a literal, …) and returns its
// placeholder, so caller-owned predicates share the builder's numbering.
func (b *Builder) Arg(v any) string { return b.q.Arg(v) }

// Where renders the filter WHERE fragment against the shared counter.
func (b *Builder) Where(conds []Condition) (string, error) {
	return b.r.renderTop(b.q, conds)
}

// WhereExcluding is Where with a self-excluded facet field (see CompileExcluding).
func (b *Builder) WhereExcluding(conds []Condition, exclude string) (string, error) {
	saved := b.q.exclude
	b.q.exclude = exclude
	sql, err := b.r.renderTop(b.q, conds)
	b.q.exclude = saved
	return sql, err
}

// Keyset renders the seek predicate for a cursor, continuing the counter.
func (b *Builder) Keyset(sorts []Sort, cur Cursor) (string, error) {
	return b.r.keysetInto(b.q, sorts, cur)
}

// Having renders a HAVING fragment; every field referenced must be a Having field.
func (b *Builder) Having(conds []Condition) (string, error) {
	if err := b.r.requireKind(conds, true); err != nil {
		return "", err
	}
	return b.r.renderTop(b.q, conds)
}

// OrderBy renders the ORDER BY body (no args; Sortable-gated).
func (b *Builder) OrderBy(sorts []Sort) (string, error) {
	return b.r.OrderBy(b.q.d, sorts)
}

// Limit renders "LIMIT n OFFSET m" (integers inlined; no args).
func (b *Builder) Limit(limit, offset int) (string, error) {
	return LimitOffset(limit, offset)
}

// Args returns every bound argument in the order it was bound.
func (b *Builder) Args() []any { return b.q.Args() }

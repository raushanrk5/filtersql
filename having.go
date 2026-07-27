package filtersql

import "fmt"

// CompileWhereHaving compiles two filter lists against one registry: `where`
// (fields filtered before grouping) and `having` (Having fields — aggregate
// expressions filtered after GROUP BY). It returns both fragments and a single
// combined argument slice, in WHERE-then-HAVING order.
//
// A shared argument counter spans both clauses, so numbered placeholders ($1,
// $2, …) stay continuous across the final query — which they must, since WHERE
// and HAVING live in the same statement.
//
// Each list is validated against the fields' Having flag: a non-Having field in
// `having`, or a Having field in `where`, is an ErrInvalidCondition — those
// can't be expressed in the wrong clause. A boolean group may not mix the two.
func (r Registry) CompileWhereHaving(d Dialect, where, having []Condition) (whereSQL, havingSQL string, args []any, err error) {
	if err = r.requireKind(where, false); err != nil {
		return "", "", nil, err
	}
	if err = r.requireKind(having, true); err != nil {
		return "", "", nil, err
	}

	q := newQuery(d)
	if whereSQL, err = r.renderTop(q, where); err != nil {
		return "", "", nil, err
	}
	if havingSQL, err = r.renderTop(q, having); err != nil {
		return "", "", nil, err
	}
	return whereSQL, havingSQL, q.Args(), nil
}

// requireKind checks every leaf in conds references a field whose Having flag
// equals wantHaving.
func (r Registry) requireKind(conds []Condition, wantHaving bool) error {
	for _, c := range conds {
		if err := r.checkKind(c, wantHaving); err != nil {
			return err
		}
	}
	return nil
}

func (r Registry) checkKind(c Condition, wantHaving bool) error {
	if c.Key != "" {
		f, ok := r[c.Key]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownField, c.Key)
		}
		if f.Having != wantHaving {
			clause, misplaced := "WHERE", "HAVING"
			if wantHaving {
				clause, misplaced = "HAVING", "WHERE"
			}
			return fmt.Errorf("%w: %s field %q used in %s clause", ErrInvalidCondition, misplaced, c.Key, clause)
		}
	}
	for _, sub := range c.And {
		if err := r.checkKind(sub, wantHaving); err != nil {
			return err
		}
	}
	for _, sub := range c.Or {
		if err := r.checkKind(sub, wantHaving); err != nil {
			return err
		}
	}
	if c.Not != nil {
		if err := r.checkKind(*c.Not, wantHaving); err != nil {
			return err
		}
	}
	return nil
}

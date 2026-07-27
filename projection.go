package filtersql

import "fmt"

// Projection is how a single field is selected — the SELECT-side counterpart to
// the WHERE that Compile builds. It drives the "available values" flow of a
// filter-builder UI (SELECT DISTINCT <Expr> ...).
type Projection struct {
	Expr  string          // SELECT expression: ValueExpr, else Column, else key
	Joins map[string]bool // join keys the expression depends on
}

// Project returns the projection for a field: its SELECT expression and the
// join keys that expression needs.
func (r Registry) Project(key string) (Projection, error) {
	f, ok := r[key]
	if !ok {
		return Projection{}, fmt.Errorf("%w: %q", ErrUnknownField, key)
	}
	expr := f.selectExpr(key)
	joins := make(map[string]bool, len(f.Joins))
	for _, k := range f.Joins {
		joins[k] = true
	}
	return Projection{Expr: expr, Joins: joins}, nil
}

// ValuesQuery bundles everything a "distinct values for field X" endpoint needs:
// the projected expression to SELECT, the WHERE from all OTHER active filters
// (field X excluded so the facet doesn't filter itself), the ordered JOIN SQL
// covering both the projection and those filters, and the WHERE's bind args.
//
// A typical caller drops these into a base template:
//
//	SELECT DISTINCT {{.Expr}} AS value
//	FROM base_table
//	{{.JoinSQL}}
//	WHERE tenant scoping {{if .Where}}AND {{.Where}}{{end}}
type ValuesQuery struct {
	Expr    string
	Where   string
	JoinSQL string
	Args    []any
}

// ValuesQuery assembles the pieces above for field, applying conds with field
// self-excluded and merging the projection's joins with the joins the surviving
// filters need — all deduped and dependency-ordered.
func (r Registry) ValuesQuery(d Dialect, joins Joins, field string, conds []Condition) (ValuesQuery, error) {
	proj, err := r.Project(field)
	if err != nil {
		return ValuesQuery{}, err
	}

	where, args, whereJoins, err := r.compile(d, conds, field)
	if err != nil {
		return ValuesQuery{}, err
	}

	// Union the projection's joins with the ones the filters pulled in.
	needed := make(map[string]bool, len(proj.Joins)+len(whereJoins))
	for k := range proj.Joins {
		needed[k] = true
	}
	for k := range whereJoins {
		needed[k] = true
	}

	joinSQL, err := orderJoins(joins, needed)
	if err != nil {
		return ValuesQuery{}, err
	}

	return ValuesQuery{Expr: proj.Expr, Where: where, JoinSQL: joinSQL, Args: args}, nil
}

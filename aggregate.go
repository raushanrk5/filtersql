package filtersql

import "fmt"

// AggFunc is an aggregation function.
type AggFunc string

const (
	Count         AggFunc = "count"          // count(*) / count()
	CountDistinct AggFunc = "count_distinct" // count(DISTINCT expr)
	Sum           AggFunc = "sum"
	Avg           AggFunc = "avg"
	Min           AggFunc = "min"
	Max           AggFunc = "max"
)

func (f AggFunc) valid() bool {
	switch f {
	case Count, CountDistinct, Sum, Avg, Min, Max:
		return true
	}
	return false
}

// needsMetric reports whether the function requires a field to aggregate over.
// Count is the exception — bare Count means "count all rows".
func (f AggFunc) needsMetric() bool {
	switch f {
	case CountDistinct, Sum, Avg, Min, Max:
		return true
	}
	return false
}

// aggCall renders the SQL for an aggregate. countAll is the dialect's "count all
// rows" form used when fn is Count with no expr.
func aggCall(fn AggFunc, expr, countAll string) string {
	switch fn {
	case Count:
		if expr == "" {
			return countAll
		}
		return "count(" + expr + ")"
	case CountDistinct:
		return "count(DISTINCT " + expr + ")"
	default: // Sum, Avg, Min, Max — the const value is the SQL function name
		return string(fn) + "(" + expr + ")"
	}
}

// Aggregation describes a GROUP BY aggregation over the filtered rows.
type Aggregation struct {
	GroupBy string  // field key to group by; empty = a single scalar aggregate (no GROUP BY)
	Func    AggFunc // required
	Metric  string  // field key to aggregate over; required for sum/avg/min/max/count_distinct, ignored for bare Count
	Exclude string  // field key whose own filter is dropped from WHERE; set to GroupBy for faceted counts
}

// AggQuery is the assembled aggregation: the pieces a caller drops into a base
// template. GroupExpr is empty when Aggregation.GroupBy is empty.
//
//	SELECT {{if .GroupExpr}}{{.GroupExpr}} AS bucket, {{end}}{{.AggExpr}} AS value
//	FROM base a
//	{{.JoinSQL}}
//	WHERE tenant_id = ? {{if .Where}}AND {{.Where}}{{end}}
//	{{if .GroupExpr}}GROUP BY {{.GroupExpr}}{{end}}
type AggQuery struct {
	GroupExpr string
	AggExpr   string
	Where     string
	JoinSQL   string
	Args      []any
}

// AggregateQuery assembles a GROUP BY aggregation over the rows matching conds.
// It projects the group and metric expressions (pulling in their joins), applies
// the filters (dropping agg.Exclude's own filter if set), and unions every join
// the query touches into one dependency-ordered fragment.
func (r Registry) AggregateQuery(d Dialect, joins Joins, agg Aggregation, conds []Condition) (AggQuery, error) {
	if !agg.Func.valid() {
		return AggQuery{}, fmt.Errorf("invalid aggregation function: %q", agg.Func)
	}

	needed := map[string]bool{}

	var groupExpr string
	if agg.GroupBy != "" {
		p, err := r.Project(agg.GroupBy)
		if err != nil {
			return AggQuery{}, fmt.Errorf("group by: %w", err)
		}
		groupExpr = p.Expr
		for k := range p.Joins {
			needed[k] = true
		}
	}

	if agg.Func.needsMetric() && agg.Metric == "" {
		return AggQuery{}, fmt.Errorf("aggregation %q requires a metric field", agg.Func)
	}

	var metricExpr string
	if agg.Metric != "" {
		mf, ok := r[agg.Metric]
		if !ok {
			return AggQuery{}, fmt.Errorf("%w: %q", ErrUnknownField, agg.Metric)
		}
		if (agg.Func == Sum || agg.Func == Avg) && mf.Type != TypeInt && mf.Type != TypeFloat {
			return AggQuery{}, fmt.Errorf("aggregation %q needs a numeric metric, got %s field %q", agg.Func, mf.Type, agg.Metric)
		}
		p, err := r.Project(agg.Metric)
		if err != nil {
			return AggQuery{}, err
		}
		metricExpr = p.Expr
		for k := range p.Joins {
			needed[k] = true
		}
	}

	aggExpr := d.Aggregate(agg.Func, metricExpr)

	where, args, whereJoins, err := r.compile(d, conds, agg.Exclude)
	if err != nil {
		return AggQuery{}, err
	}
	for k := range whereJoins {
		needed[k] = true
	}

	joinSQL, err := orderJoins(joins, needed)
	if err != nil {
		return AggQuery{}, err
	}

	return AggQuery{GroupExpr: groupExpr, AggExpr: aggExpr, Where: where, JoinSQL: joinSQL, Args: args}, nil
}

// FacetCounts is the common case: count rows per value of field, with field's
// own filter excluded — the classic "Critical (42)" filter-sidebar query.
// Bucket by field, count all rows, self-exclude.
func (r Registry) FacetCounts(d Dialect, joins Joins, field string, conds []Condition) (AggQuery, error) {
	return r.AggregateQuery(d, joins, Aggregation{GroupBy: field, Func: Count, Exclude: field}, conds)
}

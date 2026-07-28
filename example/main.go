// Command example is a runnable tour of filtersql: one registry driving a
// WHERE builder, nested boolean trees, dependency-ordered JOINs, a faceted
// values query, and schema introspection — across two SQL dialects.
//
//	go run ./example
package main

import (
	"encoding/json"
	"fmt"

	"github.com/raushanrk5/filtersql"
	"github.com/raushanrk5/filtersql/dialects"
)

// registry is the single source of truth: one entry per filterable field.
var registry = filtersql.Registry{
	"id":           {Type: filtersql.TypeID, Column: "a.id", Sortable: true},
	"name":         {Type: filtersql.TypeString, Column: "a.name", Sortable: true},
	"owner":        {Type: filtersql.TypeString, Column: "a.owner", Nullable: true},
	"status":       {Type: filtersql.TypeEnum, Column: "a.status", Enum: []string{"ACTIVE", "ARCHIVED"}},
	"severity":     {Type: filtersql.TypeInt, Column: "f.severity", Sortable: true, Joins: []string{"finding"}},
	"is_exploited": {Type: filtersql.TypeBool, Column: "if(f.exploited,'Yes','No')", ValueExpr: "if(f.exploited,'Yes','No')", Raw: true, Joins: []string{"finding"}},
	"tags":         {Type: filtersql.TypeArray, Column: "a.tags"},
	"labels":       {Type: filtersql.TypeMap, Column: "a.labels"},
	"policy":       {Type: filtersql.TypeString, Column: "p.name", ValueExpr: "p.display_name", Joins: []string{"policy"}},
	// Having field: an aggregate expression filtered after GROUP BY.
	"finding_count": {Type: filtersql.TypeInt, Column: "count()", Having: true, Joins: []string{"finding"}},
}

// joins declares each JOIN fragment and what it depends on. policy needs
// finding; the engine orders them and pulls finding in transitively.
var joins = filtersql.Joins{
	"finding": {SQL: "INNER JOIN finding f ON f.asset_id = a.id"},
	"policy":  {SQL: "INNER JOIN policy p ON p.finding_id = f.id", Requires: []string{"finding"}},
}

func main() {
	// Fail fast at boot: join keys resolve, no cycles, no Sortable+Having mixups.
	if err := registry.Validate(joins); err != nil {
		panic(err)
	}

	section("1. Schema introspection — what a /filters endpoint returns")
	schema, _ := json.MarshalIndent(registry.Schema(), "", "  ")
	fmt.Printf("%s\n", schema)

	section("2. A flat WHERE — same filters, two dialects")
	filters := []filtersql.Condition{
		{Key: "status", Op: filtersql.OpEq, Values: []any{"ACTIVE"}},
		{Key: "tags", Op: filtersql.OpContainsAny, Values: []any{"prod", "critical"}},
	}
	chSQL, chArgs, _ := registry.Compile(dialects.ClickHouse{}, filters)
	show("ClickHouse", chSQL, chArgs)
	pgSQL, pgArgs, _ := registry.Compile(dialects.Postgres{}, filters)
	show("Postgres  ", pgSQL, pgArgs)

	section("3. A nested AND / OR / NOT tree, decoded from UI JSON")
	raw := `[{"or":[
	    {"and":[
	        {"key":"status","op":"_eq","values":["ACTIVE"]},
	        {"key":"severity","op":"_gte","values":[7]}
	    ]},
	    {"not":{"key":"tags","op":"_contains_any","values":["ignored"]}}
	]}]`
	var tree []filtersql.Condition
	_ = json.Unmarshal([]byte(raw), &tree)
	treeSQL, treeArgs, _ := registry.Compile(dialects.ClickHouse{}, tree)
	show("ClickHouse", treeSQL, treeArgs)

	section("4. Dependency-ordered JOINs — filter on policy pulls finding in first")
	where, joinSQL, args, _ := registry.CompileWithJoins(dialects.ClickHouse{}, joins,
		[]filtersql.Condition{{Key: "policy", Op: filtersql.OpEq, Values: []any{"waf-block"}}})
	fmt.Printf("WHERE: %s\nJOINS:\n%s\nARGS:  %v\n", where, joinSQL, args)

	section("5. Faceted values query — 'distinct values for severity'")
	// Active filters: status=ACTIVE and severity>=7. The severity facet must
	// reflect status but NOT its own severity filter.
	active := []filtersql.Condition{
		{Key: "status", Op: filtersql.OpEq, Values: []any{"ACTIVE"}},
		{Key: "severity", Op: filtersql.OpGte, Values: []any{7}},
	}
	vq, _ := registry.ValuesQuery(dialects.ClickHouse{}, joins, "severity", active)
	fmt.Printf("SELECT DISTINCT %s AS value\n", vq.Expr)
	fmt.Printf("FROM asset a\n%s\n", vq.JoinSQL)
	fmt.Printf("WHERE tenant_id = ? AND %s\nARGS: %v\n", vq.Where, vq.Args)
	fmt.Println("  ^ severity's own >= filter is excluded; the finding JOIN is still emitted.")

	section("6a. Facet counts — 'Critical (42)' sidebar: rows per status value")
	fc, _ := registry.FacetCounts(dialects.ClickHouse{}, joins, "status", active)
	fmt.Printf("SELECT %s AS bucket, %s AS n\n", fc.GroupExpr, fc.AggExpr)
	fmt.Printf("FROM asset a\n%sWHERE tenant_id = ? %s\nGROUP BY %s\nARGS: %v\n",
		joinOrBlank(fc.JoinSQL), andWhere(fc.Where), fc.GroupExpr, fc.Args)
	fmt.Println("  ^ status's own filter excluded (a facet doesn't filter itself).")

	section("6b. Metric aggregation — avg(severity) grouped by status, ALL filters apply")
	agg, _ := registry.AggregateQuery(dialects.ClickHouse{}, joins,
		filtersql.Aggregation{GroupBy: "status", Func: filtersql.Avg, Metric: "severity"}, active)
	fmt.Printf("SELECT %s AS bucket, %s AS value\n", agg.GroupExpr, agg.AggExpr)
	fmt.Printf("FROM asset a\n%sWHERE tenant_id = ? %s\nGROUP BY %s\nARGS: %v\n",
		joinOrBlank(agg.JoinSQL), andWhere(agg.Where), agg.GroupExpr, agg.Args)
	fmt.Println("  ^ Exclude unset, so status = ACTIVE still applies here.")

	section("7. Read path — WHERE + ORDER BY + keyset pagination, one endpoint")
	// A list request: filter, sort by severity desc then id (unique tie-breaker).
	sorts := []filtersql.Sort{{Key: "severity", Desc: true}, {Key: "id"}}
	listWhere, listArgs, _ := registry.Compile(dialects.ClickHouse{}, []filtersql.Condition{
		{Key: "status", Op: filtersql.OpEq, Values: []any{"ACTIVE"}},
		{Key: "owner", Op: filtersql.OpIsNotNull}, // NULL operator, no value
	})
	order, _ := registry.OrderBy(dialects.ClickHouse{}, sorts)
	limit, _ := filtersql.LimitOffset(50, 0)
	fmt.Printf("Page 1:\n  WHERE %s\n  ORDER BY %s\n  %s\n  ARGS %v\n", listWhere, order, limit, listArgs)

	// Next page: caller encodes the last row's sort values into a cursor.
	token, _ := filtersql.EncodeCursor(filtersql.Cursor{"severity": 7, "id": "asset-123"})
	cur, _ := filtersql.DecodeCursor(token)
	seek, seekArgs, _ := registry.KeysetWhere(dialects.ClickHouse{}, sorts, cur)
	fmt.Printf("Next page (cursor=%s):\n  WHERE %s AND %s\n  ORDER BY %s\n  %s\n  ARGS %v + %v\n",
		token, listWhere, seek, order, limit, listArgs, seekArgs)
	fmt.Println("  ^ seek predicate is built from the SAME sort spec, so order & cursor can't disagree.")

	section("8. HAVING — filter on aggregates, placeholders continuous with WHERE")
	w, h, hargs, _ := registry.CompileWhereHaving(dialects.Postgres{},
		[]filtersql.Condition{{Key: "status", Op: filtersql.OpEq, Values: []any{"ACTIVE"}}},
		[]filtersql.Condition{{Key: "finding_count", Op: filtersql.OpGt, Values: []any{5}}})
	fmt.Printf("SELECT a.status, count() FROM asset a %s\n", joins["finding"].SQL)
	fmt.Printf("WHERE %s\nGROUP BY a.status\nHAVING %s\nARGS %v\n", w, h, hargs)
	fmt.Println("  ^ count() (a Raw Having field) isn't quoted; $2 continues after the WHERE's $1.")
}

func joinOrBlank(s string) string {
	if s == "" {
		return ""
	}
	return s + "\n"
}

func andWhere(s string) string {
	if s == "" {
		return ""
	}
	return "AND " + s
}

func section(title string) { fmt.Printf("\n\033[1m%s\033[0m\n%s\n", title, dashes(len(title))) }

func show(label, sql string, args []any) {
	fmt.Printf("%s  WHERE %s\n%s        ARGS  %v\n", label, sql, spaces(len(label)), args)
}

func dashes(n int) string { return repeat("─", n) }
func spaces(n int) string { return repeat(" ", n) }
func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

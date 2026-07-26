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
)

// registry is the single source of truth: one entry per filterable field.
var registry = filtersql.Registry{
	"name":         {Type: filtersql.TypeString, Column: "a.name"},
	"status":       {Type: filtersql.TypeEnum, Column: "a.status", Enum: []string{"ACTIVE", "ARCHIVED"}},
	"severity":     {Type: filtersql.TypeInt, Column: "f.severity", Joins: []string{"finding"}},
	"is_exploited": {Type: filtersql.TypeBool, Column: "if(f.exploited,'Yes','No')", ValueExpr: "if(f.exploited,'Yes','No')", Joins: []string{"finding"}},
	"tags":         {Type: filtersql.TypeArray, Column: "a.tags"},
	"labels":       {Type: filtersql.TypeMap, Column: "a.labels"},
	"policy":       {Type: filtersql.TypeString, Column: "p.name", ValueExpr: "p.display_name", Joins: []string{"policy"}},
}

// joins declares each JOIN fragment and what it depends on. policy needs
// finding; the engine orders them and pulls finding in transitively.
var joins = filtersql.Joins{
	"finding": {SQL: "INNER JOIN finding f ON f.asset_id = a.id"},
	"policy":  {SQL: "INNER JOIN policy p ON p.finding_id = f.id", Requires: []string{"finding"}},
}

func main() {
	section("1. Schema introspection — what a /filters endpoint returns")
	schema, _ := json.MarshalIndent(registry.Schema(), "", "  ")
	fmt.Printf("%s\n", schema)

	section("2. A flat WHERE — same filters, two dialects")
	filters := []filtersql.Condition{
		{Key: "status", Op: filtersql.OpEq, Values: []any{"ACTIVE"}},
		{Key: "tags", Op: filtersql.OpContainsAny, Values: []any{"prod", "critical"}},
	}
	chSQL, chArgs, _ := registry.Compile(filtersql.ClickHouse{}, filters)
	show("ClickHouse", chSQL, chArgs)
	pgSQL, pgArgs, _ := registry.Compile(filtersql.Postgres{}, filters)
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
	treeSQL, treeArgs, _ := registry.Compile(filtersql.ClickHouse{}, tree)
	show("ClickHouse", treeSQL, treeArgs)

	section("4. Dependency-ordered JOINs — filter on policy pulls finding in first")
	where, joinSQL, args, _ := registry.CompileWithJoins(filtersql.ClickHouse{}, joins,
		[]filtersql.Condition{{Key: "policy", Op: filtersql.OpEq, Values: []any{"waf-block"}}})
	fmt.Printf("WHERE: %s\nJOINS:\n%s\nARGS:  %v\n", where, joinSQL, args)

	section("5. Faceted values query — 'distinct values for severity'")
	// Active filters: status=ACTIVE and severity>=7. The severity facet must
	// reflect status but NOT its own severity filter.
	active := []filtersql.Condition{
		{Key: "status", Op: filtersql.OpEq, Values: []any{"ACTIVE"}},
		{Key: "severity", Op: filtersql.OpGte, Values: []any{7}},
	}
	vq, _ := registry.ValuesQuery(filtersql.ClickHouse{}, joins, "severity", active)
	fmt.Printf("SELECT DISTINCT %s AS value\n", vq.Expr)
	fmt.Printf("FROM asset a\n%s\n", vq.JoinSQL)
	fmt.Printf("WHERE tenant_id = ? AND %s\nARGS: %v\n", vq.Where, vq.Args)
	fmt.Println("  ^ severity's own >= filter is excluded; the finding JOIN is still emitted.")
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

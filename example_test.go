package filtersql_test

import (
	"encoding/json"
	"fmt"

	"github.com/raushanrk5/filtersql"
)

func ExampleRegistry_Compile() {
	reg := filtersql.Registry{
		"status": {Type: filtersql.TypeEnum, Column: "a.status", Enum: []string{"ACTIVE", "ARCHIVED"}},
		"tags":   {Type: filtersql.TypeArray, Column: "a.tags"},
	}
	filters := []filtersql.Condition{
		{Key: "status", Op: filtersql.OpEq, Values: []any{"ACTIVE"}},
		{Key: "tags", Op: filtersql.OpContainsAny, Values: []any{"prod", "crit"}},
	}

	where, args, _ := reg.Compile(filtersql.Postgres{}, filters)
	fmt.Println(where)
	fmt.Println(args)
	// Output:
	// "a"."status" = $1 AND "a"."tags" && $2::text[]
	// [ACTIVE {"prod","crit"}]
}

func ExampleRegistry_Schema() {
	reg := filtersql.Registry{
		"status": {Type: filtersql.TypeEnum, Column: "a.status", Enum: []string{"ACTIVE", "ARCHIVED"}},
	}
	b, _ := json.Marshal(reg.Schema())
	fmt.Println(string(b))
	// Output:
	// [{"key":"status","type":"enum","operators":["_eq","_ne","_in","_nin"],"enum":["ACTIVE","ARCHIVED"]}]
}

func ExampleMustFromStruct() {
	type Asset struct {
		Status   string `filter:"status,enum=ACTIVE|ARCHIVED"`
		Severity int    `filter:"severity,col=f.severity"`
	}
	reg := filtersql.MustFromStruct(Asset{})

	where, args, _ := reg.Compile(filtersql.ClickHouse{}, []filtersql.Condition{
		{Key: "severity", Op: filtersql.OpGte, Values: []any{7}},
	})
	fmt.Println(where, args)
	// Output: f.severity >= ? [7]
}

func ExampleRegistry_Builder() {
	reg := filtersql.Registry{
		"status": {Type: filtersql.TypeEnum, Column: "status"},
	}
	b := reg.Builder(filtersql.Postgres{})
	tenant := b.Arg("t1") // caller's tenant scoping shares the numbering
	where, _ := b.Where([]filtersql.Condition{{Key: "status", Op: filtersql.OpEq, Values: []any{"ACTIVE"}}})

	fmt.Printf("tenant_id = %s AND %s\n", tenant, where)
	fmt.Println(b.Args())
	// Output:
	// tenant_id = $1 AND "status" = $2
	// [t1 ACTIVE]
}

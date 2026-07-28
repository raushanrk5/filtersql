package filtersql_test

import (
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"testing"
)

var benchReg = Registry{
	"status":   {Type: TypeEnum, Column: "a.status", Enum: []string{"ACTIVE", "ARCHIVED"}},
	"severity": {Type: TypeInt, Column: "f.severity", Sortable: true, Joins: []string{"finding"}},
	"name":     {Type: TypeString, Column: "a.name"},
	"tags":     {Type: TypeArray, Column: "a.tags"},
	"owner":    {Type: TypeString, Column: "a.owner", Nullable: true},
}

var benchJoins = Joins{
	"finding": {SQL: "INNER JOIN finding f ON f.asset_id = a.id"},
}

func BenchmarkCompileSimple(b *testing.B) {
	filters := []Condition{
		{Key: "status", Op: OpEq, Values: []any{"ACTIVE"}},
		{Key: "severity", Op: OpGte, Values: []any{7}},
		{Key: "name", Op: OpLike, Values: []any{"web"}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := benchReg.Compile(Postgres{}, filters); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileNested(b *testing.B) {
	filters := []Condition{{
		Or: []Condition{
			{And: []Condition{
				{Key: "status", Op: OpEq, Values: []any{"ACTIVE"}},
				{Key: "severity", Op: OpGte, Values: []any{7}},
			}},
			{Not: &Condition{Key: "tags", Op: OpContainsAny, Values: []any{"ignored"}}},
		},
	}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := benchReg.Compile(ClickHouse{}, filters); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileWithJoins(b *testing.B) {
	filters := []Condition{
		{Key: "status", Op: OpEq, Values: []any{"ACTIVE"}},
		{Key: "severity", Op: OpGte, Values: []any{7}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, _, err := benchReg.CompileWithJoins(Postgres{}, benchJoins, filters); err != nil {
			b.Fatal(err)
		}
	}
}

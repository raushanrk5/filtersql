package filtersql_test

import (
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"reflect"
	"testing"
)

// aggReg reuses joins so we can prove aggregation unions projection + filter joins.
var aggReg = Registry{
	"status":   {Type: TypeEnum, Column: "a.status", Enum: []string{"ACTIVE", "ARCHIVED"}},
	"severity": {Type: TypeInt, Column: "f.severity", Joins: []string{"finding"}},
	"score":    {Type: TypeFloat, Column: "f.score", Joins: []string{"finding"}},
	"policy":   {Type: TypeString, Column: "p.name", ValueExpr: "p.display_name", Joins: []string{"policy"}},
}

var aggJoins = Joins{
	"finding": {SQL: "INNER JOIN finding f ON f.asset_id = a.id"},
	"policy":  {SQL: "INNER JOIN policy p ON p.finding_id = f.id", Requires: []string{"finding"}},
}

func TestFacetCounts_SelfExcludesAndCountsAll(t *testing.T) {
	// Counts of assets per status, reflecting the severity filter but NOT status's own.
	conds := []Condition{
		{Key: "status", Op: OpEq, Values: []any{"ACTIVE"}},
		{Key: "severity", Op: OpGte, Values: []any{7}},
	}
	aq, err := aggReg.FacetCounts(ClickHouse{}, aggJoins, "status", conds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aq.GroupExpr != "a.status" {
		t.Errorf("groupExpr = %q", aq.GroupExpr)
	}
	if aq.AggExpr != "count()" {
		t.Errorf("aggExpr = %q, want count()", aq.AggExpr)
	}
	// status's own filter is excluded; only severity remains.
	if aq.Where != "f.severity >= ?" {
		t.Errorf("where = %q", aq.Where)
	}
	// severity pulls in the finding join even though we group by status.
	if aq.JoinSQL != "INNER JOIN finding f ON f.asset_id = a.id" {
		t.Errorf("joinSQL = %q", aq.JoinSQL)
	}
	if !reflect.DeepEqual(aq.Args, []any{float64(7)}) {
		t.Errorf("args = %#v", aq.Args)
	}
}

func TestFacetCounts_PostgresCountStar(t *testing.T) {
	aq, err := aggReg.FacetCounts(Postgres{}, aggJoins, "status", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aq.AggExpr != "count(*)" {
		t.Errorf("aggExpr = %q, want count(*)", aq.AggExpr)
	}
	if aq.Where != "" {
		t.Errorf("where = %q, want empty", aq.Where)
	}
}

func TestAggregateQuery_MetricGroupedAllFiltersApply(t *testing.T) {
	// avg(score) grouped by policy, with ALL filters applied (Exclude unset).
	// Projection needs policy(+finding); the filter on severity needs finding too.
	conds := []Condition{{Key: "severity", Op: OpGte, Values: []any{5}}}
	aq, err := aggReg.AggregateQuery(ClickHouse{}, aggJoins,
		Aggregation{GroupBy: "policy", Func: Avg, Metric: "score"}, conds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aq.GroupExpr != "p.display_name" {
		t.Errorf("groupExpr = %q", aq.GroupExpr)
	}
	if aq.AggExpr != "avg(f.score)" {
		t.Errorf("aggExpr = %q", aq.AggExpr)
	}
	if aq.Where != "f.severity >= ?" {
		t.Errorf("where = %q", aq.Where)
	}
	// finding (required by policy) must come before policy; both appear once.
	wantJoin := "INNER JOIN finding f ON f.asset_id = a.id\nINNER JOIN policy p ON p.finding_id = f.id"
	if aq.JoinSQL != wantJoin {
		t.Errorf("joinSQL:\n got %q\nwant %q", aq.JoinSQL, wantJoin)
	}
}

func TestAggregateQuery_ScalarNoGroup(t *testing.T) {
	// count(DISTINCT policy) with no grouping and no filters.
	aq, err := aggReg.AggregateQuery(Postgres{}, aggJoins,
		Aggregation{Func: CountDistinct, Metric: "policy"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aq.GroupExpr != "" {
		t.Errorf("groupExpr = %q, want empty", aq.GroupExpr)
	}
	if aq.AggExpr != "count(DISTINCT p.display_name)" {
		t.Errorf("aggExpr = %q", aq.AggExpr)
	}
	// projecting policy still requires its joins.
	if aq.JoinSQL == "" {
		t.Errorf("expected policy joins, got empty")
	}
}

func TestAggregateQuery_Errors(t *testing.T) {
	tests := []struct {
		name string
		agg  Aggregation
	}{
		{"invalid func", Aggregation{Func: "median", Metric: "score"}},
		{"sum without metric", Aggregation{Func: Sum}},
		{"sum on non-numeric", Aggregation{Func: Sum, Metric: "status"}},
		{"unknown group field", Aggregation{GroupBy: "ghost", Func: Count}},
		{"unknown metric field", Aggregation{Func: Avg, Metric: "ghost"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := aggReg.AggregateQuery(ClickHouse{}, aggJoins, tt.agg, nil); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

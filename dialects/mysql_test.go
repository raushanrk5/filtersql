package dialects_test

import (
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"reflect"
	"testing"
)

func TestMySQLDialect(t *testing.T) {
	reg := Registry{
		"name":     {Type: TypeString, Column: "a.name"},
		"tags":     {Type: TypeArray, Column: "tags"},
		"labels":   {Type: TypeMap, Column: "labels"},
		"created":  {Type: TypeTimestamp, Column: "created"},
		"severity": {Type: TypeInt, Column: "severity", Sortable: true},
	}
	cases := []struct {
		name    string
		in      Condition
		wantSQL string
		wantArg []any
	}{
		{
			name:    "like lowercases both sides",
			in:      Condition{Key: "name", Op: OpLike, Values: []any{"Web"}},
			wantSQL: "LOWER(`a`.`name`) LIKE ?",
			wantArg: []any{"%web%"},
		},
		{
			name:    "array contains-all via JSON_CONTAINS",
			in:      Condition{Key: "tags", Op: OpContains, Values: []any{"prod", "crit"}},
			wantSQL: "JSON_CONTAINS(`tags`, ?)",
			wantArg: []any{`["prod","crit"]`},
		},
		{
			name:    "array contains-any via JSON_OVERLAPS",
			in:      Condition{Key: "tags", Op: OpContainsAny, Values: []any{"a", "b"}},
			wantSQL: "JSON_OVERLAPS(`tags`, ?)",
			wantArg: []any{`["a","b"]`},
		},
		{
			name:    "map has key/value via JSON_EXTRACT",
			in:      Condition{Key: "labels", Op: OpHasKeyValues, Pairs: []KeyValue{{Key: "env", Values: []string{"prod"}}}},
			wantSQL: "JSON_UNQUOTE(JSON_EXTRACT(`labels`, ?)) = ?",
			wantArg: []any{`$."env"`, "prod"},
		},
		{
			name:    "relative time uses INTERVAL",
			in:      Condition{Key: "created", Op: OpLast, Values: []any{"7d"}},
			wantSQL: "`created` BETWEEN NOW() - INTERVAL 7 DAY AND NOW()",
			wantArg: nil,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			sql, args, err := reg.Compile(MySQL{}, []Condition{tt.in})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sql != tt.wantSQL {
				t.Errorf("sql:\n got %q\nwant %q", sql, tt.wantSQL)
			}
			if len(args) != 0 || len(tt.wantArg) != 0 {
				if !reflect.DeepEqual(args, tt.wantArg) {
					t.Errorf("args:\n got %#v\nwant %#v", args, tt.wantArg)
				}
			}
		})
	}
}

func TestMySQLNullsEmulation(t *testing.T) {
	reg := Registry{"severity": {Type: TypeInt, Column: "severity", Sortable: true}}
	got, err := reg.OrderBy(MySQL{}, []Sort{{Key: "severity", Desc: true, Nulls: NullsLast}})
	if err != nil {
		t.Fatal(err)
	}
	// No NULLS LAST syntax -> leading "IS NULL" key.
	if got != "severity IS NULL, severity DESC" {
		t.Errorf("order = %q", got)
	}
}

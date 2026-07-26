package filtersql

import (
	"reflect"
	"testing"
)

var reg = Registry{
	"name":     {Type: TypeString, Column: "a.name"},
	"status":   {Type: TypeEnum, Column: "a.status", Enum: []string{"OPEN", "CLOSED"}},
	"severity": {Type: TypeInt, Column: "f.severity"},
	"active":   {Type: TypeBool, Column: "a.active"},
	"seen_at":  {Type: TypeTimestamp, Column: "a.seen_at"},
	"tags":     {Type: TypeArray, Column: "a.tags"},
	"labels":   {Type: TypeMap, Column: "a.labels"},
	"search":   {Type: TypeString, Column: "a.name", Hidden: true},
}

func TestCompile(t *testing.T) {
	tests := []struct {
		name    string
		d       Dialect
		in      []Condition
		wantSQL string
		wantArg []any
	}{
		{
			name:    "string eq (clickhouse)",
			d:       ClickHouse{},
			in:      []Condition{{Key: "name", Op: OpEq, Values: []any{"web-01"}}},
			wantSQL: "a.name = ?",
			wantArg: []any{"web-01"},
		},
		{
			name:    "string in (postgres numbers placeholders)",
			d:       Postgres{},
			in:      []Condition{{Key: "name", Op: OpIn, Values: []any{"a", "b", "c"}}},
			wantSQL: `"a"."name" IN ($1, $2, $3)`,
			wantArg: []any{"a", "b", "c"},
		},
		{
			name:    "like wraps and escapes (clickhouse)",
			d:       ClickHouse{},
			in:      []Condition{{Key: "name", Op: OpLike, Values: []any{"50%_off"}}},
			wantSQL: "a.name ILIKE ?",
			wantArg: []any{`%50\%\_off%`},
		},
		{
			name:    "numeric gte coerces string",
			d:       ClickHouse{},
			in:      []Condition{{Key: "severity", Op: OpGte, Values: []any{"7"}}},
			wantSQL: "f.severity >= ?",
			wantArg: []any{float64(7)},
		},
		{
			name:    "bool eq accepts Yes",
			d:       ClickHouse{},
			in:      []Condition{{Key: "active", Op: OpEq, Values: []any{"Yes"}}},
			wantSQL: "a.active = ?",
			wantArg: []any{true},
		},
		{
			name:    "array contains all (clickhouse hasAll)",
			d:       ClickHouse{},
			in:      []Condition{{Key: "tags", Op: OpContains, Values: []any{"prod", "crit"}}},
			wantSQL: "hasAll(a.tags, ?)",
			wantArg: []any{[]string{"prod", "crit"}},
		},
		{
			name:    "array not-contains-any (postgres overlap, negated)",
			d:       Postgres{},
			in:      []Condition{{Key: "tags", Op: OpNotContainsAny, Values: []any{"x", "y"}}},
			wantSQL: `NOT ("a"."tags" && $1)`,
			wantArg: []any{"{\"x\",\"y\"}"},
		},
		{
			name:    "map has key values (clickhouse)",
			d:       ClickHouse{},
			in:      []Condition{{Key: "labels", Op: OpHasKeyValues, Pairs: []KeyValue{{Key: "env", Values: []string{"prod"}}}}},
			wantSQL: "a.labels[?] = ?",
			wantArg: []any{"env", "prod"},
		},
		{
			name: "multiple conditions AND-joined",
			d:    ClickHouse{},
			in: []Condition{
				{Key: "status", Op: OpEq, Values: []any{"OPEN"}},
				{Key: "severity", Op: OpGt, Values: []any{5}},
			},
			wantSQL: "a.status = ? AND f.severity > ?",
			wantArg: []any{"OPEN", float64(5)},
		},
		{
			name:    "empty _in list is skipped, not an error",
			d:       ClickHouse{},
			in:      []Condition{{Key: "name", Op: OpIn, Values: []any{}}},
			wantSQL: "",
			wantArg: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args, err := reg.Compile(tt.d, tt.in)
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

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name string
		in   []Condition
	}{
		{"unknown field", []Condition{{Key: "nope", Op: OpEq, Values: []any{"x"}}}},
		{"both leaf and group", []Condition{{Key: "name", Op: OpEq, Values: []any{"x"}, Or: []Condition{{Key: "name", Op: OpEq, Values: []any{"y"}}}}}},
		{"illegal operator for type", []Condition{{Key: "severity", Op: OpLike, Values: []any{"x"}}}},
		{"enum value out of range", []Condition{{Key: "status", Op: OpEq, Values: []any{"BOGUS"}}}},
		{"non-numeric for int", []Condition{{Key: "severity", Op: OpEq, Values: []any{"abc"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := reg.Compile(ClickHouse{}, tt.in); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestSchemaHidesVirtualAndReportsOperators(t *testing.T) {
	schema := reg.Schema()
	for _, fs := range schema {
		if fs.Key == "search" {
			t.Fatalf("hidden field should not appear in schema")
		}
		if fs.Key == "status" {
			if !reflect.DeepEqual(fs.Operators, typeOperators[TypeEnum]) {
				t.Errorf("status operators = %v, want %v", fs.Operators, typeOperators[TypeEnum])
			}
			if !reflect.DeepEqual(fs.Enum, []string{"OPEN", "CLOSED"}) {
				t.Errorf("status enum = %v", fs.Enum)
			}
		}
	}
}

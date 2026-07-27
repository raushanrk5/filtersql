package filtersql

import (
	"reflect"
	"testing"
)

// String-level coverage for the SQLite dialect. End-to-end execution against a
// real SQLite database lives in the separate integration module.
func TestSQLiteDialect(t *testing.T) {
	reg := Registry{
		"name":   {Type: TypeString, Column: "name"},
		"tags":   {Type: TypeArray, Column: "tags"},
		"labels": {Type: TypeMap, Column: "labels"},
	}
	cases := []struct {
		name    string
		in      Condition
		wantSQL string
		wantArg []any
	}{
		{
			name:    "like uses ESCAPE clause",
			in:      Condition{Key: "name", Op: OpLike, Values: []any{"a_b"}},
			wantSQL: `"name" LIKE ? ESCAPE '\'`,
			wantArg: []any{`%a\_b%`},
		},
		{
			name:    "array contains-any via json_each",
			in:      Condition{Key: "tags", Op: OpContainsAny, Values: []any{"prod", "db"}},
			wantSQL: `EXISTS (SELECT 1 FROM json_each("tags") WHERE value IN (?, ?))`,
			wantArg: []any{"prod", "db"},
		},
		{
			name:    "array contains-all counts distinct matches",
			in:      Condition{Key: "tags", Op: OpContains, Values: []any{"prod", "crit"}},
			wantSQL: `(SELECT count(DISTINCT value) FROM json_each("tags") WHERE value IN (?, ?)) = ?`,
			wantArg: []any{"prod", "crit", 2},
		},
		{
			name:    "map has key/value via json_each",
			in:      Condition{Key: "labels", Op: OpHasKeyValues, Pairs: []KeyValue{{Key: "env", Values: []string{"prod"}}}},
			wantSQL: `EXISTS (SELECT 1 FROM json_each("labels") WHERE key = ? AND value = ?)`,
			wantArg: []any{"env", "prod"},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			sql, args, err := reg.Compile(SQLite{}, []Condition{tt.in})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sql != tt.wantSQL {
				t.Errorf("sql:\n got %q\nwant %q", sql, tt.wantSQL)
			}
			if !reflect.DeepEqual(args, tt.wantArg) {
				t.Errorf("args:\n got %#v\nwant %#v", args, tt.wantArg)
			}
		})
	}
}

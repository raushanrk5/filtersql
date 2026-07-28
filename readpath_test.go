package filtersql_test

import (
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"reflect"
	"testing"
)

var rpReg = Registry{
	"name":     {Type: TypeString, Column: "a.name", Sortable: true},
	"created":  {Type: TypeTimestamp, Column: "a.created", Sortable: true},
	"id":       {Type: TypeID, Column: "a.id", Sortable: true},
	"assignee": {Type: TypeString, Column: "a.assignee", Nullable: true},
	"score":    {Type: TypeInt, Column: "a.score"}, // not sortable
}

// --- NULL operators ---

func TestNullOperators(t *testing.T) {
	sql, args, err := rpReg.Compile(ClickHouse{}, []Condition{
		{Key: "assignee", Op: OpIsNull},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql != "a.assignee IS NULL" {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 0 {
		t.Errorf("args = %#v, want none", args)
	}

	sql, _, _ = rpReg.Compile(Postgres{}, []Condition{{Key: "assignee", Op: OpIsNotNull}})
	if sql != `"a"."assignee" IS NOT NULL` {
		t.Errorf("sql = %q", sql)
	}
}

func TestNullOperatorRejectedOnNonNullableField(t *testing.T) {
	// score is not Nullable, so _is_null must be rejected.
	if _, _, err := rpReg.Compile(ClickHouse{}, []Condition{{Key: "score", Op: OpIsNull}}); err == nil {
		t.Fatal("expected error for _is_null on non-nullable field")
	}
}

func TestSchemaAdvertisesNullOpsAndSortable(t *testing.T) {
	for _, fs := range rpReg.Schema() {
		switch fs.Key {
		case "assignee":
			if !hasOp(fs.Operators, OpIsNull) || !hasOp(fs.Operators, OpIsNotNull) {
				t.Errorf("assignee should advertise null ops, got %v", fs.Operators)
			}
		case "name":
			if !fs.Sortable {
				t.Errorf("name should be advertised sortable")
			}
			if hasOp(fs.Operators, OpIsNull) {
				t.Errorf("non-nullable field should not advertise null ops")
			}
		}
	}
}

// --- ORDER BY ---

func TestOrderBy(t *testing.T) {
	sql, err := rpReg.OrderBy(Postgres{}, []Sort{
		{Key: "created", Desc: true, Nulls: NullsLast},
		{Key: "id"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql != "a.created DESC NULLS LAST, a.id ASC" {
		t.Errorf("sql = %q", sql)
	}
}

func TestOrderByRejectsNonSortable(t *testing.T) {
	if _, err := rpReg.OrderBy(ClickHouse{}, []Sort{{Key: "score"}}); err == nil {
		t.Fatal("expected error sorting by non-sortable field")
	}
	if _, err := rpReg.OrderBy(ClickHouse{}, []Sort{{Key: "ghost"}}); err == nil {
		t.Fatal("expected error sorting by unknown field")
	}
}

func TestOrderByEmpty(t *testing.T) {
	sql, err := rpReg.OrderBy(ClickHouse{}, nil)
	if err != nil || sql != "" {
		t.Fatalf("want empty, got %q err %v", sql, err)
	}
}

// --- Pagination: offset ---

func TestLimitOffset(t *testing.T) {
	cases := []struct {
		limit, offset int
		want          string
		wantErr       bool
	}{
		{50, 0, "LIMIT 50", false},
		{50, 100, "LIMIT 50 OFFSET 100", false},
		{0, 0, "", false},
		{-1, 0, "", true},
		{10, -5, "", true},
	}
	for _, c := range cases {
		got, err := LimitOffset(c.limit, c.offset)
		if c.wantErr {
			if err == nil {
				t.Errorf("LimitOffset(%d,%d): expected error", c.limit, c.offset)
			}
			continue
		}
		if err != nil {
			t.Errorf("LimitOffset(%d,%d): %v", c.limit, c.offset, err)
		}
		if got != c.want {
			t.Errorf("LimitOffset(%d,%d) = %q, want %q", c.limit, c.offset, got, c.want)
		}
	}
}

// --- Pagination: keyset ---

func TestKeysetWhere_MixedDirections(t *testing.T) {
	sorts := []Sort{{Key: "created", Desc: true}, {Key: "id"}}
	cur := Cursor{"created": "2026-01-01T00:00:00Z", "id": "abc"}
	sql, args, err := rpReg.KeysetWhere(ClickHouse{}, sorts, cur)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// created DESC -> "<" ; id ASC -> ">"
	want := "((a.created < ?) OR (a.created = ? AND a.id > ?))"
	if sql != want {
		t.Errorf("sql:\n got %q\nwant %q", sql, want)
	}
	wantArgs := []any{"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "abc"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("args:\n got %#v\nwant %#v", args, wantArgs)
	}
}

func TestKeysetWhere_EmptyCursorFirstPage(t *testing.T) {
	sql, args, err := rpReg.KeysetWhere(ClickHouse{}, []Sort{{Key: "id"}}, nil)
	if err != nil || sql != "" || args != nil {
		t.Fatalf("first page should be empty: sql=%q args=%v err=%v", sql, args, err)
	}
}

func TestKeysetWhere_Errors(t *testing.T) {
	// non-sortable field
	if _, _, err := rpReg.KeysetWhere(ClickHouse{}, []Sort{{Key: "score"}}, Cursor{"score": 1}); err == nil {
		t.Error("expected error for non-sortable keyset field")
	}
	// cursor missing a value
	if _, _, err := rpReg.KeysetWhere(ClickHouse{}, []Sort{{Key: "id"}}, Cursor{"other": 1}); err == nil {
		t.Error("expected error for cursor missing sort key")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	orig := Cursor{"created": "2026-01-01T00:00:00Z", "id": "abc"}
	tok, err := EncodeCursor(orig)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeCursor(tok)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, orig) {
		t.Errorf("round-trip mismatch: %#v vs %#v", got, orig)
	}
	// empty token -> nil cursor, no error
	if c, err := DecodeCursor(""); err != nil || c != nil {
		t.Errorf("empty token: got %#v err %v", c, err)
	}
	// garbage -> error
	if _, err := DecodeCursor("!!!not-base64!!!"); err == nil {
		t.Error("expected error decoding garbage cursor")
	}
}

func hasOp(ops []Operator, target Operator) bool {
	for _, o := range ops {
		if o == target {
			return true
		}
	}
	return false
}

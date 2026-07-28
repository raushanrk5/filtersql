package filtersql_test

import (
	"errors"
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"reflect"
	"testing"
)

var builderReg = Registry{
	"status":   {Type: TypeEnum, Column: "status", Enum: []string{"ACTIVE", "ARCHIVED"}},
	"severity": {Type: TypeInt, Column: "severity", Sortable: true},
	"id":       {Type: TypeID, Column: "id", Sortable: true},
	"cnt":      {Type: TypeInt, Column: "count()", Having: true},
}

func TestBuilder_ContinuousPlaceholders(t *testing.T) {
	b := builderReg.Builder(Postgres{})

	tenant := b.Arg("t1") // $1
	if tenant != "$1" {
		t.Fatalf("tenant placeholder = %q, want $1", tenant)
	}

	where, err := b.Where([]Condition{{Key: "status", Op: OpEq, Values: []any{"ACTIVE"}}})
	if err != nil {
		t.Fatal(err)
	}
	if where != `"status" = $2` {
		t.Errorf("where = %q", where)
	}

	sort := []Sort{{Key: "severity", Desc: true}, {Key: "id"}}
	seek, err := b.Keyset(sort, Cursor{"severity": 5, "id": "a3"})
	if err != nil {
		t.Fatal(err)
	}
	// severity DESC -> "<"; id ASC -> ">". Placeholders continue at $3.
	if seek != "((severity < $3) OR (severity = $4 AND id > $5))" {
		t.Errorf("seek = %q", seek)
	}

	having, err := b.Having([]Condition{{Key: "cnt", Op: OpGt, Values: []any{1}}})
	if err != nil {
		t.Fatal(err)
	}
	if having != "count() > $6" {
		t.Errorf("having = %q", having)
	}

	// Args accumulate in call order — matching the placeholder numbers.
	want := []any{"t1", "ACTIVE", 5, 5, "a3", float64(1)}
	if !reflect.DeepEqual(b.Args(), want) {
		t.Errorf("args:\n got %#v\nwant %#v", b.Args(), want)
	}
}

func TestBuilder_QMarkDialectUnaffected(t *testing.T) {
	b := builderReg.Builder(ClickHouse{})
	_ = b.Arg("t1")
	where, _ := b.Where([]Condition{{Key: "status", Op: OpEq, Values: []any{"ACTIVE"}}})
	if where != "status = ?" {
		t.Errorf("where = %q", where)
	}
}

func TestBuilder_HavingRejectsNonHavingField(t *testing.T) {
	b := builderReg.Builder(Postgres{})
	_, err := b.Having([]Condition{{Key: "status", Op: OpEq, Values: []any{"ACTIVE"}}})
	if !errors.Is(err, ErrInvalidCondition) {
		t.Errorf("want ErrInvalidCondition, got %v", err)
	}
}

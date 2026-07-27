package filtersql

import (
	"errors"
	"reflect"
	"testing"
)

func TestSearchCols_ExpandsToOR(t *testing.T) {
	reg := Registry{
		"q":   {Type: TypeString, SearchCols: []string{"a.name", "a.email"}, Hidden: true},
		"one": {Type: TypeString, SearchCols: []string{"a.name"}},
	}

	// Multi-column -> parenthesized OR, one bind per column.
	sql, args, err := reg.Compile(Postgres{}, []Condition{{Key: "q", Op: OpLike, Values: []any{"web"}}})
	if err != nil {
		t.Fatal(err)
	}
	if sql != `("a"."name" ILIKE $1 OR "a"."email" ILIKE $2)` {
		t.Errorf("sql = %q", sql)
	}
	if !reflect.DeepEqual(args, []any{"%web%", "%web%"}) {
		t.Errorf("args = %#v", args)
	}

	// ClickHouse, single column -> no parens.
	ch, _, _ := reg.Compile(ClickHouse{}, []Condition{{Key: "one", Op: OpStartsWith, Values: []any{"x"}}})
	if ch != "a.name ILIKE ?" {
		t.Errorf("single-col sql = %q", ch)
	}

	// Hidden search field stays out of the schema.
	for _, fs := range reg.Schema() {
		if fs.Key == "q" {
			t.Error("hidden search field should not be in schema")
		}
	}
}

func TestTransform_NormalizesValues(t *testing.T) {
	reg := Registry{"email": {Type: TypeString, Column: "a.email", Transform: Lower()}}

	_, args, err := reg.Compile(ClickHouse{}, []Condition{{Key: "email", Op: OpEq, Values: []any{"Foo@BAR.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, []any{"foo@bar.com"}) {
		t.Errorf("args = %#v, want [foo@bar.com]", args)
	}

	// Applies to every value in an _in list too.
	_, inArgs, _ := reg.Compile(ClickHouse{}, []Condition{{Key: "email", Op: OpIn, Values: []any{"A", "B"}}})
	if !reflect.DeepEqual(inArgs, []any{"a", "b"}) {
		t.Errorf("in args = %#v", inArgs)
	}
}

func TestValidate_RejectsBadValues(t *testing.T) {
	reg := Registry{"name": {Type: TypeString, Column: "a.name", Validate: MaxLen(3)}}

	// too long -> ErrBadValue
	if _, _, err := reg.Compile(ClickHouse{}, []Condition{{Key: "name", Op: OpEq, Values: []any{"toolong"}}}); !errors.Is(err, ErrBadValue) {
		t.Errorf("want ErrBadValue, got %v", err)
	}
	// within limit -> ok
	if _, _, err := reg.Compile(ClickHouse{}, []Condition{{Key: "name", Op: OpEq, Values: []any{"ok"}}}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSearchCols_FromStructTag(t *testing.T) {
	type search struct {
		Q string `filter:"q,search=a.name|a.email,hidden"`
	}
	reg := MustFromStruct(search{})
	f := reg["q"]
	if !reflect.DeepEqual(f.SearchCols, []string{"a.name", "a.email"}) {
		t.Errorf("SearchCols = %v", f.SearchCols)
	}
	if !f.Hidden {
		t.Error("expected hidden")
	}
}

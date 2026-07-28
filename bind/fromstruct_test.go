package bind

import (
	. "github.com/raushanrk5/filtersql"
	. "github.com/raushanrk5/filtersql/dialects"
	"reflect"
	"testing"
	"time"
)

type assetFilter struct {
	ID       string            `filter:"id,sortable"`
	Name     string            `filter:"name,col=a.name,sortable"`
	Status   string            `filter:"status,enum=ACTIVE|ARCHIVED"`
	Severity int               `filter:"severity,type=int,col=f.severity,joins=finding"`
	Score    float64           `filter:"score"`
	Active   bool              `filter:"active"`
	Owner    *string           `filter:"owner"` // pointer -> nullable
	SeenAt   time.Time         `filter:"seen_at"`
	Tags     []string          `filter:"tags"`
	Labels   map[string]string `filter:"labels"`
	Count    int               `filter:"finding_count,col=count(),having"`
	Internal string            // no tag -> skipped
	Secret   string            `filter:"-"`         // explicit skip
	AutoKey  string            `filter:",sortable"` // key from field name
}

func TestFromStruct(t *testing.T) {
	reg, err := FromStruct(assetFilter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Untagged / skipped fields are absent.
	if _, ok := reg["Internal"]; ok {
		t.Error("untagged field should be skipped")
	}
	if _, ok := reg["secret"]; ok {
		t.Error("filter:\"-\" field should be skipped")
	}
	if len(reg) != 12 {
		t.Errorf("expected 12 fields, got %d: %v", len(reg), keysOf(reg))
	}

	checks := map[string]Field{
		"id":            {Type: TypeID, Sortable: true, Column: ""}, // inferred string->... see note
		"name":          {Type: TypeString, Column: "a.name", Sortable: true},
		"status":        {Type: TypeEnum, Enum: []string{"ACTIVE", "ARCHIVED"}},
		"severity":      {Type: TypeInt, Column: "f.severity", Joins: []string{"finding"}},
		"score":         {Type: TypeFloat},
		"active":        {Type: TypeBool},
		"owner":         {Type: TypeString, Nullable: true},
		"seen_at":       {Type: TypeTimestamp},
		"tags":          {Type: TypeArray},
		"labels":        {Type: TypeMap},
		"finding_count": {Type: TypeInt, Column: "count()", Having: true},
		"auto_key":      {Type: TypeString, Sortable: true},
	}
	// "id" infers to TypeString (Go string), not TypeID — fix expectation.
	c := checks["id"]
	c.Type = TypeString
	checks["id"] = c

	for key, want := range checks {
		got, ok := reg[key]
		if !ok {
			t.Errorf("missing key %q", key)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("field %q:\n got %+v\nwant %+v", key, got, want)
		}
	}
}

func TestFromStruct_RoundTripsWithHandWritten(t *testing.T) {
	type mini struct {
		Status   string `filter:"status,enum=ACTIVE|ARCHIVED"`
		Severity int    `filter:"severity,col=f.severity"`
	}
	generated := MustFromStruct(mini{})
	handWritten := Registry{
		"status":   {Type: TypeEnum, Enum: []string{"ACTIVE", "ARCHIVED"}},
		"severity": {Type: TypeInt, Column: "f.severity"},
	}

	conds := []Condition{
		{Key: "status", Op: OpEq, Values: []any{"ACTIVE"}},
		{Key: "severity", Op: OpGte, Values: []any{7}},
	}
	gs, ga, err := generated.Compile(Postgres{}, conds)
	if err != nil {
		t.Fatal(err)
	}
	hs, ha, _ := handWritten.Compile(Postgres{}, conds)
	if gs != hs || !reflect.DeepEqual(ga, ha) {
		t.Errorf("generated != hand-written:\n gen  %q %v\n hand %q %v", gs, ga, hs, ha)
	}
}

func TestFromStruct_Errors(t *testing.T) {
	cases := []struct {
		name string
		v    any
	}{
		{"not a struct", 42},
		{"unknown option", struct {
			X string `filter:"x,bogus"`
		}{}},
		{"unknown type", struct {
			X string `filter:"x,type=weird"`
		}{}},
		{"uninferable type", struct {
			X chan int `filter:"x"`
		}{}},
		{"duplicate key", struct {
			A string `filter:"dup"`
			B string `filter:"dup"`
		}{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := FromStruct(c.v); err == nil {
				t.Errorf("expected error")
			}
		})
	}
}

func TestFromStruct_EmbeddedFlatten(t *testing.T) {
	type base struct {
		ID string `filter:"id,sortable"`
	}
	type derived struct {
		base
		Name string `filter:"name"`
	}
	reg := MustFromStruct(derived{})
	if _, ok := reg["id"]; !ok {
		t.Error("embedded field should be flattened in")
	}
	if _, ok := reg["name"]; !ok {
		t.Error("own field missing")
	}
}

func TestToSnakeCase(t *testing.T) {
	cases := map[string]string{
		"ID": "id", "Name": "name", "AssetName": "asset_name",
		"SeenAt": "seen_at", "HTTPStatus": "httpstatus",
	}
	for in, want := range cases {
		if got := toSnakeCase(in); got != want {
			t.Errorf("toSnakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func keysOf(r Registry) []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	return out
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

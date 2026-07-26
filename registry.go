package filtersql

import "sort"

func sortSchema(s []FieldSchema) {
	sort.Slice(s, func(i, j int) bool { return s[i].Key < s[j].Key })
}

// Field is one declarative registry entry.
type Field struct {
	Type   Type
	Column string // SQL column; drives the WHERE side. Defaults to the field key.
	// ValueExpr is the SELECT-side expression used by the values/projection flow
	// (see Registry.Project). It defaults to Column, then to the field key.
	//
	// INVARIANT: ValueExpr and Column must round-trip. If ValueExpr maps a stored
	// value to a display form (e.g. if(active,'Yes','No'), or a CASE turning ''
	// into 'SKIPPED'), the value the UI sends back to filter must still match on
	// the Column/WHERE side — so Column has to apply the same mapping, or the
	// dialect/coercion must accept the display form. Mismatch = a value shows in
	// the dropdown but filtering on it returns nothing.
	ValueExpr string
	Enum      []string // allowed values for TypeEnum (also surfaced by Schema)
	Hidden    bool     // resolvable in WHERE but omitted from Schema (virtual fields)
	Joins     []string // join keys this field needs; each names a Join in the Joins map
	// passed to CompileWithJoins / ProjectWithJoins. A join is emitted only when a
	// filter on this field actually resolves (WHERE) or the field is projected.
}

// Registry maps a filter key to its Field definition.
type Registry map[string]Field

// Condition is a node in a boolean filter tree. Exactly one form is used per
// node:
//
//   - leaf comparison — set Key and Op (plus Values, or Pairs for map ops)
//   - conjunction     — set And
//   - disjunction     — set Or
//   - negation        — set Not
//
// The zero Condition (nothing set) resolves to no SQL and is skipped. Passing a
// flat []Condition of leaves to Compile is the common case; And/Or/Not exist for
// arbitrarily nested trees a filter-builder UI produces.
//
// The JSON shape is deliberately UI-friendly:
//
//	{"or": [
//	  {"key": "severity", "op": "_gte", "values": [7]},
//	  {"and": [
//	    {"key": "status", "op": "_eq", "values": ["OPEN"]},
//	    {"not": {"key": "tags", "op": "_contains", "values": ["ignored"]}}
//	  ]}
//	]}
type Condition struct {
	Key    string     `json:"key,omitempty"`
	Op     Operator   `json:"op,omitempty"`
	Values []any      `json:"values,omitempty"`
	Pairs  []KeyValue `json:"pairs,omitempty"` // for map operators

	And []Condition `json:"and,omitempty"`
	Or  []Condition `json:"or,omitempty"`
	Not *Condition  `json:"not,omitempty"`
}

// KeyValue is a single map-filter entry, e.g. tag "env" in ("prod","stage").
type KeyValue struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// FieldSchema is the introspection view of one field, for a /filters-style
// endpoint. It is derived from the same data Compile executes against.
type FieldSchema struct {
	Key       string     `json:"key"`
	Type      Type       `json:"type"`
	Operators []Operator `json:"operators"`
	Enum      []string   `json:"enum,omitempty"`
}

// Schema returns the introspection view of the registry: every non-hidden
// field with the operators it accepts. Sorted by key for stable output.
func (r Registry) Schema() []FieldSchema {
	out := make([]FieldSchema, 0, len(r))
	for key, f := range r {
		if f.Hidden {
			continue
		}
		out = append(out, FieldSchema{
			Key:       key,
			Type:      f.Type,
			Operators: f.Type.Operators(),
			Enum:      f.Enum,
		})
	}
	sortSchema(out)
	return out
}

func (f Field) column(key string) string {
	if f.Column != "" {
		return f.Column
	}
	return key
}

func (f Field) validEnum(v string) bool {
	if len(f.Enum) == 0 {
		return true // no enum constraint declared
	}
	for _, e := range f.Enum {
		if e == v {
			return true
		}
	}
	return false
}

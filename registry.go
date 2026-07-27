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
	Nullable  bool     // when true, the field also accepts _is_null / _is_not_null
	Sortable  bool     // when true, the field may appear in OrderBy (and Schema advertises it)
	// Only, when non-empty, restricts the field to this subset of its type's
	// operators (an allowlist) — e.g. a large text column limited to {_eq}.
	Only []Operator
	// Except removes specific operators the type would otherwise allow (a
	// denylist) — e.g. everything but _like. Applied after Only.
	Except []Operator
	// Raw marks Column as a raw SQL expression rather than a plain identifier, so
	// it is passed through verbatim instead of being quoted by the dialect. Set it
	// whenever Column is a function/CASE expression (e.g. if(a.active,'Yes','No')):
	// without it, a dialect like Postgres would quote the whole expression as one
	// identifier and mangle it.
	Raw bool
	// Having marks this field as a HAVING-clause field: Column is an aggregate
	// expression (e.g. count(), sum(f.score)) filtered after GROUP BY. Implies Raw
	// (aggregate expressions are never quoted). Routed by CompileWhereHaving.
	Having bool
	Joins  []string // join keys this field needs; each names a Join in the Joins map
	// passed to CompileWithJoins / ProjectWithJoins. A join is emitted only when a
	// filter on this field actually resolves (WHERE) or the field is projected.
}

// effectiveOperators is the single source of truth for which operators this
// field accepts: its Type's operators (plus null operators when Nullable),
// narrowed by Only (allowlist) and then Except (denylist). Both allows() and
// schemaOperators() read it, so schema and execution can never disagree.
func (f Field) effectiveOperators() []Operator {
	ops := append([]Operator(nil), f.Type.Operators()...)
	if f.Nullable {
		ops = append(ops, nullOperators...)
	}
	if len(f.Only) > 0 {
		only := make(map[Operator]bool, len(f.Only))
		for _, o := range f.Only {
			only[o] = true
		}
		ops = filterOps(ops, func(o Operator) bool { return only[o] })
	}
	if len(f.Except) > 0 {
		except := make(map[Operator]bool, len(f.Except))
		for _, o := range f.Except {
			except[o] = true
		}
		ops = filterOps(ops, func(o Operator) bool { return !except[o] })
	}
	return ops
}

func filterOps(ops []Operator, keep func(Operator) bool) []Operator {
	out := ops[:0]
	for _, o := range ops {
		if keep(o) {
			out = append(out, o)
		}
	}
	return out
}

// allows reports whether op is valid for this field.
func (f Field) allows(op Operator) bool {
	for _, o := range f.effectiveOperators() {
		if o == op {
			return true
		}
	}
	return false
}

// schemaOperators is the operator list Schema advertises for the field.
func (f Field) schemaOperators() []Operator { return f.effectiveOperators() }

// whereExpr renders the WHERE-side reference. A plain identifier is quoted by
// the dialect; a raw expression (Raw, or a Having aggregate) passes through.
func (f Field) whereExpr(d Dialect, key string) string {
	c := f.column(key)
	if f.Raw || f.Having {
		return c
	}
	return d.QuoteIdent(c)
}

// selectExpr is the SELECT / ORDER BY / GROUP BY reference: ValueExpr (always a
// raw expression), else Column, else the key. It is intentionally emitted
// verbatim (not dialect-quoted) because ValueExpr is frequently an expression;
// for a case-sensitive identifier, quote it yourself in Column/ValueExpr. Kept
// identical between OrderBy and KeysetWhere so the sort order and the cursor
// predicate can never disagree.
func (f Field) selectExpr(key string) string {
	if f.ValueExpr != "" {
		return f.ValueExpr
	}
	return f.column(key)
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
	Sortable  bool       `json:"sortable,omitempty"`
	Having    bool       `json:"having,omitempty"` // filters after GROUP BY, not in WHERE
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
			Operators: f.schemaOperators(),
			Enum:      f.Enum,
			Sortable:  f.Sortable,
			Having:    f.Having,
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

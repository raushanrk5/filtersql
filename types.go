// Package filtersql turns a declarative registry of filterable fields plus a
// list of user-supplied filter inputs into a parameterized SQL WHERE fragment.
//
// One registry entry per field is the single source of truth for three things:
//   - which operators the field accepts (validation),
//   - how the field renders to SQL (execution),
//   - what the introspection endpoint advertises (Schema).
//
// SQL dialect specifics live behind the Dialect interface, so the same registry
// and the same operator model target ClickHouse, Postgres, MySQL, etc.
package filtersql

// Type is the logical data type of a filterable field. It is dialect-agnostic;
// how each type renders is decided at compile time by the Dialect.
type Type string

const (
	TypeString    Type = "string"
	TypeID        Type = "id"
	TypeInt       Type = "int"
	TypeFloat     Type = "float"
	TypeBool      Type = "bool"
	TypeTimestamp Type = "timestamp"
	TypeEnum      Type = "enum"
	TypeArray     Type = "array" // multi-valued column (ClickHouse Array, PG array/jsonb)
	TypeMap       Type = "map"   // key/value column (ClickHouse Map, PG jsonb)
)

// Operator is a filter operator. The leading underscore mirrors the common
// GraphQL/Hasura convention so filter payloads read the same over the wire.
type Operator string

const (
	OpEq         Operator = "_eq"
	OpNe         Operator = "_ne"
	OpIn         Operator = "_in"
	OpNin        Operator = "_nin"
	OpLike       Operator = "_like"        // case-insensitive substring
	OpStartsWith Operator = "_starts_with" // case-insensitive prefix
	OpGt         Operator = "_gt"
	OpGte        Operator = "_gte"
	OpLt         Operator = "_lt"
	OpLte        Operator = "_lte"

	OpContains       Operator = "_contains"     // array ⊇ all values
	OpContainsAny    Operator = "_contains_any" // array ∩ values ≠ ∅
	OpNotContains    Operator = "_not_contains" // NOT (array ⊇ all values)
	OpNotContainsAny Operator = "_not_contains_any"

	OpHasKeys         Operator = "_has_keys"
	OpNotHasKeys      Operator = "_not_has_keys"
	OpHasKeyValues    Operator = "_has_key_values"
	OpNotHasKeyValues Operator = "_not_has_key_values"

	// Null operators take no value and apply to any Nullable field regardless of
	// Type. They are not in typeOperators — a field opts in via Field.Nullable.
	OpIsNull    Operator = "_is_null"
	OpIsNotNull Operator = "_is_not_null"
)

// nullOperators are valid on any Field whose Nullable flag is set.
var nullOperators = []Operator{OpIsNull, OpIsNotNull}

func isNullOp(op Operator) bool { return op == OpIsNull || op == OpIsNotNull }

// NullsOrder controls where NULLs sort relative to non-NULLs in an ORDER BY term.
type NullsOrder int

const (
	NullsDefault NullsOrder = iota // leave it to the database's default
	NullsFirst
	NullsLast
)

// typeOperators is the single source of truth for which operators each Type
// accepts. Compile validates against it; Schema reports from it. They cannot
// drift because they read the same map.
var typeOperators = map[Type][]Operator{
	TypeString:    {OpEq, OpNe, OpIn, OpNin, OpLike, OpStartsWith},
	TypeID:        {OpEq, OpNe, OpIn, OpNin},
	TypeInt:       {OpEq, OpNe, OpIn, OpNin, OpGt, OpGte, OpLt, OpLte},
	TypeFloat:     {OpEq, OpNe, OpIn, OpNin, OpGt, OpGte, OpLt, OpLte},
	TypeBool:      {OpEq, OpNe},
	TypeTimestamp: {OpEq, OpGt, OpGte, OpLt, OpLte},
	TypeEnum:      {OpEq, OpNe, OpIn, OpNin},
	TypeArray:     {OpContains, OpContainsAny, OpNotContains, OpNotContainsAny},
	TypeMap:       {OpHasKeys, OpNotHasKeys, OpHasKeyValues, OpNotHasKeyValues},
}

// Operators returns the operators supported by a Type, or nil if unknown.
func (t Type) Operators() []Operator { return typeOperators[t] }

func (t Type) allows(op Operator) bool {
	for _, o := range typeOperators[t] {
		if o == op {
			return true
		}
	}
	return false
}

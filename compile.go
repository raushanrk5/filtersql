package filtersql

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Compile turns a filter tree into a single parameterized WHERE fragment and
// the ordered bind arguments for the given dialect. The top-level slice is
// AND-joined; individual Conditions may nest And/Or/Not to any depth.
//
// It returns an error if a key is unknown, an operator is illegal for the
// field's type, an enum value is out of range, a value fails coercion, or a
// Condition sets more than one form (e.g. both Key and And).
//
// A filter that resolves to no condition (e.g. an empty _in list) is skipped,
// not an error. If nothing resolves, sql is "" and args is nil — callers should
// guard their template with `if sql != ""`.
func (r Registry) Compile(d Dialect, conds []Condition) (sql string, args []any, err error) {
	sql, args, _, err = r.compile(d, conds, "")
	return sql, args, err
}

// CompileExcluding is Compile but skips every leaf whose Key == exclude. It
// powers the faceted values flow: the list of values offered for a field must
// reflect all OTHER active filters, not the field's own.
//
// Caveat: exclusion is leaf-level. Dropping a leaf that sits inside an Or arm
// broadens that arm — as it does in any faceted search — so mixing self-facets
// with OR groups on the same field needs a deliberate eye.
func (r Registry) CompileExcluding(d Dialect, conds []Condition, exclude string) (sql string, args []any, err error) {
	sql, args, _, err = r.compile(d, conds, exclude)
	return sql, args, err
}

// compile is the shared core: it renders the top-level AND of conds and returns
// the WHERE fragment, args, and the set of join keys referenced by emitted
// conditions. exclude, when non-empty, skips leaves on that field key.
func (r Registry) compile(d Dialect, conds []Condition, exclude string) (string, []any, map[string]bool, error) {
	q := newQuery(d)
	q.exclude = exclude
	sql, err := r.renderTop(q, conds)
	if err != nil {
		return "", nil, nil, err
	}
	return sql, q.Args(), q.joinKeys, nil
}

// renderTop renders a top-level AND list against an existing Query (so callers
// like CompileWhereHaving can share one argument counter across clauses, keeping
// $N placeholder numbering continuous). Unlike a group, the top level is not
// parenthesized.
func (r Registry) renderTop(q *Query, conds []Condition) (string, error) {
	var parts []string
	for _, c := range conds {
		s, err := r.renderCond(q, c)
		if err != nil {
			return "", err
		}
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " AND "), nil
}

// renderCond renders one node of the tree. Exactly one form must be set.
func (r Registry) renderCond(q *Query, c Condition) (string, error) {
	forms := 0
	if c.Key != "" {
		forms++
	}
	if len(c.And) > 0 {
		forms++
	}
	if len(c.Or) > 0 {
		forms++
	}
	if c.Not != nil {
		forms++
	}
	switch {
	case forms == 0:
		return "", nil // empty condition — skip
	case forms > 1:
		return "", fmt.Errorf("%w: set exactly one of key, and, or, not", ErrInvalidCondition)
	case len(c.And) > 0:
		return r.renderGroup(q, c.And, " AND ")
	case len(c.Or) > 0:
		return r.renderGroup(q, c.Or, " OR ")
	case c.Not != nil:
		inner, err := r.renderCond(q, *c.Not)
		if err != nil {
			return "", err
		}
		if inner == "" {
			return "", nil
		}
		return "NOT (" + inner + ")", nil
	default:
		return r.renderLeaf(q, c)
	}
}

// renderGroup renders a conjunction/disjunction, parenthesizing when it holds
// more than one live child so precedence is explicit against its parent.
func (r Registry) renderGroup(q *Query, conds []Condition, sep string) (string, error) {
	var parts []string
	for _, c := range conds {
		s, err := r.renderCond(q, c)
		if err != nil {
			return "", err
		}
		if s != "" {
			parts = append(parts, s)
		}
	}
	switch len(parts) {
	case 0:
		return "", nil
	case 1:
		return parts[0], nil
	default:
		return "(" + strings.Join(parts, sep) + ")", nil
	}
}

// renderLeaf resolves a single field comparison and records its join keys.
func (r Registry) renderLeaf(q *Query, c Condition) (string, error) {
	if c.Op == "" {
		return "", nil // key without operator — skip
	}
	if q.exclude != "" && c.Key == q.exclude {
		return "", nil // facet self-exclusion
	}
	f, ok := r[c.Key]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownField, c.Key)
	}
	if !f.allows(c.Op) {
		return "", fmt.Errorf("%w: %q on %s field %q", ErrBadOperator, c.Op, f.Type, c.Key)
	}
	col := f.whereExpr(q.d, c.Key)
	cond, err := resolve(q, f, col, c)
	if err != nil {
		// Past the allows() gate, any resolve failure is value-domain (coercion
		// or enum), so surface it as ErrBadValue for callers that branch on it.
		return "", fmt.Errorf("field %q: %w", c.Key, valueErr(err))
	}
	if cond != "" {
		for _, k := range f.Joins {
			q.joinKeys[k] = true
		}
	}
	return cond, nil
}

func resolve(q *Query, f Field, col string, in Condition) (string, error) {
	// Null operators are type-independent and take no value.
	switch in.Op {
	case OpIsNull:
		return col + " IS NULL", nil
	case OpIsNotNull:
		return col + " IS NOT NULL", nil
	}

	switch f.Type {
	case TypeString, TypeID, TypeEnum:
		return resolveScalar(q, f, col, in)
	case TypeInt, TypeFloat:
		return resolveNumeric(q, col, in)
	case TypeBool:
		return resolveBool(q, col, in)
	case TypeTimestamp:
		return resolveTimestamp(q, col, in)
	case TypeArray:
		return resolveArray(q, col, in)
	case TypeMap:
		return resolveMap(q, col, in)
	default:
		return "", fmt.Errorf("unsupported type %q", f.Type)
	}
}

func resolveScalar(q *Query, f Field, col string, in Condition) (string, error) {
	switch in.Op {
	case OpEq, OpNe:
		v, err := scalarStr(f, in.Values)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s %s", col, sqlCmp[in.Op], q.Arg(v)), nil
	case OpIn, OpNin:
		vals, err := enumStrSlice(f, in.Values)
		if err != nil {
			return "", err
		}
		if len(vals) == 0 {
			return "", nil
		}
		marks := make([]string, len(vals))
		for i, v := range vals {
			marks[i] = q.Arg(v)
		}
		neg := ""
		if in.Op == OpNin {
			neg = "NOT "
		}
		return fmt.Sprintf("%s %sIN (%s)", col, neg, strings.Join(marks, ", ")), nil
	case OpLike, OpStartsWith, OpEndsWith:
		v, err := scalarStr(f, in.Values)
		if err != nil {
			return "", err
		}
		match := MatchSubstring
		switch in.Op {
		case OpStartsWith:
			match = MatchPrefix
		case OpEndsWith:
			match = MatchSuffix
		}
		return q.d.Like(q, col, v, match), nil
	}
	return "", fmt.Errorf("operator %q not handled for scalar", in.Op)
}

func resolveNumeric(q *Query, col string, in Condition) (string, error) {
	switch in.Op {
	case OpEq, OpNe, OpGt, OpGte, OpLt, OpLte:
		v, err := toFloat(first(in.Values))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s %s", col, sqlCmp[in.Op], q.Arg(v)), nil
	case OpIn, OpNin:
		if len(in.Values) == 0 {
			return "", nil
		}
		marks := make([]string, 0, len(in.Values))
		for _, raw := range in.Values {
			v, err := toFloat(raw)
			if err != nil {
				return "", err
			}
			marks = append(marks, q.Arg(v))
		}
		neg := ""
		if in.Op == OpNin {
			neg = "NOT "
		}
		return fmt.Sprintf("%s %sIN (%s)", col, neg, strings.Join(marks, ", ")), nil
	case OpBetween:
		if len(in.Values) != 2 {
			return "", fmt.Errorf("%w: _between needs exactly 2 values, got %d", ErrBadValue, len(in.Values))
		}
		lo, err := toFloat(in.Values[0])
		if err != nil {
			return "", err
		}
		hi, err := toFloat(in.Values[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s BETWEEN %s AND %s", col, q.Arg(lo), q.Arg(hi)), nil
	}
	return "", fmt.Errorf("operator %q not handled for numeric", in.Op)
}

func resolveBool(q *Query, col string, in Condition) (string, error) {
	v, err := toBool(first(in.Values))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s %s", col, sqlCmp[in.Op], q.Arg(v)), nil
}

func resolveTimestamp(q *Query, col string, in Condition) (string, error) {
	if in.Op == OpBetween {
		if len(in.Values) != 2 {
			return "", fmt.Errorf("%w: _between needs exactly 2 values, got %d", ErrBadValue, len(in.Values))
		}
		lo, err := toString(in.Values[0])
		if err != nil {
			return "", err
		}
		hi, err := toString(in.Values[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s BETWEEN %s AND %s", col, q.Arg(lo), q.Arg(hi)), nil
	}
	v, err := toString(first(in.Values))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s %s", col, sqlCmp[in.Op], q.Arg(v)), nil
}

func resolveArray(q *Query, col string, in Condition) (string, error) {
	vals, err := strSlice(in.Values)
	if err != nil {
		return "", err
	}
	if len(vals) == 0 {
		return "", nil
	}
	all := in.Op == OpContains || in.Op == OpNotContains
	neg := in.Op == OpNotContains || in.Op == OpNotContainsAny
	cond := q.d.ArrayContains(q, col, vals, all)
	if neg {
		return "NOT (" + cond + ")", nil
	}
	return cond, nil
}

func resolveMap(q *Query, col string, in Condition) (string, error) {
	if len(in.Pairs) == 0 {
		return "", nil
	}
	var cond string
	switch in.Op {
	case OpHasKeys, OpNotHasKeys:
		keys := make([]string, len(in.Pairs))
		for i, p := range in.Pairs {
			keys[i] = p.Key
		}
		cond = q.d.MapHasKeys(q, col, keys)
	case OpHasKeyValues, OpNotHasKeyValues:
		cond = q.d.MapHasKeyValues(q, col, in.Pairs)
	default:
		return "", fmt.Errorf("operator %q not handled for map", in.Op)
	}
	if in.Op == OpNotHasKeys || in.Op == OpNotHasKeyValues {
		return "NOT (" + cond + ")", nil
	}
	return cond, nil
}

// sqlCmp maps comparison operators to their SQL symbol.
var sqlCmp = map[Operator]string{
	OpEq: "=", OpNe: "!=",
	OpGt: ">", OpGte: ">=", OpLt: "<", OpLte: "<=",
}

// --- coercion helpers ---

// valueErr tags a value-domain failure as ErrBadValue (unless it already is),
// preserving the original message for context.
func valueErr(err error) error {
	if errors.Is(err, ErrBadValue) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrBadValue, err)
}

func first(vals []any) any {
	if len(vals) == 0 {
		return nil
	}
	return vals[0]
}

func scalarStr(f Field, vals []any) (string, error) {
	s, err := toString(first(vals))
	if err != nil {
		return "", err
	}
	if !f.validEnum(s) {
		return "", fmt.Errorf("%w: %q not in enum", ErrBadValue, s)
	}
	return s, nil
}

func enumStrSlice(f Field, vals []any) ([]string, error) {
	out, err := strSlice(vals)
	if err != nil {
		return nil, err
	}
	for _, s := range out {
		if !f.validEnum(s) {
			return nil, fmt.Errorf("%w: %q not in enum", ErrBadValue, s)
		}
	}
	return out, nil
}

func strSlice(vals []any) ([]string, error) {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		s, err := toString(v)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func toString(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), nil
		}
		return strconv.FormatFloat(t, 'g', -1, 64), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case bool:
		return strconv.FormatBool(t), nil
	case nil:
		return "", fmt.Errorf("nil value")
	default:
		return fmt.Sprintf("%v", t), nil
	}
}

func toFloat(v any) (float64, error) {
	switch t := v.(type) {
	case float64:
		return t, nil
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, fmt.Errorf("cannot parse %q as number: %w", t, err)
		}
		return f, nil
	case nil:
		return 0, fmt.Errorf("nil numeric value")
	default:
		return 0, fmt.Errorf("cannot convert %T to number", v)
	}
}

func toBool(v any) (bool, error) {
	switch t := v.(type) {
	case bool:
		return t, nil
	case float64:
		return t != 0, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes":
			return true, nil
		case "false", "0", "no":
			return false, nil
		}
		return false, fmt.Errorf("invalid bool string: %q", t)
	case nil:
		return false, fmt.Errorf("nil bool value")
	default:
		return false, fmt.Errorf("cannot convert %T to bool", v)
	}
}

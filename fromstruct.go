package filtersql

import (
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// FromStruct builds a Registry from the `filter` struct tags on v, which may be
// a struct or a pointer to one. Only fields carrying a `filter` tag are
// included (explicit opt-in), so a struct can freely hold non-filterable fields.
//
// Tag grammar — comma-separated, first element is the key:
//
//	`filter:"status,enum=ACTIVE|ARCHIVED"`
//	`filter:"severity,sortable"`
//	`filter:"owner,nullable"`
//	`filter:"finding_count,type=int,col=count(),having"`
//	`filter:"-"`                 // skip this field
//	`filter:",sortable"`         // key omitted -> snake_case of the field name
//
// Options: type=<datatype>, col=<column>, valueexpr=<expr>, enum=A|B|C,
// joins=a|b, and the boolean flags sortable, nullable, hidden, raw, having.
// (Pipe-separated lists avoid clashing with the comma separator; a col or
// valueexpr containing a comma needs a hand-written registry.)
//
// The field type is inferred from the Go type — string, int/uint, float, bool,
// time.Time (timestamp), slice (array), map (map) — and a pointer field implies
// nullable. `type=` overrides the inference; it's required when the Go type is
// not inferable.
//
// Anonymous embedded structs are flattened, so shared filter sets compose.
func FromStruct(v any) (Registry, error) {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("FromStruct: expected a struct or *struct, got %T", v)
	}
	reg := Registry{}
	if err := collectFields(t, reg); err != nil {
		return nil, err
	}
	return reg, nil
}

// MustFromStruct is FromStruct that panics on error, for package-level vars.
func MustFromStruct(v any) Registry {
	reg, err := FromStruct(v)
	if err != nil {
		panic(err)
	}
	return reg
}

var timeType = reflect.TypeOf(time.Time{})

func collectFields(t reflect.Type, reg Registry) error {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)

		// Flatten anonymous embedded structs (skipping embedded time.Time).
		if sf.Anonymous {
			et := sf.Type
			for et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct && et != timeType {
				if err := collectFields(et, reg); err != nil {
					return err
				}
				continue
			}
		}

		tag, ok := sf.Tag.Lookup("filter")
		if !ok || tag == "-" {
			continue
		}
		key, field, err := parseFilterTag(sf, tag)
		if err != nil {
			return fmt.Errorf("field %s: %w", sf.Name, err)
		}
		if _, dup := reg[key]; dup {
			return fmt.Errorf("duplicate filter key %q", key)
		}
		reg[key] = field
	}
	return nil
}

func parseFilterTag(sf reflect.StructField, tag string) (string, Field, error) {
	parts := strings.Split(tag, ",")
	key := strings.TrimSpace(parts[0])
	if key == "" {
		key = toSnakeCase(sf.Name)
	}

	var f Field

	// Infer type (and nullability) from the Go type; a pointer implies nullable.
	ft := sf.Type
	if ft.Kind() == reflect.Pointer {
		f.Nullable = true
		ft = ft.Elem()
	}
	inferred, inferErr := inferDataType(ft)
	f.Type = inferred // may be overridden by type=; validated below

	typeExplicit := false
	for _, opt := range parts[1:] {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		name, val, hasVal := strings.Cut(opt, "=")
		switch name {
		case "type":
			f.Type, typeExplicit = Type(val), true
		case "col":
			f.Column = val
		case "valueexpr":
			f.ValueExpr = val
		case "enum":
			f.Enum = strings.Split(val, "|")
		case "joins":
			f.Joins = strings.Split(val, "|")
		case "sortable":
			f.Sortable = true
		case "nullable":
			f.Nullable = true
		case "hidden":
			f.Hidden = true
		case "raw":
			f.Raw = true
		case "having":
			f.Having = true
		default:
			return "", Field{}, fmt.Errorf("unknown filter option %q", name)
		}
		_ = hasVal
	}

	// enum implies TypeEnum unless the caller set a type explicitly.
	if len(f.Enum) > 0 && !typeExplicit && inferred == TypeString {
		f.Type = TypeEnum
	}
	// If we could not infer and no type= was given, that's an error.
	if f.Type == "" {
		if inferErr != nil {
			return "", Field{}, inferErr
		}
		return "", Field{}, fmt.Errorf("missing type")
	}
	if _, ok := typeOperators[f.Type]; !ok {
		return "", Field{}, fmt.Errorf("unknown filter type %q", f.Type)
	}
	return key, f, nil
}

func inferDataType(t reflect.Type) (Type, error) {
	if t == timeType {
		return TypeTimestamp, nil
	}
	switch t.Kind() {
	case reflect.String:
		return TypeString, nil
	case reflect.Bool:
		return TypeBool, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return TypeInt, nil
	case reflect.Float32, reflect.Float64:
		return TypeFloat, nil
	case reflect.Slice, reflect.Array:
		return TypeArray, nil
	case reflect.Map:
		return TypeMap, nil
	}
	return "", fmt.Errorf("cannot infer filter type for Go type %s (add type=...)", t)
}

// toSnakeCase converts an exported Go field name to snake_case, e.g.
// "AssetName" -> "asset_name", "ID" -> "id", "CVEScore" -> "cvescore".
func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			prev := rune(s[i-1])
			if !unicode.IsUpper(prev) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

package filtersql

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Value coercion: filter values arrive as any (JSON decodes numbers as float64,
// strings as string, etc.), and these helpers turn them into the concrete forms
// the resolvers bind. Enum validation lives here too, since it gates coercion.

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

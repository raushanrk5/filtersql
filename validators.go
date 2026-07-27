package filtersql

import (
	"fmt"
	"regexp"
	"strings"
)

// Transformer normalizes a filter value before it is bound (lowercase, trim,
// parse, canonicalize). It runs per value; return the value unchanged when it
// isn't the kind you handle.
type Transformer func(any) (any, error)

// Validator rejects an invalid filter value before it is bound. A non-nil error
// surfaces as ErrBadValue (a 400-class failure).
type Validator func(any) error

// --- built-in transformers ---

// Lower lowercases string values (others pass through).
func Lower() Transformer {
	return func(v any) (any, error) {
		if s, ok := v.(string); ok {
			return strings.ToLower(s), nil
		}
		return v, nil
	}
}

// Trim strips leading/trailing whitespace from string values.
func Trim() Transformer {
	return func(v any) (any, error) {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s), nil
		}
		return v, nil
	}
}

// --- built-in validators ---

// MaxLen rejects string values longer than n.
func MaxLen(n int) Validator {
	return func(v any) error {
		if s, ok := v.(string); ok && len(s) > n {
			return fmt.Errorf("value exceeds %d characters", n)
		}
		return nil
	}
}

// MinLen rejects string values shorter than n.
func MinLen(n int) Validator {
	return func(v any) error {
		if s, ok := v.(string); ok && len(s) < n {
			return fmt.Errorf("value shorter than %d characters", n)
		}
		return nil
	}
}

// Matches rejects string values that don't match re.
func Matches(re *regexp.Regexp) Validator {
	return func(v any) error {
		if s, ok := v.(string); ok && !re.MatchString(s) {
			return fmt.Errorf("value %q does not match %s", s, re)
		}
		return nil
	}
}

// applyHooks runs Transform then Validate over each value, returning a new slice.
func (f Field) applyHooks(vals []any) ([]any, error) {
	if f.Transform == nil && f.Validate == nil {
		return vals, nil
	}
	out := make([]any, len(vals))
	for i, v := range vals {
		if f.Transform != nil {
			tv, err := f.Transform(v)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrBadValue, err)
			}
			v = tv
		}
		if f.Validate != nil {
			if err := f.Validate(v); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrBadValue, err)
			}
		}
		out[i] = v
	}
	return out, nil
}

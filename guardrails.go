package filtersql

import "fmt"

// Limits bounds how complex a caller-supplied filter may be, so an abusive or
// runaway payload fails fast with ErrTooComplex (a 400) instead of compiling
// into a pathological query. A zero field means "no limit".
type Limits struct {
	MaxDepth      int // maximum and/or/not nesting depth
	MaxConditions int // maximum total leaf conditions
	MaxValues     int // maximum values (or map pairs) in a single filter
}

// Check verifies conds against the limits, returning ErrTooComplex on the first
// violation. It walks the whole tree without touching the database, so it's
// cheap to run at the edge before compiling.
func (l Limits) Check(conds []Condition) error {
	count := 0
	var walk func(c Condition, depth int) error
	walk = func(c Condition, depth int) error {
		if l.MaxDepth > 0 && depth > l.MaxDepth {
			return fmt.Errorf("%w: nesting deeper than %d", ErrTooComplex, l.MaxDepth)
		}
		switch {
		case len(c.And) > 0:
			for _, s := range c.And {
				if err := walk(s, depth+1); err != nil {
					return err
				}
			}
		case len(c.Or) > 0:
			for _, s := range c.Or {
				if err := walk(s, depth+1); err != nil {
					return err
				}
			}
		case c.Not != nil:
			return walk(*c.Not, depth+1)
		case c.Key != "":
			count++
			if l.MaxConditions > 0 && count > l.MaxConditions {
				return fmt.Errorf("%w: more than %d conditions", ErrTooComplex, l.MaxConditions)
			}
			n := len(c.Values)
			if len(c.Pairs) > n {
				n = len(c.Pairs)
			}
			if l.MaxValues > 0 && n > l.MaxValues {
				return fmt.Errorf("%w: field %q has %d values (max %d)", ErrTooComplex, c.Key, n, l.MaxValues)
			}
		}
		return nil
	}
	for _, c := range conds {
		if err := walk(c, 1); err != nil {
			return err
		}
	}
	return nil
}

// CompileWithLimits enforces lim, then compiles. A payload that blows a limit
// returns ErrTooComplex before any SQL is built.
func (r Registry) CompileWithLimits(d Dialect, conds []Condition, lim Limits) (string, []any, error) {
	if err := lim.Check(conds); err != nil {
		return "", nil, err
	}
	return r.Compile(d, conds)
}

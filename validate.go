package filtersql

import (
	"errors"
	"fmt"
	"sort"
)

// Validate checks a registry (and the join set it will be used with) for
// configuration mistakes that would otherwise surface as broken SQL at request
// time. Call it once at startup and fail fast. It reports every problem it finds
// (joined), not just the first:
//
//   - a field with an unknown Type,
//   - a field referencing a join key not present in joins,
//   - a field marked Sortable that is also Having (a HAVING aggregate can't be an
//     ORDER BY key in the pre-grouped sense — almost always a mistake),
//   - a join dependency cycle or a Requires naming an undefined join.
//
// Pass nil joins if the registry uses none. Validate never touches the database.
func (r Registry) Validate(joins Joins) error {
	var errs []error

	for _, key := range sortedFieldKeys(r) {
		f := r[key]
		if _, ok := typeOperators[f.Type]; !ok {
			errs = append(errs, fmt.Errorf("field %q: unknown type %q", key, f.Type))
		}
		for _, jk := range f.Joins {
			if _, ok := joins[jk]; !ok {
				errs = append(errs, fmt.Errorf("field %q: references undefined join %q", key, jk))
			}
		}
		if f.Sortable && f.Having {
			errs = append(errs, fmt.Errorf("field %q: cannot be both Sortable and Having", key))
		}
	}

	// Whole-graph cycle / undefined-dependency check: order every join at once.
	if len(joins) > 0 {
		all := make(map[string]bool, len(joins))
		for k := range joins {
			all[k] = true
		}
		if _, err := orderJoins(joins, all); err != nil {
			errs = append(errs, fmt.Errorf("joins: %w", err))
		}
	}

	return errors.Join(errs...)
}

func sortedFieldKeys(r Registry) []string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic error ordering
	return keys
}

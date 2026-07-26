package filtersql

import (
	"fmt"
	"sort"
	"strings"
)

// Join is a declarative JOIN fragment. SQL is the literal join clause (the
// caller owns any schema/alias interpolation); Requires names other join keys
// that must appear before this one.
type Join struct {
	SQL      string
	Requires []string
}

// Joins maps a join key to its definition.
type Joins map[string]Join

// CompileWithJoins is Compile plus dependency-ordered JOIN emission. It returns
// the WHERE fragment, the JOIN SQL (only the joins reachable from filters that
// actually resolved, newline-joined in dependency order), and the bind args.
//
// A join is pulled in when a filter on a field carrying its key resolves to a
// condition; transitive Requires are pulled in with it. The order is
// deterministic (dependencies first, lexicographic tie-break) so identical
// filter sets produce byte-identical SQL. Cyclic or undefined Requires error.
func (r Registry) CompileWithJoins(d Dialect, joins Joins, conds []Condition) (where, joinSQL string, args []any, err error) {
	where, args, keys, err := r.compile(d, conds, "")
	if err != nil {
		return "", "", nil, err
	}
	joinSQL, err = orderJoins(joins, keys)
	if err != nil {
		return "", "", nil, err
	}
	return where, joinSQL, args, nil
}

// orderJoins expands the needed keys with their transitive Requires and returns
// the join SQL in dependency order (a join that Requires another comes after
// it). Deterministic: needed keys and each Requires list are visited in sorted
// order. Errors on an undefined key or a dependency cycle.
func orderJoins(defs Joins, needed map[string]bool) (string, error) {
	const (
		visiting = 1
		done     = 2
	)
	state := make(map[string]int, len(defs))
	var order []string

	var dfs func(key string, path []string) error
	dfs = func(key string, path []string) error {
		switch state[key] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("cyclic join dependency: %s", strings.Join(append(path, key), " -> "))
		}
		def, ok := defs[key]
		if !ok {
			return fmt.Errorf("undefined join key: %q", key)
		}
		state[key] = visiting

		reqs := append([]string(nil), def.Requires...)
		sort.Strings(reqs)
		for _, dep := range reqs {
			if err := dfs(dep, append(path, key)); err != nil {
				return err
			}
		}

		state[key] = done
		order = append(order, key)
		return nil
	}

	for _, key := range sortedKeys(needed) {
		if err := dfs(key, nil); err != nil {
			return "", err
		}
	}

	var b strings.Builder
	for i, key := range order {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(defs[key].SQL)
	}
	return b.String(), nil
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

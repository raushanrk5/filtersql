package filtersql

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// LimitOffset renders a "LIMIT n OFFSET m" clause (OFFSET omitted when 0). It
// returns "" when limit is 0 (no page size requested). limit and offset are
// integers inlined directly — they are never user-controlled strings, so there
// is no injection surface. Negative values are an error.
//
// LIMIT/OFFSET is standard across ClickHouse, Postgres, MySQL and SQLite, so no
// dialect is needed.
func LimitOffset(limit, offset int) (string, error) {
	if limit < 0 || offset < 0 {
		return "", fmt.Errorf("limit and offset must be >= 0 (got limit=%d offset=%d)", limit, offset)
	}
	if limit == 0 {
		return "", nil
	}
	if offset > 0 {
		return fmt.Sprintf("LIMIT %d OFFSET %d", limit, offset), nil
	}
	return fmt.Sprintf("LIMIT %d", limit), nil
}

// Cursor holds the sort-key values of the last row of a page, keyed by sort key.
// It is the opaque state keyset pagination carries between pages.
type Cursor map[string]any

// EncodeCursor serializes a cursor to a URL-safe base64 string for a next-page token.
func EncodeCursor(c Cursor) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor parses a token produced by EncodeCursor. An empty string yields a
// nil cursor (i.e. "first page"), not an error.
func DecodeCursor(token string) (Cursor, error) {
	if token == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor: %w", err)
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("invalid cursor: %w", err)
	}
	return c, nil
}

// KeysetWhere builds the seek predicate that fetches rows strictly after cur,
// under the same sort spec used for ORDER BY. It returns a WHERE fragment and
// its args; a nil/empty cursor yields "" (first page).
//
// For sorts (s0, s1, ...) with cursor values (c0, c1, ...) it emits the standard
// lexicographic "seek" chain, honoring each key's direction:
//
//	(e0 > c0)
//	OR (e0 = c0 AND e1 < c1)         -- if s1 is Desc
//	OR (e0 = c0 AND e1 = c1 AND e2 > c2)
//	...
//
// AND it with your filter WHERE. Requirements (standard for keyset pagination,
// not enforced here): the sort keys must be NON-NULL and their combination
// UNIQUE (add a unique tie-breaker like the primary key as the last sort), or
// pages can repeat or skip rows.
func (r Registry) KeysetWhere(d Dialect, sorts []Sort, cur Cursor) (string, []any, error) {
	if len(sorts) == 0 || len(cur) == 0 {
		return "", nil, nil
	}

	exprs := make([]string, len(sorts))
	for i, s := range sorts {
		f, ok := r[s.Key]
		if !ok {
			return "", nil, fmt.Errorf("unknown sort field: %q", s.Key)
		}
		if !f.Sortable {
			return "", nil, fmt.Errorf("field %q is not sortable", s.Key)
		}
		if _, has := cur[s.Key]; !has {
			return "", nil, fmt.Errorf("cursor missing value for sort key %q", s.Key)
		}
		exprs[i] = f.sortExpr(s.Key)
	}

	q := newQuery(d)
	ors := make([]string, 0, len(sorts))
	for i := range sorts {
		ands := make([]string, 0, i+1)
		for j := 0; j < i; j++ { // equalities on the higher-priority keys
			ands = append(ands, fmt.Sprintf("%s = %s", exprs[j], q.Arg(cur[sorts[j].Key])))
		}
		cmp := ">"
		if sorts[i].Desc {
			cmp = "<"
		}
		ands = append(ands, fmt.Sprintf("%s %s %s", exprs[i], cmp, q.Arg(cur[sorts[i].Key])))
		ors = append(ors, "("+strings.Join(ands, " AND ")+")")
	}
	return "(" + strings.Join(ors, " OR ") + ")", q.Args(), nil
}

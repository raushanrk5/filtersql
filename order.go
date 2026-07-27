package filtersql

import (
	"fmt"
	"strings"
)

// Sort is one ORDER BY term over a Sortable field.
type Sort struct {
	Key   string     `json:"key"`
	Desc  bool       `json:"desc,omitempty"`
	Nulls NullsOrder `json:"nulls,omitempty"`
}

// OrderBy renders the ORDER BY body (no leading "ORDER BY ") for the given sorts,
// e.g. "a.created DESC, a.id ASC". Every sort key must reference a field marked
// Sortable — this allowlist is what keeps a user-supplied sort key from becoming
// an arbitrary ORDER BY expression. An empty sorts slice yields "".
func (r Registry) OrderBy(d Dialect, sorts []Sort) (string, error) {
	if len(sorts) == 0 {
		return "", nil
	}
	terms := make([]string, 0, len(sorts))
	for _, s := range sorts {
		f, ok := r[s.Key]
		if !ok {
			return "", fmt.Errorf("%w: %q", ErrUnknownField, s.Key)
		}
		if !f.Sortable {
			return "", fmt.Errorf("%w: field %q is not sortable", ErrBadOperator, s.Key)
		}
		terms = append(terms, d.OrderTerm(f.selectExpr(s.Key), s.Desc, s.Nulls))
	}
	return strings.Join(terms, ", "), nil
}

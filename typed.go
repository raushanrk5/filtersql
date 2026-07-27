package filtersql

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// Querier is the read side of *sql.DB / *sql.Tx — whatever this package needs to
// run a query. Both stdlib types satisfy it.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// structInfo maps a struct's `db`-tagged fields to SQL columns.
type structInfo struct {
	columns  []string         // db column names, in struct order
	colIndex map[string][]int // column -> field index path (for FieldByIndex)
}

var structInfoCache sync.Map // reflect.Type -> structInfo

func infoFor[T any]() structInfo {
	t := reflect.TypeFor[T]()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if cached, ok := structInfoCache.Load(t); ok {
		return cached.(structInfo)
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("filtersql: type %s is not a struct", t))
	}
	info := structInfo{colIndex: map[string][]int{}}
	var walk func(t reflect.Type, prefix []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			idx := append(append([]int(nil), prefix...), i)
			if f.Anonymous {
				ft := f.Type
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft, idx)
					continue
				}
			}
			tag := f.Tag.Get("db")
			if tag == "" || tag == "-" {
				continue
			}
			col := strings.Split(tag, ",")[0]
			info.columns = append(info.columns, col)
			info.colIndex[col] = idx
		}
	}
	walk(t, nil)
	if len(info.columns) == 0 {
		panic(fmt.Sprintf("filtersql: type %s has no `db`-tagged fields", t))
	}
	structInfoCache.Store(t, info)
	return info
}

// ScanAll scans every row of rs into a []T, matching result columns to T's
// `db`-tagged fields. Columns with no matching field are discarded (so a
// SELECT * still works). It does not close rs.
func ScanAll[T any](rs *sql.Rows) ([]T, error) {
	return scanRows[T](rs, infoFor[T]())
}

// ScanOne scans exactly one row into a T, returning sql.ErrNoRows if there is none.
func ScanOne[T any](rs *sql.Rows) (T, error) {
	var zero T
	out, err := scanRows[T](rs, infoFor[T]())
	if err != nil {
		return zero, err
	}
	if len(out) == 0 {
		return zero, sql.ErrNoRows
	}
	return out[0], nil
}

func scanRows[T any](rs *sql.Rows, info structInfo) ([]T, error) {
	cols, err := rs.Columns()
	if err != nil {
		return nil, err
	}
	idxs := make([][]int, len(cols))
	for i, c := range cols {
		idxs[i] = info.colIndex[c] // nil -> discard column
	}

	var out []T
	for rs.Next() {
		var v T
		rv := reflect.ValueOf(&v).Elem()
		targets := make([]any, len(cols))
		for i := range cols {
			if idxs[i] == nil {
				var discard any
				targets[i] = &discard
				continue
			}
			targets[i] = rv.FieldByIndex(idxs[i]).Addr().Interface()
		}
		if err := rs.Scan(targets...); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rs.Err()
}

// Typed pairs a Registry with a row type T and a table name, so a query can be
// compiled, executed, and scanned into []T in one flow. It still leaves SELECT/
// FROM shape and tenant scoping to you (via Scope) — the FROM table is the only
// thing it fills in.
type Typed[T any] struct {
	reg   Registry
	table string
	info  structInfo
}

// For builds a Typed[T]. It panics if T is not a struct or has no `db` tags —
// both are programmer errors surfaced at startup.
func For[T any](reg Registry, table string) *Typed[T] {
	return &Typed[T]{reg: reg, table: table, info: infoFor[T]()}
}

// Select starts a typed query for the given dialect.
func (t *Typed[T]) Select(d Dialect) *TypedSelect[T] {
	return &TypedSelect[T]{t: t, d: d}
}

// TypedSelect accumulates a query. Rendering is deferred to SQL/All and always
// happens in canonical order (scope, WHERE, keyset seek), so method call order
// never affects the placeholder numbering.
type TypedSelect[T any] struct {
	t       *Typed[T]
	d       Dialect
	scopeFn func(*Builder) string
	conds   []Condition
	sorts   []Sort
	cursor  string
	limit   int
}

// Scope adds a caller-owned predicate (e.g. tenant scoping). Build the predicate
// with the *Builder so its placeholders share the query's numbering:
//
//	.Scope(func(b *filtersql.Builder) string { return "tenant_id = " + b.Arg(tid) })
func (s *TypedSelect[T]) Scope(fn func(*Builder) string) *TypedSelect[T] { s.scopeFn = fn; return s }

// Where sets the filter conditions.
func (s *TypedSelect[T]) Where(conds []Condition) *TypedSelect[T] { s.conds = conds; return s }

// Sort sets the ORDER BY (and the basis for keyset paging). Include a unique key
// (e.g. id) last so pages don't repeat.
func (s *TypedSelect[T]) Sort(sorts []Sort) *TypedSelect[T] { s.sorts = sorts; return s }

// After applies a next-page cursor (from a previous All).
func (s *TypedSelect[T]) After(cursor string) *TypedSelect[T] { s.cursor = cursor; return s }

// Limit caps the page size. All fetches one extra row internally to compute the
// next cursor.
func (s *TypedSelect[T]) Limit(n int) *TypedSelect[T] { s.limit = n; return s }

// SQL renders the query and its args without executing (handy for logging/tests).
func (s *TypedSelect[T]) SQL() (string, []any, error) {
	q, args, err := s.build(s.limit)
	return q, args, err
}

func (s *TypedSelect[T]) build(limit int) (string, []any, error) {
	b := s.t.reg.Builder(s.d)

	var where []string
	if s.scopeFn != nil {
		if p := s.scopeFn(b); p != "" {
			where = append(where, p)
		}
	}
	filt, err := b.Where(s.conds)
	if err != nil {
		return "", nil, err
	}
	if filt != "" {
		where = append(where, filt)
	}
	if s.cursor != "" {
		cur, derr := DecodeCursor(s.cursor)
		if derr != nil {
			return "", nil, derr
		}
		seek, serr := b.Keyset(s.sorts, cur)
		if serr != nil {
			return "", nil, serr
		}
		if seek != "" {
			where = append(where, seek)
		}
	}
	order, err := b.OrderBy(s.sorts)
	if err != nil {
		return "", nil, err
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(s.t.info.columns, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(s.t.table)
	if len(where) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(where, " AND "))
	}
	if order != "" {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(order)
	}
	if limit > 0 {
		lim, lerr := LimitOffset(limit, 0)
		if lerr != nil {
			return "", nil, lerr
		}
		sb.WriteString(" ")
		sb.WriteString(lim)
	}
	return sb.String(), b.Args(), nil
}

// All runs the query and scans the rows into []T. If Limit was set and there are
// more rows, it returns a non-empty next cursor (built from the last row's sort
// values). The cursor is empty when there is no next page or a sort key can't be
// mapped to a struct column.
func (s *TypedSelect[T]) All(ctx context.Context, db Querier) ([]T, string, error) {
	fetch := s.limit
	if fetch > 0 {
		fetch++ // one extra row tells us whether a next page exists
	}
	query, args, err := s.build(fetch)
	if err != nil {
		return nil, "", err
	}
	rs, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rs.Close()
	out, err := scanRows[T](rs, s.t.info)
	if err != nil {
		return nil, "", err
	}

	next := ""
	if s.limit > 0 && len(out) > s.limit {
		out = out[:s.limit]
		if cur := s.cursorFor(out[len(out)-1]); cur != nil {
			next, _ = EncodeCursor(cur)
		}
	}
	return out, next, nil
}

// Count returns the total number of rows matching Scope + Where, ignoring Sort,
// After, and Limit — for "showing 1–20 of 340" pagination UIs. It reuses the
// exact filter set of the page query, so the count and the page can't disagree.
func (s *TypedSelect[T]) Count(ctx context.Context, db Querier) (int64, error) {
	b := s.t.reg.Builder(s.d)
	var where []string
	if s.scopeFn != nil {
		if p := s.scopeFn(b); p != "" {
			where = append(where, p)
		}
	}
	filt, err := b.Where(s.conds)
	if err != nil {
		return 0, err
	}
	if filt != "" {
		where = append(where, filt)
	}
	query := "SELECT count(*) FROM " + s.t.table
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	rs, err := db.QueryContext(ctx, query, b.Args()...)
	if err != nil {
		return 0, err
	}
	defer rs.Close()
	var n int64
	if rs.Next() {
		if err := rs.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rs.Err()
}

// cursorFor reads the sort-key values off the last row to form the next cursor.
// Returns nil (no cursor) if a sort key's column isn't a scannable struct field.
func (s *TypedSelect[T]) cursorFor(v T) Cursor {
	rv := reflect.ValueOf(v)
	c := Cursor{}
	for _, srt := range s.sorts {
		f := s.t.reg[srt.Key]
		col := f.Column
		if col == "" {
			col = srt.Key
		}
		idx, ok := s.t.info.colIndex[col]
		if !ok {
			return nil
		}
		c[srt.Key] = rv.FieldByIndex(idx).Interface()
	}
	return c
}

// Ergonomics / real-world patterns

// Multi-field search field — a virtual field that expands to (name ILIKE ? OR description ILIKE ? OR id ILIKE ?), so a single "search box" input hits several columns. Extremely common; I have prior art from the original design. Practical and self-contained.
// Total-count helper — pagination UIs want "showing 1–20 of 340"; a helper to produce the COUNT(*) query for the same filter set (minus limit/sort). Natural companion to what we built.
// Extensibility

// Value transformers & validators — per-field Transform func(any)(any,error) (lowercase, trim, parse dates, canonicalize) and Validate (regex, min/max, length) hooks that run before binding. Unlocks a lot without bloating the core.

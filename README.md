# filtersql

**One declarative registry per resource drives your `WHERE` clause, your JOINs, your "available values" dropdowns, and your filter-schema endpoint — parameterized, injection-safe, and dialect-pluggable.**

Most services grow a filter layer by hand: a request struct, a translator that stitches SQL strings, a schema endpoint that describes the filters, and template blocks that conditionally add JOINs. Adding one filter means editing all four, and they drift. `filtersql` collapses that into a single map:

```go
var registry = filtersql.Registry{
    "status":   {Type: filtersql.TypeEnum, Column: "a.status", Enum: []string{"ACTIVE", "ARCHIVED"}},
    "severity": {Type: filtersql.TypeInt, Column: "f.severity", Joins: []string{"finding"}},
    "tags":     {Type: filtersql.TypeArray, Column: "a.tags"},
}
```

One entry per field is the single source of truth. It decides which operators the field accepts, how it renders to SQL, which JOIN it needs, and what the introspection endpoint advertises — so those can't disagree.

> **Status:** pre-release (v0.x). Zero dependencies. The API may still change.

---

## Install

```sh
go get github.com/raushanrk5/filtersql
```

## Quickstart

```go
package main

import (
    "fmt"
    "github.com/raushanrk5/filtersql"
)

var registry = filtersql.Registry{
    "status": {Type: filtersql.TypeEnum, Column: "a.status", Enum: []string{"ACTIVE", "ARCHIVED"}},
    "tags":   {Type: filtersql.TypeArray, Column: "a.tags"},
}

func main() {
    filters := []filtersql.Condition{
        {Key: "status", Op: filtersql.OpEq, Values: []any{"ACTIVE"}},
        {Key: "tags", Op: filtersql.OpContainsAny, Values: []any{"prod", "critical"}},
    }

    where, args, err := registry.Compile(filtersql.ClickHouse{}, filters)
    if err != nil {
        panic(err)
    }
    fmt.Println(where) // a.status = ? AND hasAny(a.tags, ?)
    fmt.Println(args)  // [ACTIVE [prod critical]]
}
```

`Compile` returns a `WHERE` **fragment** plus its bind args — you slot it into your own base query (which owns the table name and tenant scoping). It never builds a whole query for you, so nothing host-specific leaks into the library.

Run the full tour: **`go run ./example`**.

---

## What you get from one registry

### 1. A parameterized WHERE — nested `and` / `or` / `not`

The common case is a flat slice (AND-joined). For a real filter-builder UI, a `Condition` nests to any depth and decodes straight from JSON:

```json
[{"or":[
  {"and":[
    {"key":"status","op":"_eq","values":["ACTIVE"]},
    {"key":"severity","op":"_gte","values":[7]}
  ]},
  {"not":{"key":"tags","op":"_contains_any","values":["ignored"]}}
]}]
```

```
((a.status = ? AND f.severity >= ?) OR NOT (hasAny(a.tags, ?)))
args: [ACTIVE 7 [ignored]]
```

Parenthesization is precedence-driven; empty branches (e.g. an `_in` with no values) collapse instead of emitting `()`.

### 2. Dependency-ordered JOINs

A field declares the join keys it needs; you declare each JOIN once, with its dependencies:

```go
var joins = filtersql.Joins{
    "finding": {SQL: "INNER JOIN finding f ON f.asset_id = a.id"},
    "policy":  {SQL: "INNER JOIN policy p ON p.finding_id = f.id", Requires: []string{"finding"}},
}

where, joinSQL, args, _ := registry.CompileWithJoins(filtersql.ClickHouse{}, joins, filters)
```

Filtering on a `policy` field emits both joins, in the right order:

```
WHERE: p.name = ?
JOINS:
INNER JOIN finding f ON f.asset_id = a.id
INNER JOIN policy p ON p.finding_id = f.id
```

Only joins reachable from filters that actually resolved are emitted; the order is deterministic (dependencies first, lexicographic tie-break); cyclic or undefined dependencies error rather than producing wrong SQL.

### 3. Faceted "available values" queries

`ValuesQuery` assembles everything a `/filter-values?field=X` endpoint needs — the projected expression, the WHERE from **every other** active filter (the facet doesn't filter itself), and the JOINs both of those need:

```go
vq, _ := registry.ValuesQuery(filtersql.ClickHouse{}, joins, "severity", activeFilters)
// SELECT DISTINCT f.severity AS value
// FROM asset a
// INNER JOIN finding f ON f.asset_id = a.id      <- projection still needs the join
// WHERE tenant_id = ? AND a.status = ?           <- severity's own filter excluded
```

`ValueExpr` lets the SELECT side differ from the WHERE side (e.g. `if(f.exploited,'Yes','No')` for a display label) — with the documented invariant that the display value must round-trip back through the filter.

### 4. Aggregation & facet counts

`FacetCounts` is the `ValuesQuery` sibling for `Critical (42)`-style sidebars — rows per value of a field, self-excluding that field's own filter:

```go
fc, _ := registry.FacetCounts(filtersql.ClickHouse{}, joins, "status", activeFilters)
// SELECT a.status AS bucket, count() AS n
// FROM asset a INNER JOIN finding f ON f.asset_id = a.id
// WHERE tenant_id = ? AND f.severity >= ?     <- status's own filter excluded
// GROUP BY a.status
```

`AggregateQuery` is the general form — `count`/`count_distinct`/`sum`/`avg`/`min`/`max`, optionally grouped, with an explicit `Exclude` knob so metric tiles (all filters apply) and facets (self-excluded) are different calls, not a guess:

```go
agg, _ := registry.AggregateQuery(dialect, joins,
    filtersql.Aggregation{GroupBy: "status", Func: filtersql.Avg, Metric: "severity"},
    activeFilters)
// SELECT a.status AS bucket, avg(f.severity) AS value ... GROUP BY a.status
```

`count()` vs `count(*)` is dialect-rendered; `sum`/`avg` validate the metric is numeric.

### 5. A schema endpoint that can't drift

```go
json.NewEncoder(w).Encode(registry.Schema())
```

```json
[
  {"key": "status", "type": "enum",
   "operators": ["_eq", "_ne", "_in", "_nin"],
   "enum": ["ACTIVE", "ARCHIVED"]},
  {"key": "tags", "type": "array",
   "operators": ["_contains", "_contains_any", "_not_contains", "_not_contains_any"]}
]
```

The operator list comes from the same `Type → operators` table the resolver executes against, so the schema always reports exactly what the engine will accept. Mark a field `Hidden: true` to keep a virtual/search field out of the UI while still filterable.

### 6. Sorting & pagination — the rest of a list endpoint

Filtering is half of a list endpoint; sorting and paging are the other half, and they come from the same registry.

**Sorting** is gated by a `Sortable` flag — which is also a security control: it allowlists which columns a user-supplied sort key may reach, so sorting can't become arbitrary `ORDER BY` injection.

```go
order, _ := registry.OrderBy(dialect, []filtersql.Sort{
    {Key: "severity", Desc: true, Nulls: filtersql.NullsLast},
    {Key: "id"}, // unique tie-breaker
})
// f.severity DESC NULLS LAST, a.id ASC
```

**Null operators** — mark a field `Nullable: true` and it accepts `_is_null` / `_is_not_null` (no value), which Schema then advertises.

**Pagination** — offset for simple cases, keyset/cursor for stable deep paging:

```go
page1, _ := filtersql.LimitOffset(50, 0)       // "LIMIT 50"

// Next page: encode the last row's sort values, then build the seek predicate
// from the SAME sort spec — so ORDER BY and the cursor can never disagree.
token, _  := filtersql.EncodeCursor(filtersql.Cursor{"severity": 7, "id": "asset-123"})
cur, _    := filtersql.DecodeCursor(token)
seek, args, _ := registry.KeysetWhere(dialect, sorts, cur)
// ((f.severity < ?) OR (f.severity = ? AND a.id > ?))   -- AND this with your filter WHERE
```

Keyset requires non-null, unique sort keys (add a unique tie-breaker like the id as the last sort) — the standard seek-method contract.

---

## Dialects

The compiler validates and builds portable SQL; only the genuinely divergent bits — placeholder style, identifier quoting, `LIKE`/`ILIKE`, array containment, map/JSON access — go through a `Dialect`. So the same registry targets different databases:

| | ClickHouse | Postgres |
|---|---|---|
| placeholder | `?` | `$1, $2` |
| `_contains_any` on array | `hasAny(a.tags, ?)` | `"a"."tags" && $1` |
| `_like` | `ILIKE` (wildcards escaped) | `ILIKE` (wildcards escaped) |
| map has key/value | `col[?] = ?` | `col ->> ? = ?` |

Adding a dialect is implementing one small interface:

```go
type Dialect interface {
    Placeholder(n int) string
    QuoteIdent(ident string) string
    Like(q *Query, col, pattern string, prefix bool) string
    ArrayContains(q *Query, col string, values []string, all bool) string
    MapHasKeys(q *Query, col string, keys []string) string
    MapHasKeyValues(q *Query, col string, pairs []KeyValue) string
}
```

Ships with `ClickHouse{}` and `Postgres{}`.

---

## Types & operators

| Type | Operators |
|---|---|
| `string`, `id`, `enum` | `_eq` `_ne` `_in` `_nin` `_like` `_starts_with` *(id/enum: no text ops)* |
| `int`, `float` | `_eq` `_ne` `_in` `_nin` `_gt` `_gte` `_lt` `_lte` |
| `bool` | `_eq` `_ne` |
| `timestamp` | `_eq` `_gt` `_gte` `_lt` `_lte` |
| `array` | `_contains` `_contains_any` `_not_contains` `_not_contains_any` |
| `map` | `_has_keys` `_not_has_keys` `_has_key_values` `_not_has_key_values` |
| *any `Nullable` field* | `_is_null` `_is_not_null` (no value) |

An operator illegal for a field's type is a compile error, not silent wrong SQL.

## Safety

Every user-supplied value is a bound argument — nothing is string-interpolated into SQL. `_like` escapes `%` and `_` so a literal value can't inject a pattern; array/map literals are escaped per dialect. The engine is injection-safe at its boundary.

## Roadmap

- More dialects (MySQL, SQLite)
- Executing integration tests + injection fuzz test + CI
- `HAVING` support (filter on aggregates)
- Per-field operator allow/deny overrides
- Raw-expression vs identifier distinction for `Column`/`ValueExpr` (projection-side quoting)

## License

[MIT](LICENSE)

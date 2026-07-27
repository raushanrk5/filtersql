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

- **`go run ./example`** — a printed tour of every capability (no database).
- **`cd cookbook && go run .`** — a complete, runnable HTTP service (`/filters`, `/assets/values`, `/assets/search` with filtering, sorting, and keyset pagination) backed by SQLite. The clearest "how do I use this in my service" reference.

### Or generate the registry from a struct

Prefer tags to a hand-written map? `FromStruct` builds the registry from `filter:"..."` tags. Only tagged fields are included (explicit opt-in), the type is inferred from the Go type, and a pointer implies `nullable`:

```go
type Asset struct {
    ID       string  `filter:"id,sortable"`
    Status   string  `filter:"status,enum=ACTIVE|ARCHIVED"`
    Severity int     `filter:"severity,col=f.severity,sortable,joins=finding"`
    Owner    *string `filter:"owner"`                        // pointer -> nullable
    Count    int     `filter:"finding_count,col=count(),having"`
    Internal string  // no tag -> not filterable
}

var Assets = filtersql.MustFromStruct(Asset{})
```

Options: `type=`, `col=`, `valueexpr=`, `enum=A|B|C`, `joins=a|b`, and the flags `sortable`, `nullable`, `hidden`, `raw`, `having`. Anonymous embedded structs are flattened, so shared filter sets compose.

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

| | ClickHouse | Postgres | SQLite | MySQL |
|---|---|---|---|---|
| placeholder | `?` | `$1, $2` | `?` | `?` |
| identifier quote | *(none)* | `"a"."b"` | `"a"."b"` | `` `a`.`b` `` |
| `_like` | `ILIKE` | `ILIKE` | `LIKE … ESCAPE` | `LOWER(col) LIKE` |
| `_contains_any` on array | `hasAny(col, ?)` | `col && $1::text[]` | `json_each` | `JSON_OVERLAPS(col, ?)` |
| map has key/value | `col[?] = ?` | `col ->> ? = ?` | `json_each` | `JSON_EXTRACT(col, ?)` |
| `NULLS LAST` | native | native | native | `col IS NULL, col …` |

Adding a dialect is implementing one small interface:

```go
type Dialect interface {
    Placeholder(n int) string
    QuoteIdent(ident string) string
    Like(q *Query, col, pattern string, prefix bool) string
    ArrayContains(q *Query, col string, values []string, all bool) string
    MapHasKeys(q *Query, col string, keys []string) string
    MapHasKeyValues(q *Query, col string, pairs []KeyValue) string
    Aggregate(fn AggFunc, expr string) string
    OrderTerm(expr string, desc bool, nulls NullsOrder) string
}
```

Ships with `ClickHouse{}`, `Postgres{}`, `SQLite{}`, and `MySQL{}` (SQLite stores arrays/maps as JSON text, queried via `json_each`).

---

## Types & operators

| Type | Operators |
|---|---|
| `string` | `_eq` `_ne` `_in` `_nin` `_like` `_starts_with` `_ends_with` |
| `id`, `enum` | `_eq` `_ne` `_in` `_nin` |
| `int`, `float` | `_eq` `_ne` `_in` `_nin` `_gt` `_gte` `_lt` `_lte` `_between` |
| `bool` | `_eq` `_ne` |
| `timestamp` | `_eq` `_gt` `_gte` `_lt` `_lte` `_between` `_last` `_within` |
| `array` | `_contains` `_contains_any` `_not_contains` `_not_contains_any` |
| `map` | `_has_keys` `_not_has_keys` `_has_key_values` `_not_has_key_values` |
| *any `Nullable` field* | `_is_null` `_is_not_null` (no value) |

`_between` takes exactly two values `[lo, hi]` (inclusive). `_last` / `_within` take a compact interval — `"7d"`, `"24h"`, `"30m"`, `"2w"` — and render dialect-specific date math (`_last "7d"` → `col BETWEEN now()-7d AND now()`); the amount is parsed to a validated integer, so nothing user-typed reaches the SQL. An operator illegal for a field's type is a compile error, not silent wrong SQL.

**Per-field operator overrides.** Narrow a field to a subset of its type's operators — an allowlist (`Only`) or a denylist (`Except`). Both `Schema()` and execution read the same narrowed set, so they stay consistent:

```go
{Type: filtersql.TypeString, Column: "a.body", Only: []filtersql.Operator{filtersql.OpEq}}   // huge column: equality only
{Type: filtersql.TypeString, Column: "a.name", Except: []filtersql.Operator{filtersql.OpLike}} // no substring scans
```

In struct tags: `filter:"body,only=_eq"` / `filter:"name,except=_like"`.

## Multi-field search

A "search box" input usually needs to hit several columns at once. A `SearchCols` field is virtual — no single `Column` — and expands to an OR across the columns, each resolved with the same operator:

```go
"q": {Type: filtersql.TypeString, SearchCols: []string{"a.name", "a.email"}, Hidden: true}
// {key:"q", op:"_like", values:["web"]}
// -> (a.name ILIKE ? OR a.email ILIKE ?)
```

Pair with `Hidden` to keep it out of the schema's field list. Struct tag: `filter:"q,search=a.name|a.email,hidden"`.

## Value transformers & validators

Per-field hooks run on every value before it is bound: `Transform` normalizes (lowercase, trim, parse), `Validate` rejects (a failure is `ErrBadValue`).

```go
{Type: filtersql.TypeString, Column: "a.email",
    Transform: filtersql.Lower(),        // Foo@BAR -> foo@bar, on every value incl. _in lists
    Validate:  filtersql.MaxLen(320)}
```

Built-ins: `Lower()`, `Trim()`, `MaxLen(n)`, `MinLen(n)`, `Matches(re)` — or supply your own `func(any)(any,error)` / `func(any)error`.

## HAVING — filtering on aggregates

Mark a field `Having: true` and its `Column` is treated as an aggregate expression filtered after `GROUP BY`. `CompileWhereHaving` splits two filter lists and — crucially — shares one placeholder counter so `$N` numbering stays continuous across the clause boundary:

```go
reg := filtersql.Registry{
    "status":        {Type: filtersql.TypeEnum, Column: "a.status"},
    "finding_count": {Type: filtersql.TypeInt, Column: "count()", Having: true},
}
where, having, args, _ := reg.CompileWhereHaving(filtersql.Postgres{},
    []filtersql.Condition{{Key: "status", Op: filtersql.OpEq, Values: []any{"ACTIVE"}}},
    []filtersql.Condition{{Key: "finding_count", Op: filtersql.OpGt, Values: []any{5}}})
// where  = "a"."status" = $1
// having = count() > $2          <- $2 continues after WHERE's $1
```

Putting a HAVING field in the `where` list (or vice-versa) is an `ErrInvalidCondition`.

## Raw expressions vs identifiers

`Column` is quoted as an identifier by default. When it's a SQL expression rather than a plain column, set `Raw: true` so it passes through verbatim instead of being mangled by a dialect's identifier quoter:

```go
{Type: filtersql.TypeBool, Column: "if(a.active,'Yes','No')", Raw: true}
// Postgres WHERE: if(a.active,'Yes','No') = $1   (not "if(a.active,...)" quoted)
```

`Having` fields imply `Raw`.

## Validate at boot

Catch registry/join misconfiguration at startup instead of at the first bad request. `Validate` reports *every* problem it finds (unknown types, fields referencing undefined joins, join cycles, `Sortable`+`Having` mixups):

```go
if err := registry.Validate(joins); err != nil {
    log.Fatalf("filter registry misconfigured: %v", err)
}
```

## Typed errors

Compile-time failures wrap sentinel errors so a handler can map them to status codes — the four request-domain errors mean "the caller's filter is malformed" (400), everything else is a server fault (500):

```go
switch {
case errors.Is(err, filtersql.ErrUnknownField),
     errors.Is(err, filtersql.ErrBadOperator),
     errors.Is(err, filtersql.ErrBadValue),
     errors.Is(err, filtersql.ErrInvalidCondition):
    http.Error(w, err.Error(), http.StatusBadRequest)
default:
    http.Error(w, "internal error", http.StatusInternalServerError)
}
```

## Assembling a statement — the `Builder`

`Compile`, `KeysetWhere`, and `CompileWhereHaving` each start their own placeholder counter. Combining a filter WHERE *and* a keyset seek by hand is therefore safe for `?`-dialects but **breaks on Postgres** — both restart at `$1` and the args misalign. The `Builder` threads **one** counter through every fragment:

```go
b := reg.Builder(filtersql.Postgres{})
tenant := b.Arg(tenantID)          // $1  — your own tenant scoping shares the numbering
where, _ := b.Where(filters)       // $2 …
seek, _  := b.Keyset(sort, cursor) // continues …
order, _ := b.OrderBy(sort)
page, _  := b.Limit(50, 0)

sql := "SELECT id FROM asset WHERE tenant_id = " + tenant
if where != "" { sql += " AND " + where }
if seek  != "" { sql += " AND " + seek }
sql += " ORDER BY " + order + " " + page
rows, _ := db.Query(sql, b.Args()...)   // args in matching order
```

You still own `SELECT`/`FROM` and tenant scoping — the Builder just guarantees the placeholders and args line up. Call the render methods in the order the fragments appear (WHERE parts, then `Having`). The [cookbook](cookbook/) uses it.

## Typed queries & row scanning

If you'd rather not hand-write `SELECT`/`rows.Scan`, `For[T]` pairs a registry with a row type (its `db` tags name the columns) and runs the whole thing — compile, execute, scan into `[]T`, and return the next cursor:

```go
type Asset struct {
    ID       string `db:"id"`
    Name     string `db:"name"`
    Severity int    `db:"severity"`
}

assets := filtersql.For[Asset](registry, "asset")

page, next, err := assets.Select(dialect).
    Scope(func(b *filtersql.Builder) string { return "tenant_id = " + b.Arg(tenantID) }).
    Where(filters).
    Sort([]filtersql.Sort{{Key: "severity", Desc: true}, {Key: "id"}}).
    After(cursor).      // "" for the first page
    Limit(50).
    All(ctx, db)        // db is any *sql.DB / *sql.Tx
// page is []Asset; next is the cursor for the following page ("" if none)
```

Rendering is deferred and always ordered (scope → WHERE → seek), so method call order never affects placeholder numbering. `ScanAll[T]` / `ScanOne[T]` are also exported for scanning arbitrary `*sql.Rows`. This layer uses only `database/sql` from the standard library — **still no third-party dependencies.**

For "showing 1–20 of 340" UIs, `.Count(ctx, db)` runs the same filter set without sort/limit/cursor, so the total and the page can't disagree:

```go
total, err := assets.Select(dialect).Scope(scope).Where(filters).Count(ctx, db)
```

## Safety

Every user-supplied value is a bound argument — nothing is string-interpolated into SQL. `_like` escapes `%` and `_` so a literal value can't inject a pattern; array/map literals are escaped per dialect. The engine is injection-safe at its boundary.

## Testing

The root module has zero dependencies and is unit-tested with string assertions. The `integration/` directory is a **separate module** (so its driver never enters the library's dependency graph) that runs the generated SQL against a real in-memory SQLite database, asserting on actual result rows — filtering, JSON array containment, keyset paging, and HAVING:

```sh
go test ./...              # unit tests, no deps
cd integration && go test  # executing tests against real SQLite
```

## Roadmap

- More dialects (MySQL)
- Per-field operator allow/deny overrides
- Production guardrails (max depth / IN-size / condition count)
- More operators (`_between`, `_ends_with`)

## License

[MIT](LICENSE)

// Package filtersql turns a declarative registry of filterable fields, plus a
// list of user-supplied filter conditions, into a parameterized SQL WHERE
// fragment (and matching ORDER BY, JOIN, aggregation, and pagination fragments).
//
// One registry entry per field is the single source of truth for three things:
// which operators the field accepts, how it renders to SQL, and what the
// introspection endpoint advertises — so they cannot drift. Every user-supplied
// value is a bound argument; nothing is string-interpolated. Dialect specifics
// live behind the Dialect interface (implemented in the dialects subpackage),
// so one registry targets ClickHouse, Postgres, SQLite, and MySQL.
//
// See the README for a guided tour, ./example for a printed walkthrough, and
// ./cookbook for a runnable HTTP service.
//
// # Packages
//
//	filtersql           the engine: Registry/Field/Condition, Compile, the
//	                    Dialect interface, and every query feature (joins, order,
//	                    pagination, aggregation, having, builder, guardrails,
//	                    projection, validators).
//	filtersql/dialects  the concrete renderers: ClickHouse, Postgres, SQLite,
//	                    MySQL. Import this to pass a dialect to Compile.
//	filtersql/bind      reflection helpers: FromStruct (build a Registry from
//	                    struct tags), For[T]/ScanAll (execute + scan into []T).
//
// A typical caller imports filtersql and dialects; bind is optional.
//
// # Engine file layout
//
// The engine package stays flat (a directory is a Go package). Files group by
// concern:
//
//	Core        types.go, registry.go, compile.go, coerce.go, errors.go,
//	            dialect.go (the Dialect interface + Query)
//	Features    joins.go, order.go, pagination.go, aggregate.go, having.go,
//	            projection.go, builder.go, guardrails.go
//	Inputs      validators.go (Transform/Validate hooks), validate.go
//
// The integration/ and cookbook/ directories are separate modules, so their
// database drivers never enter this package's (zero) dependency graph.
package filtersql

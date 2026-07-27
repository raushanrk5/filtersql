// Package filtersql turns a declarative registry of filterable fields, plus a
// list of user-supplied filter conditions, into a parameterized SQL WHERE
// fragment (and matching ORDER BY, JOIN, aggregation, and pagination fragments).
//
// One registry entry per field is the single source of truth for three things:
// which operators the field accepts, how it renders to SQL, and what the
// introspection endpoint advertises — so they cannot drift. Every user-supplied
// value is a bound argument; nothing is string-interpolated. Dialect specifics
// live behind the Dialect interface, so one registry targets ClickHouse,
// Postgres, SQLite, and MySQL.
//
// See the README for a guided tour, ./example for a printed walkthrough, and
// ./cookbook for a runnable HTTP service.
//
// # Package layout
//
// The package is intentionally flat (a directory is a Go package; splitting the
// core would break the public API and force exporting internals). Files group by
// concern:
//
//	Core engine
//	  types.go        Type, Operator, and the type→operators table
//	  registry.go     Field, Registry, Condition, Schema
//	  compile.go      Compile / CompileExcluding and the recursive resolvers
//	  coerce.go       value coercion (any → string/float/bool) + enum checks
//	  errors.go       sentinel errors (ErrUnknownField, ErrBadValue, …)
//
//	Dialects (implement the Dialect interface)
//	  dialect.go          the Dialect interface, Query, optional upgrades
//	  dialect_shared.go   helpers shared by the concrete dialects
//	  dialect_clickhouse.go / _postgres.go / _sqlite.go / _mysql.go
//
//	Query features (each adds one capability off the same registry)
//	  joins.go        dependency-ordered JOIN emission
//	  order.go        ORDER BY (Sortable-gated)
//	  pagination.go   LimitOffset + keyset cursors
//	  aggregate.go    aggregation + facet counts
//	  having.go       filtering on aggregates
//	  projection.go   faceted "available values" queries
//	  builder.go      shared placeholder counter across clauses
//	  guardrails.go   Limits: reject abusive payloads (ErrTooComplex)
//
//	Inputs & ergonomics
//	  fromstruct.go   build a Registry from struct tags
//	  typed.go        For[T]: compile → execute → scan into []T
//	  validators.go   per-field Transform / Validate hooks + built-ins
//	  validate.go     Registry.Validate for start-up checks
//
// The integration/ and cookbook/ directories are separate modules, so their
// database drivers never enter this package's (zero) dependency graph.
package filtersql

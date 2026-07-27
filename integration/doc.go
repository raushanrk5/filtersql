// Package integration runs filtersql's generated SQL against a real (in-memory,
// pure-Go) SQLite database, asserting on actual result rows rather than on SQL
// strings. It lives in its own module so the driver never touches the library's
// dependency graph.
//
// Run: cd integration && go test ./...
package integration

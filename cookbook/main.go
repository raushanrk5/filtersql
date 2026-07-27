// Command cookbook is a runnable reference service that shows how to wire
// filtersql into real HTTP handlers. It is deliberately read-top-to-bottom.
//
// The whole point filtersql makes: you define ONE registry, and it drives your
// filter endpoint, your values endpoint, and the WHERE/ORDER BY/pagination of
// your list endpoint. filtersql returns SQL *fragments* + args — you keep your
// own FROM clause, your tenant scoping, and your driver.
//
// Run:  cd cookbook && go run .
// Then: curl -s localhost:8080/filters | jq
//
//	curl -s localhost:8080/assets/values?field=status
//	curl -s -XPOST localhost:8080/assets/search -d '{
//	  "filters":[{"key":"status","op":"_eq","values":["ACTIVE"]}],
//	  "sort":[{"key":"severity","desc":true}], "limit":2 }' | jq
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	fq "github.com/raushanrk5/filtersql"
	_ "modernc.org/sqlite"
)

// dialect used everywhere in this service.
var dialect fq.Dialect = fq.SQLite{}

// assetFilters is the single source of truth for how assets can be filtered,
// sorted, and projected. Adding a new filterable field is one line here — no SQL
// edits, no schema-endpoint edits.
var assetFilters = fq.Registry{
	"id":       {Type: fq.TypeID, Column: "id", Sortable: true},
	"name":     {Type: fq.TypeString, Column: "name", Sortable: true},
	"status":   {Type: fq.TypeEnum, Column: "status", Enum: []string{"ACTIVE", "ARCHIVED"}, Sortable: true},
	"severity": {Type: fq.TypeInt, Column: "severity", Sortable: true},
	"owner":    {Type: fq.TypeString, Column: "owner", Nullable: true},
	"tags":     {Type: fq.TypeArray, Column: "tags"},
}

// demoTenant stands in for the tenant id you would pull from auth/JWT on each
// request. Tenant isolation is the APPLICATION's job — filtersql never sees it.
const demoTenant = "t1"

type server struct{ db *sql.DB }

func main() {
	db := mustSeed()
	s := &server{db: db}

	http.HandleFunc("/filters", s.handleFilters)
	http.HandleFunc("/assets/values", s.handleValues)
	http.HandleFunc("/assets/search", s.handleSearch)

	log.Println("cookbook listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// GET /filters — the frontend builds its entire filter UI from this. Because it
// is derived from the same registry Compile runs against, it can never drift.
func (s *server) handleFilters(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, assetFilters.Schema())
}

// GET /assets/values?field=status — distinct values for a filter dropdown.
func (s *server) handleValues(w http.ResponseWriter, r *http.Request) {
	field := r.URL.Query().Get("field")

	vq, err := assetFilters.ValuesQuery(dialect, nil, field, nil)
	if err != nil {
		writeErr(w, err) // e.g. ErrUnknownField -> 400
		return
	}

	// We own the FROM and the tenant scoping; filtersql gave us the projection.
	query := fmt.Sprintf("SELECT DISTINCT %s AS v FROM asset WHERE tenant_id = ?", vq.Expr)
	if vq.Where != "" {
		query += " AND " + vq.Where
	}
	query += " ORDER BY v"

	args := append([]any{demoTenant}, vq.Args...)
	values, err := scanStrings(s.db, query, args)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": values})
}

type searchReq struct {
	Filters []fq.Condition `json:"filters"`
	Sort    []fq.Sort      `json:"sort"`
	Limit   int            `json:"limit"`
	Cursor  string         `json:"cursor"`
}

type searchResp struct {
	Rows       []asset `json:"rows"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

// POST /assets/search — the list endpoint: filter + sort + keyset pagination.
func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return
	}
	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 20 // sane default / ceiling
	}

	// Keyset needs a unique, stable sort suffix — append id if the caller didn't.
	sort := req.Sort
	if !hasSortKey(sort, "id") {
		sort = append(sort, fq.Sort{Key: "id"})
	}

	// 1. WHERE from the caller's filters.
	where, args, err := assetFilters.Compile(dialect, req.Filters)
	if err != nil {
		writeErr(w, err)
		return
	}
	// 2. ORDER BY from the caller's sort spec (Sortable-gated).
	order, err := assetFilters.OrderBy(dialect, sort)
	if err != nil {
		writeErr(w, err)
		return
	}
	// 3. Keyset seek predicate from the cursor, built off the SAME sort spec.
	var seek string
	var seekArgs []any
	if req.Cursor != "" {
		cur, derr := fq.DecodeCursor(req.Cursor)
		if derr != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid cursor"))
			return
		}
		if seek, seekArgs, err = assetFilters.KeysetWhere(dialect, sort, cur); err != nil {
			writeErr(w, err)
			return
		}
	}

	// 4. Assemble into OUR base query. Tenant scoping is ours to enforce.
	query := "SELECT id, name, status, severity, owner FROM asset WHERE tenant_id = ?"
	all := []any{demoTenant}
	if where != "" {
		query += " AND " + where
		all = append(all, args...)
	}
	if seek != "" {
		query += " AND " + seek
		all = append(all, seekArgs...)
	}
	query += " ORDER BY " + order
	query += fmt.Sprintf(" LIMIT %d", limit+1) // fetch one extra to detect "more"

	rows, err := scanAssets(s.db, query, all)
	if err != nil {
		writeErr(w, err)
		return
	}

	// 5. Build the next cursor from the last returned row's sort values.
	resp := searchResp{Rows: rows}
	if len(rows) > limit {
		resp.Rows = rows[:limit]
		last := resp.Rows[len(resp.Rows)-1]
		if tok, cerr := fq.EncodeCursor(cursorValues(sort, last)); cerr == nil {
			resp.NextCursor = tok
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- helpers ---

type asset struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Severity int     `json:"severity"`
	Owner    *string `json:"owner"`
}

func cursorValues(sort []fq.Sort, a asset) fq.Cursor {
	c := fq.Cursor{}
	for _, srt := range sort {
		switch srt.Key {
		case "id":
			c["id"] = a.ID
		case "name":
			c["name"] = a.Name
		case "status":
			c["status"] = a.Status
		case "severity":
			c["severity"] = a.Severity
		}
	}
	return c
}

func hasSortKey(sorts []fq.Sort, key string) bool {
	for _, s := range sorts {
		if s.Key == key {
			return true
		}
	}
	return false
}

// writeErr maps filtersql's typed errors to HTTP status codes: a malformed
// filter is the client's fault (400), anything else is ours (500).
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fq.ErrUnknownField),
		errors.Is(err, fq.ErrBadOperator),
		errors.Is(err, fq.ErrBadValue),
		errors.Is(err, fq.ErrInvalidCondition):
		writeJSON(w, http.StatusBadRequest, errBody(err.Error()))
	default:
		log.Printf("internal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, errBody("internal error"))
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }

func scanAssets(db *sql.DB, query string, args []any) ([]asset, error) {
	rs, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []asset
	for rs.Next() {
		var a asset
		if err := rs.Scan(&a.ID, &a.Name, &a.Status, &a.Severity, &a.Owner); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rs.Err()
}

func scanStrings(db *sql.DB, query string, args []any) ([]string, error) {
	rs, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var s string
		if err := rs.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rs.Err()
}

// mustSeed builds an in-memory database with a handful of assets for tenant t1.
func mustSeed() *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE asset (
		tenant_id TEXT, id TEXT, name TEXT, status TEXT,
		severity INTEGER, owner TEXT, tags TEXT)`); err != nil {
		log.Fatal(err)
	}
	seed := []asset{
		{"a1", "web-01", "ACTIVE", 9, ptr("alice")},
		{"a2", "web-02", "ACTIVE", 5, nil},
		{"a3", "db-01", "ARCHIVED", 8, ptr("bob")},
		{"a4", "api-01", "ACTIVE", 7, ptr("alice")},
	}
	tags := map[string]string{"a1": `["prod","crit"]`, "a2": `["prod"]`, "a3": `["db"]`, "a4": `["prod","api"]`}
	for _, a := range seed {
		if _, err := db.Exec(`INSERT INTO asset VALUES (?,?,?,?,?,?,?)`,
			demoTenant, a.ID, a.Name, a.Status, a.Severity, a.Owner, tags[a.ID]); err != nil {
			log.Fatal(err)
		}
	}
	return db
}

func ptr(s string) *string { return &s }

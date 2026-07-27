package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	s := &server{db: mustSeed()}
	t.Cleanup(func() { s.db.Close() })
	return s
}

func TestFiltersEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleFilters(rec, httptest.NewRequest(http.MethodGet, "/filters", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var schema []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &schema); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(schema) == 0 {
		t.Fatal("expected fields in schema")
	}
}

func TestValuesEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleValues(rec, httptest.NewRequest(http.MethodGet, "/assets/values?field=status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Values []string `json:"values"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if got, want := strings.Join(body.Values, ","), "ACTIVE,ARCHIVED"; got != want {
		t.Errorf("values = %q, want %q", got, want)
	}
}

func TestValuesEndpoint_UnknownFieldIs400(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleValues(rec, httptest.NewRequest(http.MethodGet, "/assets/values?field=bogus", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func search(t *testing.T, s *server, body string) searchResp {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleSearch(rec, httptest.NewRequest(http.MethodPost, "/assets/search", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var resp searchResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func ids(rows []asset) string {
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(r.ID)
	}
	return b.String()
}

func TestSearch_FilterSortAndKeysetPaging(t *testing.T) {
	s := newTestServer(t)

	// ACTIVE assets sorted by severity desc: a1(9), a4(7), a2(5). Page size 2.
	page1 := search(t, s, `{
		"filters":[{"key":"status","op":"_eq","values":["ACTIVE"]}],
		"sort":[{"key":"severity","desc":true}],
		"limit":2 }`)
	if got := ids(page1.Rows); got != "a1,a4" {
		t.Fatalf("page1 = %q, want a1,a4", got)
	}
	if page1.NextCursor == "" {
		t.Fatal("expected a next_cursor for page1")
	}

	// Page 2 via the cursor: only a2 remains.
	page2 := search(t, s, `{
		"filters":[{"key":"status","op":"_eq","values":["ACTIVE"]}],
		"sort":[{"key":"severity","desc":true}],
		"limit":2, "cursor":"`+page1.NextCursor+`" }`)
	if got := ids(page2.Rows); got != "a2" {
		t.Errorf("page2 = %q, want a2", got)
	}
	if page2.NextCursor != "" {
		t.Errorf("expected no next_cursor on last page, got %q", page2.NextCursor)
	}
}

func TestSearch_UnknownFieldIs400(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleSearch(rec, httptest.NewRequest(http.MethodPost, "/assets/search",
		strings.NewReader(`{"filters":[{"key":"bogus","op":"_eq","values":["x"]}]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

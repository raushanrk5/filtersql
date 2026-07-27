package integration

import (
	"database/sql"
	"testing"

	fq "github.com/raushanrk5/filtersql"
)

// TestReltime_SQLite proves the relative-time SQL actually executes and filters
// correctly against real timestamps (rows placed relative to now()).
func TestReltime_SQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`CREATE TABLE ev (id TEXT, at TEXT)`); err != nil {
		t.Fatal(err)
	}
	seed := map[string]string{
		"recent": "-1 day",
		"old":    "-10 days",
		"future": "+2 days",
	}
	for id, mod := range seed {
		if _, err := db.Exec(`INSERT INTO ev VALUES (?, datetime('now', ?))`, id, mod); err != nil {
			t.Fatal(err)
		}
	}

	reg := fq.Registry{"at": {Type: fq.TypeTimestamp, Column: "at"}}

	// _last 7d -> only "recent" (old is too far back; future isn't in the past window).
	where, args, _ := reg.Compile(fq.SQLite{}, []fq.Condition{{Key: "at", Op: fq.OpLast, Values: []any{"7d"}}})
	if got := ids(t, db, "SELECT id FROM ev WHERE "+where, args); len(got) != 1 || got[0] != "recent" {
		t.Errorf("_last 7d = %v, want [recent]", got)
	}

	// _within 3d -> only "future".
	w2, a2, _ := reg.Compile(fq.SQLite{}, []fq.Condition{{Key: "at", Op: fq.OpWithin, Values: []any{"3d"}}})
	if got := ids(t, db, "SELECT id FROM ev WHERE "+w2, a2); len(got) != 1 || got[0] != "future" {
		t.Errorf("_within 3d = %v, want [future]", got)
	}
}

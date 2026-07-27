package integration

import (
	"context"
	"strings"
	"testing"

	fq "github.com/raushanrk5/filtersql"
)

// asset is a row type: its `db` tags name the columns to SELECT and scan into.
type asset struct {
	ID       string `db:"id"`
	Name     string `db:"name"`
	Status   string `db:"status"`
	Severity int    `db:"severity"`
}

var typedReg = fq.Registry{
	"status":   {Type: fq.TypeEnum, Column: "status", Enum: []string{"ACTIVE", "ARCHIVED"}},
	"severity": {Type: fq.TypeInt, Column: "severity", Sortable: true},
	"id":       {Type: fq.TypeID, Column: "id", Sortable: true},
}

func assetIDs(rows []asset) string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return strings.Join(ids, ",")
}

func TestTyped_ExecuteScanAndPage(t *testing.T) {
	db := setup(t)
	tt := fq.For[asset](typedReg, "asset")
	ctx := context.Background()

	// ACTIVE assets by severity desc: a1(9), a4(7), a2(5). Page size 2.
	page1, next, err := tt.Select(fq.SQLite{}).
		Where([]fq.Condition{{Key: "status", Op: fq.OpEq, Values: []any{"ACTIVE"}}}).
		Sort([]fq.Sort{{Key: "severity", Desc: true}, {Key: "id"}}).
		Limit(2).
		All(ctx, db)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if got := assetIDs(page1); got != "a1,a4" {
		t.Fatalf("page1 ids = %q, want a1,a4", got)
	}
	// Scanning actually populated the struct fields.
	if page1[0].Name != "web-01" || page1[0].Severity != 9 {
		t.Errorf("row not scanned: %+v", page1[0])
	}
	if next == "" {
		t.Fatal("expected a next cursor")
	}

	// Page 2 via the cursor: only a2 remains, and no further cursor.
	page2, next2, err := tt.Select(fq.SQLite{}).
		Where([]fq.Condition{{Key: "status", Op: fq.OpEq, Values: []any{"ACTIVE"}}}).
		Sort([]fq.Sort{{Key: "severity", Desc: true}, {Key: "id"}}).
		Limit(2).
		After(next).
		All(ctx, db)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if got := assetIDs(page2); got != "a2" {
		t.Errorf("page2 ids = %q, want a2", got)
	}
	if next2 != "" {
		t.Errorf("expected no next cursor, got %q", next2)
	}
}

func TestTyped_Count(t *testing.T) {
	db := setup(t)
	tt := fq.For[asset](typedReg, "asset")
	// 3 ACTIVE assets (a1, a2, a4). Count ignores sort/limit/cursor.
	n, err := tt.Select(fq.SQLite{}).
		Where([]fq.Condition{{Key: "status", Op: fq.OpEq, Values: []any{"ACTIVE"}}}).
		Sort([]fq.Sort{{Key: "severity", Desc: true}}).
		Limit(2).
		Count(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
}

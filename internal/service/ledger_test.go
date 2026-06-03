package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

func ledgerTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AOS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

func textPtr(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

func TestLedgerService_AppendAndListRunLog(t *testing.T) {
	pool := ledgerTestDB(t)
	defer pool.Close()
	queries := db.New(pool)
	svc := NewLedgerService(queries)
	ctx := context.Background()

	// Clean up after test
	defer pool.Exec(ctx, "DELETE FROM run_log WHERE wp_ref = 'WP-O3-TEST'")

	row, err := svc.AppendRunLog(ctx, "dispatch", "#42", "WP-O3-TEST", textPtr("test summary"), []byte(`{"key":"val"}`))
	if err != nil {
		t.Fatalf("AppendRunLog: %v", err)
	}
	if row.EventType != "dispatch" {
		t.Fatalf("expected event_type dispatch, got %s", row.EventType)
	}
	if row.PrRef != "#42" {
		t.Fatalf("expected pr_ref #42, got %s", row.PrRef)
	}
	if row.WpRef != "WP-O3-TEST" {
		t.Fatalf("expected wp_ref WP-O3-TEST, got %s", row.WpRef)
	}

	rows, err := svc.ListRunLog(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListRunLog: %v", err)
	}
	if len(rows) < 1 {
		t.Fatalf("expected at least 1 run_log row, got %d", len(rows))
	}
	// Newest first
	if rows[0].WpRef != "WP-O3-TEST" {
		t.Fatalf("expected first row wp_ref WP-O3-TEST, got %s", rows[0].WpRef)
	}
}

func TestLedgerService_AppendAndListFindings(t *testing.T) {
	pool := ledgerTestDB(t)
	defer pool.Close()
	queries := db.New(pool)
	svc := NewLedgerService(queries)
	ctx := context.Background()

	class := "test-class-svc-" + time.Now().Format("20060102150405")
	defer pool.Exec(ctx, "DELETE FROM findings WHERE class = $1", class)

	_, err := svc.AppendFinding(ctx, "#42", "WP-O3-TEST", 1, "roux", "glm-5.1", "warning", class, textPtr("rc"), textPtr("sum"))
	if err != nil {
		t.Fatalf("AppendFinding: %v", err)
	}

	rows, err := svc.ListFindings(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(rows) < 1 {
		t.Fatalf("expected at least 1 finding, got %d", len(rows))
	}
}

// TestLedgerService_RecurringFindings_WpRefDiscrimination proves: recurrence groups by
// (class, author_agent, wp_ref), not just (class, author_agent).
//
// MUTATION GUARD: This test MUST FAIL if wp_ref is dropped from RecurringFindings GROUP BY.
// Data: (roux, cls, WP-A) ×2 and (roux, cls, WP-B) ×2, min_count=3.
// Correct grouping: each group has count=2, neither surfaces.
// Buggy grouping (class, author_agent): count=4 >= 3, surfaces → test FAILS.
func TestLedgerService_RecurringFindings_WpRefDiscrimination(t *testing.T) {
	pool := ledgerTestDB(t)
	defer pool.Close()
	queries := db.New(pool)
	svc := NewLedgerService(queries)
	ctx := context.Background()

	class := "svc-recur-wp-" + time.Now().Format("20060102150405")
	defer pool.Exec(ctx, "DELETE FROM findings WHERE class = $1", class)

	// Same agent, same class, two different wp_refs: 2 rows each (below threshold 3)
	for i := 0; i < 2; i++ {
		_, err := svc.AppendFinding(ctx, "#42", "WP-DISC-A", 1, "roux", "glm-5.1", "warning", class, textPtr("rc"), textPtr("sum"))
		if err != nil {
			t.Fatalf("AppendFinding WP-DISC-A %d: %v", i, err)
		}
		_, err = svc.AppendFinding(ctx, "#42", "WP-DISC-B", 1, "roux", "glm-5.1", "warning", class, textPtr("rc"), textPtr("sum"))
		if err != nil {
			t.Fatalf("AppendFinding WP-DISC-B %d: %v", i, err)
		}
	}

	// min_count=3 → neither should surface (each group has 2)
	rows, err := svc.RecurringFindings(ctx, 3)
	if err != nil {
		t.Fatalf("RecurringFindings(3): %v", err)
	}
	for _, r := range rows {
		if r.Class == class {
			t.Fatalf("expected class %s NOT to surface — split across wp_refs (2+2), each below threshold 3; got count=%d, wp_ref=%s", class, r.Count, r.WpRef)
		}
	}
}

// TestLedgerService_RecurringFindings_AgentDiscrimination proves: recurrence groups by
// (class, author_agent, wp_ref), not just (class, wp_ref).
//
// MUTATION GUARD: This test MUST FAIL if author_agent is dropped from RecurringFindings GROUP BY.
// Data: (agent-x, cls, WP-SHARED) ×2 and (agent-y, cls, WP-SHARED) ×2, min_count=3.
// Correct grouping: each group has count=2, neither surfaces.
// Buggy grouping (class, wp_ref): count=4 >= 3, surfaces → test FAILS.
func TestLedgerService_RecurringFindings_AgentDiscrimination(t *testing.T) {
	pool := ledgerTestDB(t)
	defer pool.Close()
	queries := db.New(pool)
	svc := NewLedgerService(queries)
	ctx := context.Background()

	class := "svc-recur-agent-" + time.Now().Format("20060102150405")
	defer pool.Exec(ctx, "DELETE FROM findings WHERE class = $1", class)

	// Two agents, same class, same wp_ref: 2 rows each (below threshold 3)
	for i := 0; i < 2; i++ {
		_, err := svc.AppendFinding(ctx, "#42", "WP-SHARED", 1, "agent-x", "glm-5.1", "warning", class, textPtr("rc"), textPtr("sum"))
		if err != nil {
			t.Fatalf("AppendFinding agent-x %d: %v", i, err)
		}
		_, err = svc.AppendFinding(ctx, "#42", "WP-SHARED", 1, "agent-y", "glm-5.1", "warning", class, textPtr("rc"), textPtr("sum"))
		if err != nil {
			t.Fatalf("AppendFinding agent-y %d: %v", i, err)
		}
	}

	// min_count=3 → neither should surface (each agent group has 2)
	rows, err := svc.RecurringFindings(ctx, 3)
	if err != nil {
		t.Fatalf("RecurringFindings(3): %v", err)
	}
	for _, r := range rows {
		if r.Class == class {
			t.Fatalf("expected class %s NOT to surface — split across agents (2+2), each below threshold 3; got count=%d, agent=%s", class, r.Count, r.AuthorAgent)
		}
	}
}

// TestLedgerService_RecurringFindings is a basic threshold test:
// 3 findings for same (class, agent, wp_ref) surface at min_count=3; 2 do not.
// NOTE: This test uses different agents AND wp_refs, so it only proves the >= threshold math.
// The grouping guarantees are proven by WpRefDiscrimination and AgentDiscrimination.
func TestLedgerService_RecurringFindings(t *testing.T) {
	pool := ledgerTestDB(t)
	defer pool.Close()
	queries := db.New(pool)
	svc := NewLedgerService(queries)
	ctx := context.Background()

	class := "recur-test-" + time.Now().Format("20060102150405")
	defer pool.Exec(ctx, "DELETE FROM findings WHERE class = $1", class)

	// Agent A, WP-O3: 3 findings → should surface at min_count=3
	for i := 0; i < 3; i++ {
		_, err := svc.AppendFinding(ctx, "#42", "WP-O3", 1, "agent-a", "glm-5.1", "warning", class, textPtr("rc"), textPtr("sum"))
		if err != nil {
			t.Fatalf("AppendFinding agent-a %d: %v", i, err)
		}
	}
	// Agent B, WP-O4: 2 findings of same class → should NOT surface at min_count=3
	for i := 0; i < 2; i++ {
		_, err := svc.AppendFinding(ctx, "#43", "WP-O4", 1, "agent-b", "glm-5.1", "warning", class, textPtr("rc"), textPtr("sum"))
		if err != nil {
			t.Fatalf("AppendFinding agent-b %d: %v", i, err)
		}
	}

	// min_count=3 → only (agent-a, WP-O3, class) should surface
	rows, err := svc.RecurringFindings(ctx, 3)
	if err != nil {
		t.Fatalf("RecurringFindings(3): %v", err)
	}
	var foundA, foundB bool
	for _, r := range rows {
		if r.Class == class && r.AuthorAgent == "agent-a" {
			foundA = true
			if r.WpRef != "WP-O3" {
				t.Fatalf("expected wp_ref WP-O3, got %s", r.WpRef)
			}
			if r.Count < 3 {
				t.Fatalf("expected count >= 3 for agent-a, got %d", r.Count)
			}
		}
		if r.Class == class && r.AuthorAgent == "agent-b" {
			foundB = true
		}
	}
	if !foundA {
		t.Fatal("expected (agent-a, WP-O3) to surface with count >= 3")
	}
	if foundB {
		t.Fatal("expected (agent-b, WP-O4) NOT to surface — only 2 rows, below threshold 3")
	}

	// min_count=4 → nothing should surface for this class
	rows4, err := svc.RecurringFindings(ctx, 4)
	if err != nil {
		t.Fatalf("RecurringFindings(4): %v", err)
	}
	for _, r := range rows4 {
		if r.Class == class {
			t.Fatal("expected class NOT to appear with min_count=4 (no group has 4 rows)")
		}
	}
}

func TestLedgerService_FilterByClass(t *testing.T) {
	pool := ledgerTestDB(t)
	defer pool.Close()
	queries := db.New(pool)
	svc := NewLedgerService(queries)
	ctx := context.Background()

	classA := "filter-a-" + time.Now().Format("20060102150405")
	classB := "filter-b-" + time.Now().Format("20060102150405")
	defer pool.Exec(ctx, "DELETE FROM findings WHERE class IN ($1, $2)", classA, classB)

	_, _ = svc.AppendFinding(ctx, "#42", "WP-O3", 1, "roux", "glm-5.1", "info", classA, textPtr(""), textPtr("a"))
	_, _ = svc.AppendFinding(ctx, "#42", "WP-O3", 1, "roux", "glm-5.1", "info", classA, textPtr(""), textPtr("a2"))
	_, _ = svc.AppendFinding(ctx, "#42", "WP-O3", 1, "roux", "glm-5.1", "info", classB, textPtr(""), textPtr("b"))

	rows, err := svc.ListFindingsByClass(ctx, classA, 10, 0)
	if err != nil {
		t.Fatalf("ListFindingsByClass: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 findings for classA, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Class != classA {
			t.Fatalf("expected class %s, got %s", classA, r.Class)
		}
	}
}

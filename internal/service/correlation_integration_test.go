package service

// Postgres integration test for the correlation SQL grouping. This is the test that
// actually proves the F3 no-drop guarantee and the ADR-002 tenant separation, by running
// the REAL queries against a real Postgres — the engine-level fake test cannot.
//
// Skips automatically unless AOS_TEST_DSN is set, so the unit suite stays hermetic:
//   AOS_TEST_DSN=postgres://test:test@localhost:55432/test?sslmode=disable go test ./internal/service/ -run Integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

func TestIntegration_CorrelationGrouping(t *testing.T) {
	dsn := os.Getenv("AOS_TEST_DSN")
	if dsn == "" {
		t.Skip("AOS_TEST_DSN not set — skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// clean slate
	if _, err := pool.Exec(ctx, "TRUNCATE work_events"); err != nil {
		t.Fatalf("truncate (is the schema migrated?): %v", err)
	}

	now := time.Now().UTC()
	// helper to insert a work_event with explicit (nullable) key parts
	ins := func(tenant, harness, session string, extRef, branch, sha *string, ts time.Time) {
		_, err := pool.Exec(ctx, `
			INSERT INTO work_events
			  (event_id, schema_version, harness, session_id, host, kind, tenant, external_ref, branch, sha, ts, received_at)
			VALUES (gen_random_uuid(), 'agentos.work_event/v1', $1, $2, 'h', 'session.start', $3, $4, $5, $6, $7, $7)`,
			harness, session, tenant, extRef, branch, sha, ts)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	s := func(v string) *string { return &v }

	// Group 1: fully-keyed, tenant=personal, 2 events same key (should collapse to 1 unit, count 2)
	ins("personal", "claude", "sess-1", s("SC-100"), s("wp-a/x"), s("aaa"), now.Add(-5*time.Minute))
	ins("personal", "claude", "sess-1", s("SC-100"), s("wp-a/x"), s("aaa"), now.Add(-4*time.Minute))
	// Cross-harness session-id collision: same session_id "sess-1" different harness → must be a
	// DISTINCT session in session_count, and a DIFFERENT unit (different key? same key here) —
	// here same key but different harness: still same unit, but session_count must count 2 sessions.
	ins("personal", "hermes", "sess-1", s("SC-100"), s("wp-a/x"), s("aaa"), now.Add(-3*time.Minute))

	// Group 2: SAME external_ref but DIFFERENT tenant → must NOT merge with group 1 (ADR-002).
	ins("dayjob", "claude", "sess-2", s("SC-100"), s("wp-a/x"), s("aaa"), now.Add(-2*time.Minute))

	// Group 3: partial key (branch only) → correlated=true (has an anchor)
	ins("personal", "agy", "sess-3", nil, s("wp-c/y"), nil, now.Add(-1*time.Minute))

	// Group 4: NO key at all → uncorrelated bucket, correlated=false
	ins("personal", "claude", "sess-4", nil, nil, nil, now)

	q := db.New(pool)
	eng := NewCorrelationEngine(q)

	units, err := eng.ListWorkUnits(ctx, "", 100, 0)
	if err != nil {
		t.Fatalf("ListWorkUnits: %v", err)
	}

	// --- F3 NO-DROP INVARIANT: sum of event_counts across all units == total rows inserted (6) ---
	var sum int64
	for _, u := range units {
		sum += u.EventCount
	}
	if sum != 6 {
		t.Fatalf("no-drop invariant violated: inserted 6 events but units account for %d", sum)
	}

	// --- tenant separation: group 1 (personal) and group 2 (dayjob) must be SEPARATE units ---
	var personalSC100, dayjobSC100 *WorkUnit
	var uncorrelated *WorkUnit
	for i := range units {
		u := &units[i]
		switch {
		case u.ExternalRef == "SC-100" && u.Tenant == "personal":
			personalSC100 = u
		case u.ExternalRef == "SC-100" && u.Tenant == "dayjob":
			dayjobSC100 = u
		case !u.Correlated:
			uncorrelated = u
		}
	}
	if personalSC100 == nil || dayjobSC100 == nil {
		t.Fatalf("tenant separation failed: same external_ref across tenants was merged or missing (personal=%v dayjob=%v)", personalSC100, dayjobSC100)
	}
	// personal SC-100 has 3 events (2 claude + 1 hermes) across 2 distinct (harness,session) pairs
	if personalSC100.EventCount != 3 {
		t.Errorf("personal SC-100 should have 3 events, got %d", personalSC100.EventCount)
	}
	if personalSC100.SessionCount != 2 {
		t.Errorf("cross-harness identity: personal SC-100 should count 2 distinct sessions (claude:sess-1, hermes:sess-1), got %d", personalSC100.SessionCount)
	}

	// --- uncorrelated bucket present, surfaced, correct count ---
	if uncorrelated == nil {
		t.Fatalf("uncorrelated bucket was DROPPED — F3 violation")
	}
	if uncorrelated.EventCount != 1 {
		t.Errorf("uncorrelated bucket should have 1 event, got %d", uncorrelated.EventCount)
	}

	// --- Count consistency with the page grouping ---
	total, err := eng.Count(ctx, "")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if int(total) != len(units) {
		t.Errorf("Count (%d) disagrees with grouped unit count (%d)", total, len(units))
	}
}

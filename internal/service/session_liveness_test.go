package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// getSessionLivenessTestDB returns a real pgxpool.Pool for session liveness tests.
func getSessionLivenessTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AOS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect to test DB: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("failed to ping test DB: %v", err)
	}
	return pool
}

// seedTestSession inserts work-events for a test session via raw SQL.
// Returns the unique suffix used for isolation.
func seedTestSession(t *testing.T, pool *pgxpool.Pool, opts sessionSeedOpts) {
	t.Helper()
	ctx := context.Background()
	if opts.Tenant == "" {
		opts.Tenant = "personal"
	}
	if opts.Harness == "" {
		opts.Harness = "claude"
	}
	if opts.Kind == "" {
		opts.Kind = "session.start"
	}
	if opts.Status == "" {
		opts.Status = "running"
	}
	if opts.LivenessMode == "" {
		opts.LivenessMode = "supervised"
	}
	if opts.ReceivedAt.IsZero() {
		opts.ReceivedAt = time.Now().UTC()
	}
	if opts.PID == 0 {
		opts.PID = 99991
	}

	// Use raw INSERT to bypass the ingest pipeline (tests the liveness derivation, not ingest).
	_, err := pool.Exec(ctx, `
		INSERT INTO work_events (
			event_id, schema_version, harness, session_id, host, pid,
			kind, status, liveness_mode, tenant, received_at, ts, payload
		) VALUES (
			$1, 'agentos.work_event/v1', $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11, '{}'
		) ON CONFLICT (event_id) DO NOTHING
	`,
		uuid.NewString(),           // event_id
		opts.Harness,                // harness
		opts.SessionID,              // session_id
		opts.Host,                   // host
		opts.PID,                    // pid
		opts.Kind,                   // kind
		opts.Status,                 // status
		opts.LivenessMode,           // liveness_mode
		opts.Tenant,                 // tenant
		opts.ReceivedAt,             // received_at
		opts.ReceivedAt,             // ts (same as received_at for tests)
	)
	if err != nil {
		t.Fatalf("seed test session event: %v", err)
	}
}

type sessionSeedOpts struct {
	Harness       string
	SessionID     string
	Host          string
	PID           int
	Kind          string
	Status        string
	LivenessMode  string
	Tenant        string
	ReceivedAt    time.Time
}

// ---------------------------------------------------------------------------
// AC tests: F10 — status DERIVED from events, never asserted
// ---------------------------------------------------------------------------

// TestSupervisedSession_WithRecentHeartbeat_ReturnsRunning proves:
// A real supervised session → running; heartbeats keep it live.
func TestSupervisedSession_WithRecentHeartbeat_ReturnsRunning(t *testing.T) {
	pool := getSessionLivenessTestDB(t)
	defer pool.Close()

	svc := NewSessionLivenessService(pool)
	sessionID := "fleet-test-" + uuid.NewString()[:8]
	suffix := sessionID

	// Seed session.start (just now).
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:    sessionID,
		Kind:         "session.start",
		Host:         "test-host",
		LivenessMode: "supervised",
		Tenant:       "personal",
		ReceivedAt:   time.Now().UTC().Add(-30 * time.Second),
	})
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			"DELETE FROM work_events WHERE session_id LIKE $1", "%"+suffix+"%")
	})

	// Seed a heartbeat 15 seconds ago (within 5m timeout).
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:    sessionID,
		Kind:         "session.heartbeat",
		Host:         "test-host",
		LivenessMode: "supervised",
		ReceivedAt:   time.Now().UTC().Add(-15 * time.Second),
	})

	fleet, err := svc.GetFleet(context.Background(), "personal")
	if err != nil {
		t.Fatalf("GetFleet: %v", err)
	}

	// Find our session in the fleet.
	var found *SessionStatus
	for i := range fleet.Sessions {
		if fleet.Sessions[i].SessionID == sessionID {
			s := fleet.Sessions[i]
			found = &s
			break
		}
	}
	if found == nil {
		t.Fatalf("session %s not found in fleet (got %d sessions)", sessionID, len(fleet.Sessions))
	}
	if found.Status != "running" {
		t.Fatalf("expected status 'running', got %q", found.Status)
	}
}

// TestSupervisedSession_KilledSupervisor_ReturnsStale proves:
// Killing its supervisor → stale after 5 min (proves clock timeout).
func TestSupervisedSession_KilledSupervisor_ReturnsStale(t *testing.T) {
	pool := getSessionLivenessTestDB(t)
	defer pool.Close()

	svc := NewSessionLivenessService(pool)
	sessionID := "fleet-stale-" + uuid.NewString()[:8]

	// Seed session.start 10 minutes ago (past 5m timeout) with no heartbeat since.
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:    sessionID,
		Kind:         "session.start",
		Host:         "test-host",
		LivenessMode: "supervised",
		ReceivedAt:   time.Now().UTC().Add(-10 * time.Minute),
	})
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			"DELETE FROM work_events WHERE session_id LIKE $1", "%"+sessionID+"%")
	})

	fleet, err := svc.GetFleet(context.Background(), "personal")
	if err != nil {
		t.Fatalf("GetFleet: %v", err)
	}

	var found *SessionStatus
	for i := range fleet.Sessions {
		if fleet.Sessions[i].SessionID == sessionID {
			s := fleet.Sessions[i]
			found = &s
			break
		}
	}
	if found == nil {
		t.Fatalf("session %s not found in fleet", sessionID)
	}
	if found.Status != "stale" {
		t.Fatalf("expected status 'stale' (supervisor killed, heartbeat expired), got %q", found.Status)
	}
}

// TestSessionEnd_ReturnsTerminalStatus proves:
// session.end → idle/done (terminal, absorbing).
func TestSessionEnd_ReturnsTerminalStatus(t *testing.T) {
	pool := getSessionLivenessTestDB(t)
	defer pool.Close()

	svc := NewSessionLivenessService(pool)
	sessionID := "fleet-done-" + uuid.NewString()[:8]

	// Seed session.start.
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:    sessionID,
		Kind:         "session.start",
		Host:         "test-host",
		LivenessMode: "supervised",
		ReceivedAt:   time.Now().UTC().Add(-1 * time.Minute),
	})
	// Seed session.end with status "done".
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:   sessionID,
		Kind:        "session.end",
		Status:      "done",
		ReceivedAt:  time.Now().UTC().Add(-10 * time.Second),
	})
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			"DELETE FROM work_events WHERE session_id LIKE $1", "%"+sessionID+"%")
	})

	fleet, err := svc.GetFleet(context.Background(), "personal")
	if err != nil {
		t.Fatalf("GetFleet: %v", err)
	}

	var found *SessionStatus
	for i := range fleet.Sessions {
		if fleet.Sessions[i].SessionID == sessionID {
			s := fleet.Sessions[i]
			found = &s
			break
		}
	}
	if found == nil {
		t.Fatalf("session %s not found in fleet", sessionID)
	}
	if found.Status != "done" {
		t.Fatalf("expected terminal status 'done', got %q (terminal must be absorbing)", found.Status)
	}
}

// TestBoundedSession_DegradesToStale proves:
// A bounded session degrades gracefully when WP-N host reporter is not merged.
// Bounded sessions without host reporter proof are stale (never running without proof).
func TestBoundedSession_DegradesToStale(t *testing.T) {
	pool := getSessionLivenessTestDB(t)
	defer pool.Close()

	svc := NewSessionLivenessService(pool)
	sessionID := "fleet-bounded-" + uuid.NewString()[:8]

	// Seed session.start for a bounded session just now.
	// Even though it's recent, without WP-N host reporter, it has no proof → stale.
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:    sessionID,
		Kind:         "session.start",
		Host:         "test-host",
		LivenessMode: "bounded",
		PID:          0,
		ReceivedAt:   time.Now().UTC().Add(-30 * time.Second),
	})
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			"DELETE FROM work_events WHERE session_id LIKE $1", "%"+sessionID+"%")
	})

	fleet, err := svc.GetFleet(context.Background(), "personal")
	if err != nil {
		t.Fatalf("GetFleet: %v", err)
	}

	var found *SessionStatus
	for i := range fleet.Sessions {
		if fleet.Sessions[i].SessionID == sessionID {
			s := fleet.Sessions[i]
			found = &s
			break
		}
	}
	if found == nil {
		t.Fatalf("session %s not found in fleet", sessionID)
	}
	// Bounded without host reporter proof → stale (never running without proof).
	if found.Status != "stale" {
		t.Fatalf("expected bounded session to be 'stale' (no host reporter proof), got %q", found.Status)
	}
}

// TestTerminalIsAbsorbing proves:
// A heartbeat after session.end does NOT change status back to running.
// Terminal is absorbing (contract §4).
func TestTerminalIsAbsorbing(t *testing.T) {
	pool := getSessionLivenessTestDB(t)
	defer pool.Close()

	svc := NewSessionLivenessService(pool)
	sessionID := "fleet-absorb-" + uuid.NewString()[:8]

	// Seed session.start.
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:    sessionID,
		Kind:         "session.start",
		Host:         "test-host",
		LivenessMode: "supervised",
		ReceivedAt:   time.Now().UTC().Add(-5 * time.Minute),
	})
	// Seed session.end.
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:  sessionID,
		Kind:       "session.end",
		Status:     "failed",
		ReceivedAt: time.Now().UTC().Add(-2 * time.Minute),
	})
	// Seed a late heartbeat AFTER session.end (should be ignored for liveness).
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:    sessionID,
		Kind:         "session.heartbeat",
		Host:         "test-host",
		LivenessMode: "supervised",
		ReceivedAt:   time.Now().UTC().Add(-10 * time.Second),
	})
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			"DELETE FROM work_events WHERE session_id LIKE $1", "%"+sessionID+"%")
	})

	fleet, err := svc.GetFleet(context.Background(), "personal")
	if err != nil {
		t.Fatalf("GetFleet: %v", err)
	}

	var found *SessionStatus
	for i := range fleet.Sessions {
		if fleet.Sessions[i].SessionID == sessionID {
			s := fleet.Sessions[i]
			found = &s
			break
		}
	}
	if found == nil {
		t.Fatalf("session %s not found in fleet", sessionID)
	}
	// Despite recent heartbeat, terminal state must absorb it.
	if found.Status != "failed" {
		t.Fatalf("terminal should be absorbing: expected 'failed', got %q", found.Status)
	}
}

// TestGetFleet_EmptyTenant_ReturnsError proves:
// GetFleet requires tenant (ADR-002).
func TestGetFleet_EmptyTenant_ReturnsError(t *testing.T) {
	pool := getSessionLivenessTestDB(t)
	defer pool.Close()

	svc := NewSessionLivenessService(pool)
	_, err := svc.GetFleet(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty tenant, got nil")
	}
}

// TestBoundedMaxAgeBackstop proves:
// A bounded session older than 6h is stale regardless of other factors.
func TestBoundedMaxAgeBackstop(t *testing.T) {
	pool := getSessionLivenessTestDB(t)
	defer pool.Close()

	svc := NewSessionLivenessService(pool)
	sessionID := "fleet-backstop-" + uuid.NewString()[:8]

	// Seed a bounded session.start 7 hours ago (past 6h backstop).
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:    sessionID,
		Kind:         "session.start",
		Host:         "test-host",
		LivenessMode: "bounded",
		PID:          0,
		ReceivedAt:   time.Now().UTC().Add(-7 * time.Hour),
	})
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			"DELETE FROM work_events WHERE session_id LIKE $1", "%"+sessionID+"%")
	})

	fleet, err := svc.GetFleet(context.Background(), "personal")
	if err != nil {
		t.Fatalf("GetFleet: %v", err)
	}

	var found *SessionStatus
	for i := range fleet.Sessions {
		if fleet.Sessions[i].SessionID == sessionID {
			s := fleet.Sessions[i]
			found = &s
			break
		}
	}
	if found == nil {
		t.Fatalf("session %s not found in fleet", sessionID)
	}
	if found.Status != "stale" {
		t.Fatalf("expected 'stale' (bounded_max_age backstop), got %q", found.Status)
	}
}

// TestTenantIsolation_FleetNeverLeaks proves:
// Sessions from one tenant never appear in another tenant's fleet.
func TestTenantIsolation_FleetNeverLeaks(t *testing.T) {
	pool := getSessionLivenessTestDB(t)
	defer pool.Close()

	svc := NewSessionLivenessService(pool)
	personalSession := "fleet-personal-" + uuid.NewString()[:8]
	dayjobSession := "fleet-dayjob-" + uuid.NewString()[:8]

	// Seed a "dayjob" session.
	seedTestSession(t, pool, sessionSeedOpts{
		SessionID:    dayjobSession,
		Kind:         "session.start",
		Host:         "work-laptop",
		LivenessMode: "supervised",
		Tenant:       "dayjob",
		ReceivedAt:   time.Now().UTC().Add(-15 * time.Second),
	})
	t.Cleanup(func() {
		pool.Exec(context.Background(),
			"DELETE FROM work_events WHERE session_id LIKE $1 OR session_id LIKE $2",
			"%"+personalSession+"%", "%"+dayjobSession+"%")
	})

	// Query fleet for "personal" — must NOT contain the dayjob session.
	fleet, err := svc.GetFleet(context.Background(), "personal")
	if err != nil {
		t.Fatalf("GetFleet: %v", err)
	}

	for _, s := range fleet.Sessions {
		if s.SessionID == dayjobSession {
			t.Fatalf("TENANT LEAK: dayjob session %s appeared in personal fleet (ADR-002 violation)", dayjobSession)
		}
		if s.Tenant != "personal" {
			t.Fatalf("TENANT LEAK: session with tenant=%q in personal fleet", s.Tenant)
		}
	}
}

// TestFleet_ReturnsAllHarnesses proves:
// Fleet returns sessions from all harnesses (claude, hermes, antigravity, etc.)
func TestFleet_ReturnsAllHarnesses(t *testing.T) {
	pool := getSessionLivenessTestDB(t)
	defer pool.Close()

	svc := NewSessionLivenessService(pool)
	tenant := "fleet-multi-" + uuid.NewString()[:8]

	sessions := []struct {
		harness string
		id      string
	}{
		{"claude", "fleet-claude-" + uuid.NewString()[:8]},
		{"hermes", "fleet-hermes-" + uuid.NewString()[:8]},
		{"antigravity", "fleet-agy-" + uuid.NewString()[:8]},
	}

	t.Cleanup(func() {
		for _, s := range sessions {
			pool.Exec(context.Background(),
				"DELETE FROM work_events WHERE session_id LIKE $1", "%"+s.id+"%")
		}
		pool.Exec(context.Background(),
			"DELETE FROM work_events WHERE tenant = $1", tenant)
	})

	// Seed a session.start for each harness.
	// Note: the "personal" tenant's ingest key is what's seeded by default.
	// These sessions use a custom tenant to avoid colliding with real data.
	// But since we don't have an ingest key for that tenant, we insert raw.
	for _, s := range sessions {
		seedTestSession(t, pool, sessionSeedOpts{
			Harness:       s.harness,
			SessionID:     s.id,
			Host:          "test-host",
			LivenessMode:  "supervised",
			Tenant:        tenant,
			ReceivedAt:    time.Now().UTC().Add(-15 * time.Second),
		})
	}

	fleet, err := svc.GetFleet(context.Background(), tenant)
	if err != nil {
		t.Fatalf("GetFleet: %v", err)
	}

	if fleet.Total != int64(len(sessions)) {
		t.Fatalf("expected %d sessions, got %d", len(sessions), fleet.Total)
	}

	// Verify each harness is present.
	harnesses := make(map[string]bool)
	for _, s := range fleet.Sessions {
		harnesses[s.Harness] = true
	}
	for _, s := range sessions {
		if !harnesses[s.harness] {
			t.Fatalf("harness %q not found in fleet", s.harness)
		}
	}
	_ = fmt.Sprintf // suppress unused import (test file)
}

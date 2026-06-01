package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tim4net/agent-os/internal/service"
)

// ---------------------------------------------------------------------------
// WP-M: Emitter Health API handler tests (real-PG httptest route tests)
// Tests liveness derivation as a PURE FUNCTION of (events, server clock now).
// Contract §4: running requires positive proof; absence of proof → stale.
// ---------------------------------------------------------------------------

// seedEmitterSession seeds a work_events session directly into the DB for
// emitter health tests. Returns the session_id used.
// Parameters:
//   - harness: the emitter harness name
//   - tenant: tenant slug (unique per test to avoid cross-contamination)
//   - kind: event kind (session.start, session.heartbeat, session.end)
//   - status: event status (running, done, failed, cancelled)
//   - livenessMode: supervised or bounded
//   - receivedAt: the received_at timestamp (server clock — the liveness clock)
func seedEmitterSession(t *testing.T, harness, tenant, kind, status, livenessMode string, receivedAt time.Time) string {
	t.Helper()
	ctx := t.Context()
	pool := getTestDB(t)

	sessionID := uuid.NewString()
	eventID := uuid.New()
	host := "testhost-" + harness

	pid := 0
	if livenessMode == "supervised" {
		pid = 12345
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		eventID, harness, sessionID, host, pid, kind, status, livenessMode, tenant, receivedAt.UTC(), receivedAt.UTC(),
	)
	if err != nil {
		t.Fatalf("seedEmitterSession: %v", err)
	}
	return sessionID
}

// emitterTestTenant returns a unique tenant name for emitter health tests.
func emitterTestTenant(t *testing.T, suffix string) string {
	t.Helper()
	return "test-emitter-" + suffix + "-" + uuid.NewString()[:8]
}

// TestHTTPEmitterHealth_SupervisedRunning proves AC:
// A supervised session with a recent heartbeat is "running".
func TestHTTPEmitterHealth_SupervisedRunning(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	tenant := emitterTestTenant(t, "sup-running")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	// Seed session.start 2 minutes ago + heartbeat 1 minute ago → within 5m window → running
	now := time.Now().UTC()
	sessID := uuid.NewString()
	ctx := t.Context()
	eid1 := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-claude', 12345, 'session.start', 'running', 'supervised', $3, $4, $5)`,
		eid1, sessID, tenant, now.Add(-2*time.Minute), now.Add(-2*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed start: %v", err)
	}
	eid2 := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-claude', 12345, 'session.heartbeat', 'running', 'supervised', $3, $4, $5)`,
		eid2, sessID, tenant, now.Add(-1*time.Minute), now.Add(-1*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed heartbeat: %v", err)
	}

	req := newTestGET("/?tenant=" + tenant + "&stale_window=5m")
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp service.EmitterHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Emitters) == 0 {
		t.Fatal("expected at least 1 emitter session")
	}

	// Find our session
	found := false
	for _, e := range resp.Emitters {
		if e.SessionID == sessID {
			found = true
			if e.Status != service.EmitterStatusRunning {
				t.Fatalf("expected status 'running', got %q", e.Status)
			}
			if e.LivenessMode != "supervised" {
				t.Fatalf("expected liveness_mode 'supervised', got %q", e.LivenessMode)
			}
		}
	}
	if !found {
		t.Fatalf("session %s not found in response", sessID)
	}
}

// TestHTTPEmitterHealth_SupervisedStale proves AC:
// Stop an emitter (no heartbeat for >5m) → shows "stale".
func TestHTTPEmitterHealth_SupervisedStale(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	tenant := emitterTestTenant(t, "sup-stale")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	now := time.Now().UTC()
	sessID := uuid.NewString()
	ctx := t.Context()

	// Seed session.start 10 minutes ago, last heartbeat 10 minutes ago → beyond 5m → stale
	eid1 := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-claude', 12345, 'session.start', 'running', 'supervised', $3, $4, $5)`,
		eid1, sessID, tenant, now.Add(-10*time.Minute), now.Add(-10*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed start: %v", err)
	}
	eid2 := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-claude', 12345, 'session.heartbeat', 'running', 'supervised', $3, $4, $5)`,
		eid2, sessID, tenant, now.Add(-10*time.Minute), now.Add(-10*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed heartbeat: %v", err)
	}

	req := newTestGET("/?tenant=" + tenant + "&stale_window=5m")
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp service.EmitterHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	found := false
	for _, e := range resp.Emitters {
		if e.SessionID == sessID {
			found = true
			if e.Status != service.EmitterStatusStale {
				t.Fatalf("AC FAIL: stopped emitter should be 'stale', got %q", e.Status)
			}
		}
	}
	if !found {
		t.Fatalf("session %s not found in response", sessID)
	}
}

// TestHTTPEmitterHealth_StaleToRunningTransition proves AC:
// Resume an emitter → heartbeat → becomes "running" again.
func TestHTTPEmitterHealth_StaleToRunningTransition(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	tenant := emitterTestTenant(t, "transition")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	now := time.Now().UTC()
	sessID := uuid.NewString()
	ctx := t.Context()

	// Phase 1: Session was active 10 min ago → stale
	eid1 := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'hermes', $2, 'testhost-hermes', 12345, 'session.start', 'running', 'supervised', $3, $4, $5)`,
		eid1, sessID, tenant, now.Add(-10*time.Minute), now.Add(-10*time.Minute),
	)
	if err != nil {
		t.Fatalf("phase1 seed: %v", err)
	}

	// Verify stale
	req1 := newTestGET("/?tenant=" + tenant + "&stale_window=5m")
	rec1 := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("phase1: expected 200, got %d; body: %s", rec1.Code, rec1.Body.String())
	}
	var resp1 service.EmitterHealthResponse
	if err := json.Unmarshal(rec1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("phase1: failed to parse response: %v", err)
	}
	foundPhase1 := false
	for _, e := range resp1.Emitters {
		if e.SessionID == sessID {
			foundPhase1 = true
			if e.Status != service.EmitterStatusStale {
				t.Fatalf("phase1: expected stale, got %q", e.Status)
			}
		}
	}
	if !foundPhase1 {
		t.Fatalf("phase1: session %s not found in response", sessID)
	}

	// Phase 2: Resume — fresh heartbeat within the window
	eid2 := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'hermes', $2, 'testhost-hermes', 12345, 'session.heartbeat', 'running', 'supervised', $3, $4, $5)`,
		eid2, sessID, tenant, now.Add(-30*time.Second), now.Add(-30*time.Second),
	)
	if err != nil {
		t.Fatalf("phase2 seed: %v", err)
	}

	// Verify running
	req2 := newTestGET("/?tenant=" + tenant + "&stale_window=5m")
	rec2 := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec2, req2)
	var resp2 service.EmitterHealthResponse
	json.Unmarshal(rec2.Body.Bytes(), &resp2)

	found := false
	for _, e := range resp2.Emitters {
		if e.SessionID == sessID {
			found = true
			if e.Status != service.EmitterStatusRunning {
				t.Fatalf("AC FAIL: resumed emitter should be 'running', got %q", e.Status)
			}
		}
	}
	if !found {
		t.Fatalf("session %s not found after transition", sessID)
	}
}

// TestHTTPEmitterHealth_TerminalAbsorbing proves contract §4:
// A session.end (terminal) is absorbing — later heartbeats don't resurrect.
func TestHTTPEmitterHealth_TerminalAbsorbing(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	tenant := emitterTestTenant(t, "absorbing")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	now := time.Now().UTC()
	sessID := uuid.NewString()
	ctx := t.Context()

	// Session start 5 min ago
	eid1 := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-claude', 12345, 'session.start', 'running', 'supervised', $3, $4, $5)`,
		eid1, sessID, tenant, now.Add(-5*time.Minute), now.Add(-5*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed start: %v", err)
	}

	// Terminal event 3 min ago
	eid2 := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-claude', 12345, 'session.end', 'done', 'supervised', $3, $4, $5)`,
		eid2, sessID, tenant, now.Add(-3*time.Minute), now.Add(-3*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed end: %v", err)
	}

	// Late heartbeat (inert — terminal is absorbing)
	eid3 := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-claude', 12345, 'session.heartbeat', 'running', 'supervised', $3, $4, $5)`,
		eid3, sessID, tenant, now.Add(-1*time.Minute), now.Add(-1*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed late heartbeat: %v", err)
	}

	req := newTestGET("/?tenant=" + tenant + "&stale_window=5m")
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	var resp service.EmitterHealthResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	for _, e := range resp.Emitters {
		if e.SessionID == sessID {
			// Must be "done" (terminal), NOT "running" (absorbing rule).
			if e.Status != service.EmitterStatusDone {
				t.Fatalf("terminal absorbing: expected 'done', got %q — late heartbeat resurrected session", e.Status)
			}
			return
		}
	}
	t.Fatal("session not found")
}

// TestHTTPEmitterHealth_OldEventIsStale proves AC:
// "Last Claude event 3 days ago = broken" surfaces as stale.
func TestHTTPEmitterHealth_OldEventIsStale(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	tenant := emitterTestTenant(t, "old-event")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	now := time.Now().UTC()
	sessID := uuid.NewString()
	ctx := t.Context()

	// Session with last event 3 days ago
	eid := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-claude', 12345, 'session.start', 'running', 'supervised', $3, $4, $5)`,
		eid, sessID, tenant, now.Add(-72*time.Hour), now.Add(-72*time.Hour),
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := newTestGET("/?tenant=" + tenant + "&stale_window=5m")
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	var resp service.EmitterHealthResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	found := false
	for _, e := range resp.Emitters {
		if e.SessionID == sessID {
			found = true
			if e.Status != service.EmitterStatusStale {
				t.Fatalf("AC FAIL: 3-day-old event should be 'stale', got %q", e.Status)
			}
		}
	}
	if !found {
		t.Fatal("session not found")
	}
}

// TestHTTPEmitterHealth_TenantIsolation proves:
// Sessions from tenant A don't appear in tenant B's results.
func TestHTTPEmitterHealth_TenantIsolation(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	tenantA := emitterTestTenant(t, "tenant-a")
	tenantB := emitterTestTenant(t, "tenant-b")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	now := time.Now().UTC()
	ctx := t.Context()

	// Seed tenant A session
	eidA := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-a', 12345, 'session.start', 'running', 'supervised', $3, $4, $5)`,
		eidA, uuid.NewString(), tenantA, now.Add(-1*time.Minute), now.Add(-1*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed A: %v", err)
	}

	// Seed tenant B session
	eidB := uuid.New()
	sessBID := uuid.NewString()
	_, err = pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'hermes', $2, 'testhost-b', 12345, 'session.start', 'running', 'supervised', $3, $4, $5)`,
		eidB, sessBID, tenantB, now.Add(-1*time.Minute), now.Add(-1*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed B: %v", err)
	}

	// Query tenant B only
	req := newTestGET("/?tenant=" + tenantB + "&stale_window=5m")
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	var resp service.EmitterHealthResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	for _, e := range resp.Emitters {
		if e.Harness == "claude" {
			t.Fatalf("tenant isolation violated: claude (tenant A) leaked into tenant B results")
		}
	}

	foundB := false
	for _, e := range resp.Emitters {
		if e.SessionID == sessBID {
			foundB = true
		}
	}
	if !foundB {
		t.Fatal("tenant B session not found in its own results")
	}
}

// TestHTTPEmitterHealth_EmptyTenantReturns400 proves ADR-002:
// GET /api/emitters with no tenant query parameter → 400.
func TestHTTPEmitterHealth_EmptyTenantReturns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	req := newTestGET("/")
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty tenant, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "tenant") {
		t.Fatalf("expected 'tenant' in error message, got: %s", resp["error"])
	}
}

// TestHTTPEmitterHealth_CrossTenantNeverLeaks proves ADR-002:
// Seeding two tenants and querying each one never returns the other tenant's sessions.
func TestHTTPEmitterHealth_CrossTenantNeverLeaks(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	tenantA := emitterTestTenant(t, "leak-a")
	tenantB := emitterTestTenant(t, "leak-b")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	now := time.Now().UTC()
	ctx := t.Context()

	// Seed two sessions for tenant A
	sessA1 := uuid.NewString()
	sessA2 := uuid.NewString()
	for _, sess := range []string{sessA1, sessA2} {
		eid := uuid.New()
		_, err := pool.Exec(ctx,
			`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
			 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-leak-a', 12345, 'session.start', 'running', 'supervised', $3, $4, $5)`,
			eid, sess, tenantA, now.Add(-1*time.Minute), now.Add(-1*time.Minute),
		)
		if err != nil {
			t.Fatalf("seed A: %v", err)
		}
	}

	// Seed one session for tenant B
	sessB1 := uuid.NewString()
	eidB := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'hermes', $2, 'testhost-leak-b', 12345, 'session.start', 'running', 'supervised', $3, $4, $5)`,
		eidB, sessB1, tenantB, now.Add(-1*time.Minute), now.Add(-1*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed B: %v", err)
	}

	// Query tenant A — must only see A sessions
	reqA := newTestGET("/?tenant=" + tenantA + "&stale_window=5m")
	recA := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(recA, reqA)
	var respA service.EmitterHealthResponse
	if err := json.Unmarshal(recA.Body.Bytes(), &respA); err != nil {
		t.Fatalf("parse A response: %v", err)
	}
	for _, e := range respA.Emitters {
		if e.SessionID == sessB1 {
			t.Fatal("tenant B session leaked into tenant A query")
		}
	}
	if len(respA.Emitters) < 2 {
		t.Fatalf("expected at least 2 sessions for tenant A, got %d", len(respA.Emitters))
	}

	// Query tenant B — must only see B sessions
	reqB := newTestGET("/?tenant=" + tenantB + "&stale_window=5m")
	recB := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(recB, reqB)
	var respB service.EmitterHealthResponse
	if err := json.Unmarshal(recB.Body.Bytes(), &respB); err != nil {
		t.Fatalf("parse B response: %v", err)
	}
	for _, e := range respB.Emitters {
		if e.SessionID == sessA1 || e.SessionID == sessA2 {
			t.Fatal("tenant A session leaked into tenant B query")
		}
	}
	if len(respB.Emitters) < 1 {
		t.Fatalf("expected at least 1 session for tenant B, got %d", len(respB.Emitters))
	}
}

// TestHTTPEmitterHealth_BoundedRecentIsRunning proves:
// A bounded session with recent events is "running" (no proof from reporter yet).
func TestHTTPEmitterHealth_BoundedRecentIsRunning(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	tenant := emitterTestTenant(t, "bounded-recent")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	now := time.Now().UTC()
	sessID := uuid.NewString()
	ctx := t.Context()

	eid := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-claude', NULL, 'session.start', 'running', 'bounded', $3, $4, $5)`,
		eid, sessID, tenant, now.Add(-2*time.Minute), now.Add(-2*time.Minute),
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := newTestGET("/?tenant=" + tenant + "&stale_window=5m")
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	var resp service.EmitterHealthResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	for _, e := range resp.Emitters {
		if e.SessionID == sessID {
			if e.Status != service.EmitterStatusRunning {
				t.Fatalf("bounded recent: expected 'running', got %q", e.Status)
			}
			return
		}
	}
	t.Fatal("session not found")
}

// TestHTTPEmitterHealth_BoundedOldIsStale proves:
// A bounded session with no events for >6h is "stale" (backstop).
func TestHTTPEmitterHealth_BoundedOldIsStale(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	tenant := emitterTestTenant(t, "bounded-old")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	now := time.Now().UTC()
	sessID := uuid.NewString()
	ctx := t.Context()

	eid := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO work_events (event_id, schema_version, harness, session_id, host, pid, kind, status, liveness_mode, tenant, ts, received_at)
		 VALUES ($1, 'agentos.work_event/v1', 'claude', $2, 'testhost-claude', NULL, 'session.start', 'running', 'bounded', $3, $4, $5)`,
		eid, sessID, tenant, now.Add(-7*time.Hour), now.Add(-7*time.Hour),
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := newTestGET("/?tenant=" + tenant + "&stale_window=5m")
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	var resp service.EmitterHealthResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	for _, e := range resp.Emitters {
		if e.SessionID == sessID {
			if e.Status != service.EmitterStatusStale {
				t.Fatalf("bounded old: expected 'stale', got %q", e.Status)
			}
			return
		}
	}
	t.Fatal("session not found")
}

// TestHTTPEmitterHealth_InvalidStaleWindow_Returns400 proves error handling.
func TestHTTPEmitterHealth_InvalidStaleWindow_Returns400(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	req := newTestGET("/?stale_window=not-a-duration")
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "invalid stale_window") {
		t.Fatalf("expected 'invalid stale_window' error, got: %s", resp["error"])
	}
}

// TestHTTPEmitterHealth_ResponseShape proves the response JSON shape.
func TestHTTPEmitterHealth_ResponseShape(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	tenant := emitterTestTenant(t, "shape")
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	req := newTestGET("/?tenant=" + tenant + "&limit=10&offset=0")
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp service.EmitterHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Verify pagination fields
	if resp.Limit != 10 {
		t.Fatalf("expected limit=10, got %d", resp.Limit)
	}
	if resp.Offset != 0 {
		t.Fatalf("expected offset=0, got %d", resp.Offset)
	}
	// Verify emitters is an array (not null)
	if resp.Emitters == nil {
		t.Fatal("emitters should be an array, got null")
	}
}

// TestHTTPEmitterHealth_EmptyResult proves:
// When no events exist for the tenant, returns empty array (not error).
func TestHTTPEmitterHealth_EmptyResult(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	req := newTestGET("/?tenant=test-emitter-nonexistent-" + uuid.NewString()[:8])
	rec := httptest.NewRecorder()
	a.EmitterRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp service.EmitterHealthResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Emitters) != 0 {
		t.Fatalf("expected 0 emitters for nonexistent tenant, got %d", len(resp.Emitters))
	}
	if resp.Total != 0 {
		t.Fatalf("expected total=0, got %d", resp.Total)
	}
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// getTestDB returns a real pgxpool.Pool for integration tests, or skips the test.
func getTestDB(t *testing.T) *pgxpool.Pool {
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

// newTestAPIWithDB creates a test API backed by a real database pool.
func newTestAPIWithDB(t *testing.T) (*API, *pgxpool.Pool, *service.EventBus) {
	t.Helper()
	pool := getTestDB(t)
	queries := db.New(pool)
	bus := service.NewEventBus()
	a := &API{
		queries: queries,
		bus:     bus,
	}
	return a, pool, bus
}

// testIngestRawKey is the raw key used across all integration tests.
// Its hash is seeded into ingest_keys during test setup so the
// DB-backed resolver (ResolveTenantFromKeyDB) recognizes it.
const testIngestRawKey = "test-key"

// seedTestIngestKey ensures the standard test ingest key exists in the DB
// (bound to tenant "personal") so that all handler tests using
// X-AgentOS-Ingest-Key: test-key resolve correctly through the WP-A2
// durable key store.
func seedTestIngestKey(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	keyHash := service.HashIngestKey(testIngestRawKey)
	// INSERT ... ON CONFLICT DO NOTHING so tests are idempotent.
	_, err := pool.Exec(ctx,
		`INSERT INTO ingest_keys (key_hash, tenant, label)
		 VALUES ($1, 'personal', 'integration-test')
		 ON CONFLICT (key_hash) DO NOTHING`,
		keyHash,
	)
	if err != nil {
		t.Fatalf("seed test ingest key: %v", err)
	}
}

// validWorkEventJSON returns a JSON-encoded valid WorkEventRequest.
func validWorkEventJSON(eventID string) string {
	pid := 12345
	req := map[string]interface{}{
		"schema":        "agentos.work_event/v1",
		"event_id":      eventID,
		"host":          "testhost",
		"harness":       "hermes",
		"kind":          "session.start",
		"session_id":    uuid.NewString(),
		"ts":            time.Now().Format(time.RFC3339),
		"status":        "running",
		"liveness_mode": "supervised",
		"pid":           pid,
		"tenant":        "personal",
	}
	b, _ := json.Marshal(req)
	return string(b)
}

// ---------------------------------------------------------------------------
// Blocking #1: HTTP handler tests for WorkEventRoutes
// ---------------------------------------------------------------------------

func TestHTTPWorkEvent_MissingIngestKey_Returns403(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	body := validWorkEventJSON(uuid.NewString())
	req := httptest.NewRequest("POST", "/work", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Deliberately omit X-AgentOS-Ingest-Key

	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "missing X-AgentOS-Ingest-Key header" {
		t.Fatalf("unexpected error message: %s", resp["error"])
	}
}

func TestHTTPWorkEvent_EmptyIngestKey_Returns403(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	body := validWorkEventJSON(uuid.NewString())
	req := httptest.NewRequest("POST", "/work", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentOS-Ingest-Key", "")

	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestHTTPWorkEvent_UnknownKeys_Returns400(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	body := `{"schema":"agentos.work_event/v1","event_id":"` + uuid.NewString() + `","host":"h","harness":"hermes","kind":"session.start","session_id":"` + uuid.NewString() + `","ts":"` + time.Now().UTC().Format(time.RFC3339) + `","status":"running","liveness_mode":"supervised","made_up_key":true}`

	req := httptest.NewRequest("POST", "/work", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentOS-Ingest-Key", "test-key")

	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "unknown top-level keys") {
		t.Fatalf("expected 'unknown top-level keys' error, got: %s", resp["error"])
	}
}

func TestHTTPWorkEvent_InvalidJSON_Returns400(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	req := httptest.NewRequest("POST", "/work", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentOS-Ingest-Key", "test-key")

	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "invalid JSON") {
		t.Fatalf("expected 'invalid JSON' error, got: %s", resp["error"])
	}
}

func TestHTTPWorkEvent_ValidEvent_Returns201AndDBRow(t *testing.T) {
	a, pool, bus := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	eventID := uuid.NewString()
	body := validWorkEventJSON(eventID)

	req := httptest.NewRequest("POST", "/work", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentOS-Ingest-Key", "test-key")

	sub := bus.Subscribe()
	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify response shape: {id, accepted: true}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if resp["accepted"] != true {
		t.Fatalf("expected accepted=true, got %v", resp["accepted"])
	}
	idStr, ok := resp["id"].(string)
	if !ok || idStr == "" {
		t.Fatalf("expected non-empty id string, got %v", resp["id"])
	}

	// Verify DB row exists
	ctx := context.Background()
	var pgEID pgtype.UUID
	_ = pgEID.Scan(eventID)
	row, err := a.queries.GetWorkEventByEventID(ctx, pgEID)
	if err != nil {
		t.Fatalf("failed to query work_event from DB: %v", err)
	}
	if row.Kind != "session.start" {
		t.Fatalf("expected kind session.start, got %s", row.Kind)
	}

	// Verify SSE event published
	select {
	case evt := <-sub:
		if evt.Type != "work_event" {
			t.Fatalf("expected SSE type work_event, got %q", evt.Type)
		}
	default:
		t.Fatal("expected SSE event on bus, got none")
	}
}

func TestHTTPWorkEvent_DuplicateEventID_Returns202(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	eventID := uuid.NewString()
	body := validWorkEventJSON(eventID)

	// First request → 201
	req1 := httptest.NewRequest("POST", "/work", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-AgentOS-Ingest-Key", "test-key")

	rec1 := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusCreated {
		t.Fatalf("first request: expected 201, got %d", rec1.Code)
	}

	var resp1 map[string]any
	json.Unmarshal(rec1.Body.Bytes(), &resp1)
	firstID := resp1["id"].(string)

	// Second request with same event_id → 202
	req2 := httptest.NewRequest("POST", "/work", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-AgentOS-Ingest-Key", "test-key")

	bus2 := service.NewEventBus()
	sub2 := bus2.Subscribe()
	a2 := &API{queries: a.queries, bus: bus2}

	rec2 := httptest.NewRecorder()
	a2.WorkEventRoutes().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusAccepted {
		t.Fatalf("duplicate request: expected 202, got %d; body: %s", rec2.Code, rec2.Body.String())
	}

	// Verify same id returned
	var resp2 map[string]any
	json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if resp2["id"].(string) != firstID {
		t.Fatalf("duplicate should return same id: %s vs %s", resp2["id"], firstID)
	}

	// Verify NO SSE for duplicate
	select {
	case evt := <-sub2:
		t.Fatalf("expected NO SSE event for duplicate, got type=%q", evt.Type)
	default:
		// good — no event
	}
}

func TestHTTPWorkEvent_ValidationError_Returns400(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	tests := []struct {
		name       string
		body       string
		wantSubstr string
	}{
		{
			name:       "missing event_id",
			body:       `{"schema":"agentos.work_event/v1","host":"h","harness":"hermes","kind":"session.start","session_id":"` + uuid.NewString() + `","ts":"` + time.Now().UTC().Format(time.RFC3339) + `","status":"running","liveness_mode":"supervised"}`,
			wantSubstr: "event_id is required",
		},
		{
			name:       "session.end with running status",
			body:       `{"schema":"agentos.work_event/v1","event_id":"` + uuid.NewString() + `","host":"h","harness":"hermes","kind":"session.end","session_id":"` + uuid.NewString() + `","ts":"` + time.Now().UTC().Format(time.RFC3339) + `","status":"running","liveness_mode":"supervised"}`,
			wantSubstr: "status for session.end must be one of",
		},
		{
			name:       "heartbeat with bounded liveness_mode",
			body:       `{"schema":"agentos.work_event/v1","event_id":"` + uuid.NewString() + `","host":"h","harness":"hermes","kind":"session.heartbeat","session_id":"` + uuid.NewString() + `","ts":"` + time.Now().UTC().Format(time.RFC3339) + `","status":"running","liveness_mode":"bounded"}`,
			wantSubstr: "session.heartbeat requires liveness_mode supervised",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/work", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-AgentOS-Ingest-Key", "test-key")

			rec := httptest.NewRecorder()
			a.WorkEventRoutes().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
			}
			var resp map[string]string
			json.Unmarshal(rec.Body.Bytes(), &resp)
			if !strings.Contains(resp["error"], tt.wantSubstr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantSubstr, resp["error"])
			}
		})
	}
}

func TestHTTPWorkEvent_TenantOverriddenByKey(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	eventID := uuid.NewString()
	pid := 12345
	req := map[string]interface{}{
		"schema":        "agentos.work_event/v1",
		"event_id":      eventID,
		"host":          "testhost",
		"harness":       "hermes",
		"kind":          "session.start",
		"session_id":    uuid.NewString(),
		"ts":            time.Now().Format(time.RFC3339),
		"status":        "running",
		"liveness_mode": "supervised",
		"pid":           pid,
		"tenant":        "dayjob", // Should be overridden by key → "personal"
	}
	body, _ := json.Marshal(req)

	httpReq := httptest.NewRequest("POST", "/work", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-AgentOS-Ingest-Key", "test-key")

	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, httpReq)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify the stored event has tenant="personal" (from key), not "dayjob" (from body)
	ctx := context.Background()
	var pgEID pgtype.UUID
	_ = pgEID.Scan(eventID)
	row, err := a.queries.GetWorkEventByEventID(ctx, pgEID)
	if err != nil {
		t.Fatalf("failed to query event: %v", err)
	}
	if row.Tenant != "personal" {
		t.Fatalf("tenant should be overridden to 'personal' by key, got %q", row.Tenant)
	}
}

func TestHTTPWorkEvent_WriteErrorLogsJSON(t *testing.T) {
	// Verify writeError produces JSON with "error" field (not http.Error which uses plain text).
	// AC3: errors are JSON-encoded AND logged, NEVER silently dropped.
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	seedTestIngestKey(t, pool)

	req := httptest.NewRequest("POST", "/work", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentOS-Ingest-Key", "test-key")

	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	// Verify the Content-Type is JSON (writeError sets it)
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("expected Content-Type application/json, got %s", ct)
	}

	// Verify response body is JSON with an "error" field
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v (body: %s)", err, rec.Body.String())
	}
	if resp["error"] == "" {
		t.Fatal("expected 'error' field in JSON response, got empty")
	}
}

// ---------------------------------------------------------------------------
// WP-A2 AC integration tests (real-PG, no mocks)
// ---------------------------------------------------------------------------

// TestIngestKey_TenantBinding_AC1 proves AC1: a key bound to tenant A cannot
// write events under tenant B. The server resolves tenant from the key, so
// the body tenant field is overridden.
func TestIngestKey_TenantBinding_AC1(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create a raw key bound to tenant "acme-corp".
	rawKey := "acme-key-" + uuid.NewString()[:8]
	keyHash := service.HashIngestKey(rawKey)
	var keyID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO ingest_keys (key_hash, tenant, label) VALUES ($1, 'acme-corp', 'ac1-test') RETURNING id`,
		keyHash,
	).Scan(&keyID)
	if err != nil {
		t.Fatalf("insert acme key: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM ingest_keys WHERE id = $1", keyID) })

	// POST an event with body tenant="other-tenant" using the acme key.
	eventID := uuid.NewString()
	body := fmt.Sprintf(`{
		"schema": "agentos.work_event/v1",
		"event_id": "%s",
		"host": "ac1-host",
		"harness": "hermes",
		"kind": "session.start",
		"session_id": "%s",
		"ts": "%s",
		"status": "running",
		"liveness_mode": "supervised",
		"tenant": "other-tenant"
	}`, eventID, uuid.NewString(), time.Now().UTC().Format(time.RFC3339))

	req := httptest.NewRequest("POST", "/work", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentOS-Ingest-Key", rawKey)

	rec := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify the persisted event has tenant="acme-corp" (from key), NOT "other-tenant" (from body).
	var pgEID pgtype.UUID
	_ = pgEID.Scan(eventID)
	row, err := a.queries.GetWorkEventByEventID(ctx, pgEID)
	if err != nil {
		t.Fatalf("failed to query event: %v", err)
	}
	if row.Tenant != "acme-corp" {
		t.Fatalf("AC1 VIOLATION: expected tenant 'acme-corp' (from key), got %q (body tenant should be overridden)", row.Tenant)
	}
}

// TestIngestKey_Revocation_AC2 proves AC2: an active key succeeds, but after
// revocation the same key returns 403.
func TestIngestKey_Revocation_AC2(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	ctx := context.Background()

	// Create a fresh key for this test.
	rawKey := "revoke-test-" + uuid.NewString()[:8]
	keyHash := service.HashIngestKey(rawKey)
	var keyID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO ingest_keys (key_hash, tenant, label) VALUES ($1, 'personal', 'ac2-test') RETURNING id`,
		keyHash,
	).Scan(&keyID)
	if err != nil {
		t.Fatalf("insert key: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM ingest_keys WHERE id = $1", keyID) })

	// Step 1: POST with the active key → 201.
	eventID1 := uuid.NewString()
	body1 := fmt.Sprintf(`{
		"schema": "agentos.work_event/v1",
		"event_id": "%s",
		"host": "ac2-host",
		"harness": "hermes",
		"kind": "session.start",
		"session_id": "%s",
		"ts": "%s",
		"status": "running",
		"liveness_mode": "supervised"
	}`, eventID1, uuid.NewString(), time.Now().UTC().Format(time.RFC3339))

	req1 := httptest.NewRequest("POST", "/work", strings.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-AgentOS-Ingest-Key", rawKey)

	rec1 := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("active key: expected 201, got %d; body: %s", rec1.Code, rec1.Body.String())
	}

	// Step 2: Revoke the key.
	tag, err := pool.Exec(ctx, "UPDATE ingest_keys SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL", keyID)
	if err != nil {
		t.Fatalf("revoke key: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row revoked, got %d", tag.RowsAffected())
	}

	// Step 3: POST again with the same (now revoked) key → 403.
	eventID2 := uuid.NewString()
	body2 := fmt.Sprintf(`{
		"schema": "agentos.work_event/v1",
		"event_id": "%s",
		"host": "ac2-host",
		"harness": "hermes",
		"kind": "session.end",
		"session_id": "%s",
		"ts": "%s",
		"status": "done"
	}`, eventID2, uuid.NewString(), time.Now().UTC().Format(time.RFC3339))

	req2 := httptest.NewRequest("POST", "/work", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-AgentOS-Ingest-Key", rawKey)

	rec2 := httptest.NewRecorder()
	a.WorkEventRoutes().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("revoked key: expected 403, got %d; body: %s", rec2.Code, rec2.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(rec2.Body.Bytes(), &resp)
	if resp["error"] != "invalid ingest key" {
		t.Fatalf("expected 'invalid ingest key', got %q", resp["error"])
	}
}

// Suppress unused import warnings
var _ = slog.Default

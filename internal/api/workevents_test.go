package api

import (
	"bytes"
	"context"
	"encoding/json"
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

	body := `{"schema":"agentos.work_event/v1","event_id":"` + uuid.NewString() + `","host":"h","harness":"hermes","kind":"session.start","session_id":"` + uuid.NewString() + `","ts":"2026-05-30T12:00:00Z","status":"running","liveness_mode":"supervised","made_up_key":true}`

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

	tests := []struct {
		name       string
		body       string
		wantSubstr string
	}{
		{
			name:       "missing event_id",
			body:       `{"schema":"agentos.work_event/v1","host":"h","harness":"hermes","kind":"session.start","session_id":"` + uuid.NewString() + `","ts":"2026-05-30T12:00:00Z","status":"running","liveness_mode":"supervised"}`,
			wantSubstr: "event_id is required",
		},
		{
			name:       "session.end with running status",
			body:       `{"schema":"agentos.work_event/v1","event_id":"` + uuid.NewString() + `","host":"h","harness":"hermes","kind":"session.end","session_id":"` + uuid.NewString() + `","ts":"2026-05-30T12:00:00Z","status":"running","liveness_mode":"supervised"}`,
			wantSubstr: "status for session.end must be one of",
		},
		{
			name:       "heartbeat with bounded liveness_mode",
			body:       `{"schema":"agentos.work_event/v1","event_id":"` + uuid.NewString() + `","host":"h","harness":"hermes","kind":"session.heartbeat","session_id":"` + uuid.NewString() + `","ts":"2026-05-30T12:00:00Z","status":"running","liveness_mode":"bounded"}`,
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

// Suppress unused import warnings
var _ = slog.Default

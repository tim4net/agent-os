package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// ---------------------------------------------------------------------------
// Blocking #2: HTTP handler tests for DelegationRoutes (shim exercise)
// Tests that the ACTUAL CreateDelegation/UpdateDelegationStatus handlers
// invoke synthesizeWorkEvent and produce work_events rows via the bridge.
// ---------------------------------------------------------------------------

func TestHTTPDelegation_POSTSynthesizesWorkEvent(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	parentID := uuid.NewString()
	seedTestAgent(t, pool, parentID)
	taskGoal := "do the thing"
	payload := map[string]string{
		"parent_agent_id":  parentID,
		"child_agent_name": "test-child",
		"task_goal":        taskGoal,
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	a.DelegationRoutes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify delegation response
	var delegResp DelegationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &delegResp); err != nil {
		t.Fatalf("failed to parse delegation response: %v", err)
	}
	if delegResp.Status != "pending" {
		t.Fatalf("expected delegation status 'pending', got %q", delegResp.Status)
	}
	if delegResp.ID == "" {
		t.Fatal("expected non-empty delegation ID")
	}

	// Verify a work_event was synthesized by the shim (bridge event).
	// The bridge event_id is deterministic: UUIDv5(delegation_id + ":" + kind).
	ctx := context.Background()
	deg := makeDelegationFromResp(delegResp, parentID, taskGoal, "pending")
	bridgeReq := service.BuildBridgeWorkEventRequest(deg, "")
	var bridgeEID pgtype.UUID
	_ = bridgeEID.Scan(bridgeReq.EventID)

	row, err := a.queries.GetWorkEventByEventID(ctx, bridgeEID)
	if err != nil {
		t.Fatalf("bridge work_event not found in DB (expected session.start from shim): %v", err)
	}
	if row.Kind != "session.start" {
		t.Fatalf("expected bridge event kind 'session.start', got %q", row.Kind)
	}
	if row.Status.String != "running" {
		t.Fatalf("expected bridge event status 'running', got %q", row.Status.String)
	}
	if row.Host != "bridge" {
		t.Fatalf("expected bridge event host 'bridge', got %q", row.Host)
	}
}

func TestHTTPDelegation_PATCHTerminalSynthesizesSessionEnd(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	parentID := uuid.NewString()
	taskGoal := "complete this task"

	// Seed the parent agent so the FK constraint is satisfied.
	seedTestAgent(t, pool, parentID)

	// Step 1: POST to create delegation
	postBody := map[string]string{
		"parent_agent_id":  parentID,
		"child_agent_name": "test-child",
		"task_goal":        taskGoal,
	}
	postJSON, _ := json.Marshal(postBody)
	postReq := httptest.NewRequest("POST", "/", bytes.NewReader(postJSON))
	postReq.Header.Set("Content-Type", "application/json")

	postRec := httptest.NewRecorder()
	a.DelegationRoutes().ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST: expected 200, got %d", postRec.Code)
	}

	var delegResp DelegationResponse
	json.Unmarshal(postRec.Body.Bytes(), &delegResp)
	degID := delegResp.ID

	// Step 2: PATCH to terminal status (completed)
	patchBody := map[string]string{
		"status":         "completed",
		"result_summary": "task completed successfully",
	}
	patchJSON, _ := json.Marshal(patchBody)
	patchReq := httptest.NewRequest("PATCH", "/"+degID, bytes.NewReader(patchJSON))
	patchReq.Header.Set("Content-Type", "application/json")

	patchRec := httptest.NewRecorder()
	a.DelegationRoutes().ServeHTTP(patchRec, patchReq)

	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH: expected 200, got %d; body: %s", patchRec.Code, patchRec.Body.String())
	}

	// Verify session.end bridge event was created
	ctx := context.Background()
	deg := makeDelegationFromResp(delegResp, parentID, taskGoal, "completed")
	endBridgeReq := service.BuildBridgeWorkEventRequest(deg, "session.end")
	var endEID pgtype.UUID
	_ = endEID.Scan(endBridgeReq.EventID)

	row, err := a.queries.GetWorkEventByEventID(ctx, endEID)
	if err != nil {
		t.Fatalf("session.end bridge work_event not found in DB after PATCH: %v", err)
	}
	if row.Kind != "session.end" {
		t.Fatalf("expected session.end kind, got %q", row.Kind)
	}
	if row.Status.String != "done" {
		t.Fatalf("expected status 'done', got %q", row.Status.String)
	}
}

func TestHTTPDelegation_PATCHNonTerminalNoSynthesis(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	parentID := uuid.NewString()
	taskGoal := "do the thing"

	// Seed the parent agent so the FK constraint is satisfied.
	seedTestAgent(t, pool, parentID)

	// POST to create
	postBody := map[string]string{
		"parent_agent_id":  parentID,
		"child_agent_name": "test-child",
		"task_goal":        taskGoal,
	}
	postJSON, _ := json.Marshal(postBody)
	postReq := httptest.NewRequest("POST", "/", bytes.NewReader(postJSON))
	postReq.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	a.DelegationRoutes().ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST: expected 200, got %d", postRec.Code)
	}

	var delegResp DelegationResponse
	json.Unmarshal(postRec.Body.Bytes(), &delegResp)

	// PATCH to non-terminal status (running)
	patchBody := map[string]string{"status": "running"}
	patchJSON, _ := json.Marshal(patchBody)
	patchReq := httptest.NewRequest("PATCH", "/"+delegResp.ID, bytes.NewReader(patchJSON))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	a.DelegationRoutes().ServeHTTP(patchRec, patchReq)

	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH: expected 200, got %d; body: %s", patchRec.Code, patchRec.Body.String())
	}

	// Verify no session.end bridge event was created
	ctx := context.Background()
	deg := makeDelegationFromResp(delegResp, parentID, taskGoal, "running")
	endBridgeReq := service.BuildBridgeWorkEventRequest(deg, "session.end")
	var endEID pgtype.UUID
	_ = endEID.Scan(endBridgeReq.EventID)

	_, err := a.queries.GetWorkEventByEventID(ctx, endEID)
	if err == nil {
		t.Fatal("expected NO session.end bridge event for non-terminal PATCH, but found one")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeDelegationFromResp builds a db.Delegation from a DelegationResponse for
// computing the deterministic bridge event_id via BuildBridgeWorkEventRequest.
func makeDelegationFromResp(resp DelegationResponse, parentID, taskGoal, status string) db.Delegation {
	var pgID, pgParent pgtype.UUID
	_ = pgID.Scan(resp.ID)
	_ = pgParent.Scan(parentID)

	createdAt := parseTimestamp(resp.CreatedAt)
	return db.Delegation{
		ID:            pgID,
		ParentAgentID: pgParent,
		ChildAgentName: resp.ChildAgentName,
		TaskGoal:      taskGoal,
		Status:        status,
		CreatedAt:     createdAt,
	}
}

func parseTimestamp(s string) pgtype.Timestamptz {
	if s == "" {
		return pgtype.Timestamptz{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Try alternative format
		t, err = time.Parse("2006-01-02T15:04:05Z07:00", s)
		if err != nil {
			return pgtype.Timestamptz{}
		}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// seedTestAgent inserts a minimal agents row with the given ID so the delegations
// FK constraint is satisfied. Uses the pool directly because CreateAgent
// auto-generates IDs (we need a deterministic one).
func seedTestAgent(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		"INSERT INTO agents (id, name, display_name, harness, base_url) VALUES ($1, $2, $3, $4, $5)",
		id, "test-parent-"+id[:8], "Test Parent Agent", "test", "http://localhost",
	)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

// Suppress unused import warnings
var _ = context.Background
var _ = (*http.Request)(nil)
var _ = service.BuildBridgeWorkEventRequest
var _ = json.Marshal

package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// errFakeDB is a sentinel error for simulating database failures in tests.
var errFakeDB = errors.New("fake db error")

// fakeOwnerID is the owner-0 UUID used in workevent ingest tests.
var fakeOwnerID = pgtype.UUID{Bytes: [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, Valid: true}

// ---------------------------------------------------------------------------
// fakeQuerier — minimal in-memory implementation of db.Querier for testing
// Ingest() without a real PostgreSQL instance. Embeds the interface so
// unimplemented methods return a harmless sentinel; only the methods used
// by Ingest() (InsertWorkEvent, GetWorkEventByEventID, EnsureProjectBySlug)
// are overridden with real test logic.
// ---------------------------------------------------------------------------

type fakeQuerier struct {
	db.Querier // embed for unimplemented methods
	mu         sync.Mutex
	events     map[string]db.WorkEvent // keyed by event_id string
	inserts    int                    // number of successful (non-conflict) inserts
	insertErr  error                  // optional: force InsertWorkEvent to return this error
	getErr     error                 // optional: force GetWorkEventByEventID to return this error
	projectErr error                 // optional: force EnsureProjectBySlug to return this error
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		events: make(map[string]db.WorkEvent),
	}
}

func (fq *fakeQuerier) InsertWorkEvent(_ context.Context, arg db.InsertWorkEventParams) (db.WorkEvent, error) {
	fq.mu.Lock()
	defer fq.mu.Unlock()

	if fq.insertErr != nil {
		return db.WorkEvent{}, fq.insertErr
	}

	eid := arg.EventID.String()
	if _, exists := fq.events[eid]; exists {
		// Simulate ON CONFLICT DO NOTHING → pgx.ErrNoRows
		return db.WorkEvent{}, pgx.ErrNoRows
	}

	// Simulate successful insert
	now := time.Now()
	row := db.WorkEvent{
		ID:            mustUUID(uuid.New()),
		EventID:       arg.EventID,
		SchemaVersion: arg.SchemaVersion,
		Harness:       arg.Harness,
		SessionID:     arg.SessionID,
		Host:          arg.Host,
		Pid:           arg.Pid,
		Kind:          arg.Kind,
		Status:        arg.Status,
		LivenessMode:  arg.LivenessMode,
		ProjectID:     arg.ProjectID,
		Tenant:        arg.Tenant,
		ExternalRef:   arg.ExternalRef,
		Branch:        arg.Branch,
		Sha:           arg.Sha,
		Cwd:           arg.Cwd,
		Title:         arg.Title,
		CostUsd:       arg.CostUsd,
		Payload:       arg.Payload,
		Ts:            arg.Ts,
		ReceivedAt:    pgtype.Timestamptz{Time: now, Valid: true},
	}
	fq.events[eid] = row
	fq.inserts++
	return row, nil
}

func (fq *fakeQuerier) GetWorkEventByEventID(_ context.Context, arg db.GetWorkEventByEventIDParams) (db.WorkEvent, error) {
	fq.mu.Lock()
	defer fq.mu.Unlock()

	if fq.getErr != nil {
		return db.WorkEvent{}, fq.getErr
	}

	if row, ok := fq.events[arg.EventID.String()]; ok {
		return row, nil
	}
	return db.WorkEvent{}, pgx.ErrNoRows
}

func (fq *fakeQuerier) GetWorkEventsBySession(_ context.Context, _ db.GetWorkEventsBySessionParams) ([]db.WorkEvent, error) {
	fq.mu.Lock()
	defer fq.mu.Unlock()
	var out []db.WorkEvent
	for _, e := range fq.events {
		out = append(out, e)
	}
	return out, nil
}

func (fq *fakeQuerier) EnsureProjectBySlug(_ context.Context, arg db.EnsureProjectBySlugParams) (db.Project, error) {
	if fq.projectErr != nil {
		return db.Project{}, fq.projectErr
	}
	return db.Project{
		ID:     mustUUID(uuid.New()),
		Slug:   arg.Slug,
		Name:   arg.Name,
		Tenant: arg.Tenant,
	}, nil
}

// mustUUID converts a UUID to pgtype.UUID (panics on error — test-only).
func mustUUID(u uuid.UUID) pgtype.UUID {
	var p pgtype.UUID
	_ = p.Scan(u.String())
	return p
}

// ---------------------------------------------------------------------------
// fakeQuerier runtime tests (findings #1 and #2)
// ---------------------------------------------------------------------------

// --- Finding #1 core: Ingest() with fake querier ---

// TestFakeIngestNewEvent proves: valid event → 201 + exactly one insert + SSE published.
func TestFakeIngestNewEvent(t *testing.T) {
	fq := newFakeQuerier()
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	eventID := uuid.NewString()
	req := validFakeRequest(eventID)

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	row, status, err := svc.Ingest(context.Background(), req, fakeOwnerID)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}
	if status != 201 {
		t.Fatalf("expected 201, got %d", status)
	}
	if row.Kind != "session.start" {
		t.Fatalf("expected kind session.start, got %s", row.Kind)
	}
	if row.Harness != "hermes" {
		t.Fatalf("expected harness hermes, got %s", row.Harness)
	}
	if fq.inserts != 1 {
		t.Fatalf("expected exactly 1 insert, got %d", fq.inserts)
	}

	// Verify SSE
	select {
	case evt := <-sub:
		if evt.Type != "work_event" {
			t.Fatalf("expected SSE type work_event, got %q", evt.Type)
		}
		if eid, ok := evt.Payload["event_id"].(string); !ok || !strings.Contains(eid, eventID) {
			t.Fatalf("SSE payload event_id mismatch: %v", evt.Payload["event_id"])
		}
	default:
		t.Fatal("expected SSE event, got none")
	}
}

// TestFakeIngestIdempotency proves: duplicate event_id → 202 + original id + zero new inserts + no SSE.
func TestFakeIngestIdempotency(t *testing.T) {
	fq := newFakeQuerier()
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	eventID := uuid.NewString()
	req := validFakeRequest(eventID)

	// First insert → 201
	row1, s1, err := svc.Ingest(context.Background(), req, fakeOwnerID)
	if err != nil || s1 != 201 {
		t.Fatalf("first insert: status=%d err=%v", s1, err)
	}

	// Second insert with same event_id → 202 (share same fake, so conflict triggers).
	// Use a fresh bus to detect SSE (should NOT publish for duplicate).
	bus2 := NewEventBus()
	svc2 := NewIngestService(fq, bus2, slog.Default(), "/tmp/test")
	sub2 := bus2.Subscribe()
	defer bus2.Unsubscribe(sub2)

	row2, s2, err := svc2.Ingest(context.Background(), req, fakeOwnerID)
	if err != nil {
		t.Fatalf("second insert error: %v", err)
	}
	if s2 != 202 {
		t.Fatalf("expected 202, got %d", s2)
	}
	if row2.ID.String() != row1.ID.String() {
		t.Fatalf("second insert should return original id: got %s, want %s", row2.ID.String(), row1.ID.String())
	}
	if fq.inserts != 1 {
		t.Fatalf("expected still 1 insert after duplicate, got %d", fq.inserts)
	}

	// Verify NO SSE published for duplicate
	select {
	case evt := <-sub2:
		t.Fatalf("expected NO SSE event for duplicate, but got type=%q", evt.Type)
	default:
		// good — no event
	}
}

// TestFakeIngestUncorrelatable proves: well-formed event without external_ref/branch is persisted (not rejected).
func TestFakeIngestUncorrelatable(t *testing.T) {
	fq := newFakeQuerier()
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	eventID := uuid.NewString()
	req := validFakeRequest(eventID)
	req.ExternalRef = ""
	req.Branch = ""
	req.Sha = ""
	req.Kind = "note"
	req.Status = ""
	req.LivenessMode = ""
	req.Pid = nil

	row, status, err := svc.Ingest(context.Background(), req, fakeOwnerID)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}
	if status != 201 {
		t.Fatalf("expected 201, got %d", status)
	}
	if row.Kind != "note" {
		t.Fatalf("expected kind note, got %s", row.Kind)
	}
	if fq.inserts != 1 {
		t.Fatalf("expected 1 insert, got %d", fq.inserts)
	}
}

// TestFakeIngestProjectResolution proves: project_hint resolves via EnsureProjectBySlug.
func TestFakeIngestProjectResolution(t *testing.T) {
	fq := newFakeQuerier()
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	eventID := uuid.NewString()
	req := validFakeRequest(eventID)
	req.ProjectHint = "agent-os"

	row, status, err := svc.Ingest(context.Background(), req, fakeOwnerID)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}
	if status != 201 {
		t.Fatalf("expected 201, got %d", status)
	}
	if !row.ProjectID.Valid {
		t.Fatal("expected project_id to be set")
	}
}

// --- Finding #1 regression: Ingest() db error handling ---

// TestFakeIngestInsertError proves: a real DB error during insert → 500, not masked.
func TestFakeIngestInsertError(t *testing.T) {
	fq := newFakeQuerier()
	fq.insertErr = errFakeDB
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	eventID := uuid.NewString()
	req := validFakeRequest(eventID)

	_, status, err := svc.Ingest(context.Background(), req, fakeOwnerID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if status != 500 {
		t.Fatalf("expected 500 for DB error, got %d", status)
	}
	if !strings.Contains(err.Error(), "db insert failed") {
		t.Fatalf("expected 'db insert failed' error, got: %v", err)
	}
}

// TestFakeIngestConflictFetchError proves: pgx.ErrNoRows on conflict, then real error on fetch → 500.
func TestFakeIngestConflictFetchError(t *testing.T) {
	fq := newFakeQuerier()
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	eventID := uuid.NewString()
	req := validFakeRequest(eventID)

	// First insert succeeds
	_, s1, err := svc.Ingest(context.Background(), req, fakeOwnerID)
	if err != nil || s1 != 201 {
		t.Fatalf("first insert: status=%d err=%v", s1, err)
	}

	// Now make the fetch on conflict fail
	fq.getErr = errFakeDB

	_, s2, err := svc.Ingest(context.Background(), req, fakeOwnerID)
	if err == nil {
		t.Fatal("expected error on conflict fetch, got nil")
	}
	if s2 != 500 {
		t.Fatalf("expected 500 on conflict fetch failure, got %d", s2)
	}
	if !strings.Contains(err.Error(), "fetch existing event") {
		t.Fatalf("expected 'fetch existing event' error, got: %v", err)
	}
}

// --- Finding #2 regression: resolveProject error surface ---

// TestFakeIngestProjectResolutionError proves: EnsureProjectBySlug failure → 500, not swallowed.
func TestFakeIngestProjectResolutionError(t *testing.T) {
	fq := newFakeQuerier()
	fq.projectErr = errFakeDB
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	eventID := uuid.NewString()
	req := validFakeRequest(eventID)
	req.ProjectHint = "agent-os"

	_, status, err := svc.Ingest(context.Background(), req, fakeOwnerID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if status != 500 {
		t.Fatalf("expected 500 for project resolution failure, got %d", status)
	}
	if !strings.Contains(err.Error(), "project resolution") {
		t.Fatalf("expected 'project resolution' error, got: %v", err)
	}
}

// --- Finding #2 regression: validation errors still bubble correctly through Ingest ---

// TestFakeIngestValidationRejectsBadEvents proves: validation errors return 400 through Ingest path.
func TestFakeIngestValidationRejectsBadEvents(t *testing.T) {
	fq := newFakeQuerier()
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	tests := []struct {
		name    string
		mutate  func(req *WorkEventRequest)
		wantSts int
		wantSub string
	}{
		{
			name:    "missing event_id",
			mutate:  func(req *WorkEventRequest) { req.EventID = "" },
			wantSts: 400,
			wantSub: "event_id is required",
		},
		{
			name:    "session.end with running status",
			mutate:  func(req *WorkEventRequest) { req.Kind = "session.end"; req.Status = "running" },
			wantSts: 400,
			wantSub: "status for session.end must be one of",
		},
		{
			name:    "heartbeat without supervised liveness_mode",
			mutate:  func(req *WorkEventRequest) { req.Kind = "session.heartbeat"; req.LivenessMode = "bounded" },
			wantSts: 400,
			wantSub: "session.heartbeat requires liveness_mode supervised",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validFakeRequest(uuid.NewString())
			tt.mutate(&req)
			_, httpStatus, err := svc.Ingest(context.Background(), req, fakeOwnerID)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if httpStatus != tt.wantSts {
				t.Fatalf("expected HTTP %d, got %d", tt.wantSts, httpStatus)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantSub, err)
			}
			if _, ok := err.(*ValidationError); !ok {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
		})
	}
}

// --- Bridge (delegation shim) regression tests ---
// Tests the BuildBridgeWorkEventRequest logic and its integration with Ingest.

// makeFakeDelegation creates a db.Delegation for testing bridge synthesis.
func makeFakeDelegation(id, parentID, taskGoal, status string) db.Delegation {
	var pgID, pgParent pgtype.UUID
	_ = pgID.Scan(id)
	_ = pgParent.Scan(parentID)
	return db.Delegation{
		ID:            pgID,
		ParentAgentID: pgParent,
		ChildAgentName: "test-child",
		TaskGoal:      taskGoal,
		Status:        status,
		CreatedAt:     pgtype.Timestamptz{Time: time.Now().Add(-30 * time.Minute), Valid: true},
	}
}

// TestBridgeEventIDIsDeterministic proves: same delegation_id + kind always produces the same event_id.
// This is the regression guard for finding #3 (bridge idempotency via UUIDv5).
func TestBridgeEventIDIsDeterministic(t *testing.T) {
	deg := makeFakeDelegation(
		uuid.NewString(), uuid.NewString(), "test task", "running",
	)

	req1 := BuildBridgeWorkEventRequest(deg, "")
	req2 := BuildBridgeWorkEventRequest(deg, "")

	if req1.EventID != req2.EventID {
		t.Fatalf("bridge event_id not deterministic: %s vs %s", req1.EventID, req2.EventID)
	}

	// Also verify different kinds produce different event_ids for the same delegation
	req3 := BuildBridgeWorkEventRequest(deg, "session.end")
	if req1.EventID == req3.EventID {
		t.Fatalf("bridge event_id should differ by kind: session.start=%s session.end=%s", req1.EventID, req3.EventID)
	}
}

// TestBridgeStatusMapping proves: delegation statuses map to correct work_event kinds/statuses.
func TestBridgeStatusMapping(t *testing.T) {
	tests := []struct {
		name      string
		degStatus string
		wantKind  string
		wantSts   string
	}{
		{"pending → session.start/running", "pending", "session.start", "running"},
		{"running → session.start/running", "running", "session.start", "running"},
		{"completed → session.end/done", "completed", "session.end", "done"},
		{"failed → session.end/failed", "failed", "session.end", "failed"},
		{"interrupted → session.end/cancelled", "interrupted", "session.end", "cancelled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deg := makeFakeDelegation(uuid.NewString(), uuid.NewString(), "task", tt.degStatus)
			req := BuildBridgeWorkEventRequest(deg, "")
			if req.Kind != tt.wantKind {
				t.Fatalf("kind: got %q, want %q", req.Kind, tt.wantKind)
			}
			if req.Status != tt.wantSts {
				t.Fatalf("status: got %q, want %q", req.Status, tt.wantSts)
			}
			if req.Host != "bridge" {
				t.Fatalf("host: got %q, want \"bridge\"", req.Host)
			}
			if req.Harness != "hermes" {
				t.Fatalf("harness: got %q, want \"hermes\"", req.Harness)
			}
			if req.LivenessMode != "bounded" {
				t.Fatalf("liveness_mode: got %q, want \"bounded\"", req.LivenessMode)
			}
		})
	}
}

// TestBridgePatchTerminalStatusMapping proves: PATCH kindOverride maps terminal delegation statuses correctly.
func TestBridgePatchTerminalStatusMapping(t *testing.T) {
	tests := []struct {
		name      string
		degStatus string
		override  string
		wantKind  string
		wantSts   string
	}{
		{"completed + session.end → done", "completed", "session.end", "session.end", "done"},
		{"failed + session.end → failed", "failed", "session.end", "session.end", "failed"},
		{"interrupted + session.end → cancelled", "interrupted", "session.end", "session.end", "cancelled"},
		{"completed + session.end default → done", "weird", "session.end", "session.end", "done"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deg := makeFakeDelegation(uuid.NewString(), uuid.NewString(), "task", tt.degStatus)
			req := BuildBridgeWorkEventRequest(deg, tt.override)
			if req.Kind != tt.wantKind {
				t.Fatalf("kind: got %q, want %q", req.Kind, tt.wantKind)
			}
			if req.Status != tt.wantSts {
				t.Fatalf("status: got %q, want %q", req.Status, tt.wantSts)
			}
		})
	}
}

// TestBridgeRetryIdempotency proves: calling Ingest twice with the same bridge request
// results in exactly one row (202 on second call). Regression for finding #3.
func TestBridgeRetryIdempotency(t *testing.T) {
	fq := newFakeQuerier()
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	deg := makeFakeDelegation(uuid.NewString(), uuid.NewString(), "bridge task", "completed")
	req := BuildBridgeWorkEventRequest(deg, "session.end")

	// First Ingest → 201
	row1, s1, err := svc.Ingest(context.Background(), req, fakeOwnerID)
	if err != nil || s1 != 201 {
		t.Fatalf("first bridge ingest: status=%d err=%v", s1, err)
	}
	if row1.Kind != "session.end" {
		t.Fatalf("expected kind session.end, got %s", row1.Kind)
	}
	if row1.Status.String != "done" {
		t.Fatalf("expected status done, got %s", row1.Status.String)
	}

	// Second Ingest (same deterministic event_id) → 202, same row
	bus2 := NewEventBus()
	svc2 := NewIngestService(fq, bus2, slog.Default(), "/tmp/test")
	sub2 := bus2.Subscribe()
	defer bus2.Unsubscribe(sub2)

	row2, s2, err := svc2.Ingest(context.Background(), req, fakeOwnerID)
	if err != nil {
		t.Fatalf("second bridge ingest error: %v", err)
	}
	if s2 != 202 {
		t.Fatalf("expected 202 on retry, got %d", s2)
	}
	if row2.ID.String() != row1.ID.String() {
		t.Fatalf("retry should return same id: %s vs %s", row2.ID.String(), row1.ID.String())
	}
	if fq.inserts != 1 {
		t.Fatalf("expected 1 insert after retry, got %d", fq.inserts)
	}

	// No SSE on retry
	select {
	case evt := <-sub2:
		t.Fatalf("expected NO SSE on bridge retry, got type=%q", evt.Type)
	default:
		// good
	}
}

// TestBridgePOSTAndPATCHProduceCorrectEvents proves the full delegation flow:
// POST (pending) → session.start, PATCH (completed) → session.end.
// Regression for finding #5 (PATCH path synthesis).
func TestBridgePOSTAndPATCHProduceCorrectEvents(t *testing.T) {
	fq := newFakeQuerier()
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	degID := uuid.NewString()
	parentID := uuid.NewString()

	// Simulate POST: CreateDelegation with status "pending" → session.start
	degPending := makeFakeDelegation(degID, parentID, "delegate task", "pending")
	reqStart := BuildBridgeWorkEventRequest(degPending, "")
	row1, s1, err := svc.Ingest(context.Background(), reqStart, fakeOwnerID)
	if err != nil || s1 != 201 {
		t.Fatalf("POST session.start: status=%d err=%v", s1, err)
	}
	if row1.Kind != "session.start" {
		t.Fatalf("POST: expected kind session.start, got %s", row1.Kind)
	}
	if row1.Status.String != "running" {
		t.Fatalf("POST: expected status running, got %s", row1.Status.String)
	}
	if fq.inserts != 1 {
		t.Fatalf("expected 1 insert after POST, got %d", fq.inserts)
	}

	// Simulate PATCH: UpdateDelegationStatus to "completed" → session.end
	degCompleted := degPending
	degCompleted.Status = "completed"
	reqEnd := BuildBridgeWorkEventRequest(degCompleted, "session.end")
	bus2 := NewEventBus()
	svc2 := NewIngestService(fq, bus2, slog.Default(), "/tmp/test")

	row2, s2, err := svc2.Ingest(context.Background(), reqEnd, fakeOwnerID)
	if err != nil || s2 != 201 {
		t.Fatalf("PATCH session.end: status=%d err=%v", s2, err)
	}
	if row2.Kind != "session.end" {
		t.Fatalf("PATCH: expected kind session.end, got %s", row2.Kind)
	}
	if row2.Status.String != "done" {
		t.Fatalf("PATCH: expected status done, got %s", row2.Status.String)
	}

	// Verify exactly 2 inserts total (session.start + session.end)
	if fq.inserts != 2 {
		t.Fatalf("expected 2 inserts (start + end), got %d", fq.inserts)
	}

	// Verify the two events have different event_ids (different kinds)
	if row1.EventID.String() == row2.EventID.String() {
		t.Fatalf("POST and PATCH should produce different event_ids: %s", row1.EventID.String())
	}
}

// TestBridgePATCHRetryIdempotency proves: PATCHing terminal status twice produces one session.end.
// Regression for finding #3 (bridge retry idempotency on PATCH path).
func TestBridgePATCHRetryIdempotency(t *testing.T) {
	fq := newFakeQuerier()
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/tmp/test")

	degID := uuid.NewString()
	parentID := uuid.NewString()

	// First PATCH terminal → 201
	deg := makeFakeDelegation(degID, parentID, "task", "completed")
	req := BuildBridgeWorkEventRequest(deg, "session.end")
	row1, s1, err := svc.Ingest(context.Background(), req, fakeOwnerID)
	if err != nil || s1 != 201 {
		t.Fatalf("first PATCH: status=%d err=%v", s1, err)
	}

	// Retry PATCH (same delegation_id + same kind → same deterministic event_id) → 202
	bus2 := NewEventBus()
	svc2 := NewIngestService(fq, bus2, slog.Default(), "/tmp/test")

	row2, s2, err := svc2.Ingest(context.Background(), req, fakeOwnerID)
	if err != nil {
		t.Fatalf("retry PATCH error: %v", err)
	}
	if s2 != 202 {
		t.Fatalf("expected 202 on PATCH retry, got %d", s2)
	}
	if row2.ID.String() != row1.ID.String() {
		t.Fatalf("retry should return same id: %s vs %s", row2.ID.String(), row1.ID.String())
	}
	if fq.inserts != 1 {
		t.Fatalf("expected 1 insert after PATCH retry, got %d", fq.inserts)
	}
}

// validFakeRequest returns a minimal valid WorkEventRequest for testing with the fake querier.
func validFakeRequest(eventID string) WorkEventRequest {
	pid := 12345
	return WorkEventRequest{
		Schema:       "agentos.work_event/v1",
		EventID:      eventID,
		Host:         "testhost",
		Harness:      "hermes",
		Kind:         "session.start",
		SessionID:    uuid.NewString(),
		Ts:           time.Now().Format(time.RFC3339),
		Status:       "running",
		LivenessMode: "supervised",
		Pid:          &pid,
		Tenant:       "personal",
	}
}

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

func (fq *fakeQuerier) GetWorkEventByEventID(_ context.Context, eventID pgtype.UUID) (db.WorkEvent, error) {
	fq.mu.Lock()
	defer fq.mu.Unlock()

	if fq.getErr != nil {
		return db.WorkEvent{}, fq.getErr
	}

	if row, ok := fq.events[eventID.String()]; ok {
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

	row, status, err := svc.Ingest(context.Background(), req)
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
	row1, s1, err := svc.Ingest(context.Background(), req)
	if err != nil || s1 != 201 {
		t.Fatalf("first insert: status=%d err=%v", s1, err)
	}

	// Second insert with same event_id → 202 (share same fake, so conflict triggers).
	// Use a fresh bus to detect SSE (should NOT publish for duplicate).
	bus2 := NewEventBus()
	svc2 := NewIngestService(fq, bus2, slog.Default(), "/tmp/test")
	sub2 := bus2.Subscribe()
	defer bus2.Unsubscribe(sub2)

	row2, s2, err := svc2.Ingest(context.Background(), req)
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

	row, status, err := svc.Ingest(context.Background(), req)
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

	row, status, err := svc.Ingest(context.Background(), req)
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

	_, status, err := svc.Ingest(context.Background(), req)
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
	_, s1, err := svc.Ingest(context.Background(), req)
	if err != nil || s1 != 201 {
		t.Fatalf("first insert: status=%d err=%v", s1, err)
	}

	// Now make the fetch on conflict fail
	fq.getErr = errFakeDB

	_, s2, err := svc.Ingest(context.Background(), req)
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

	_, status, err := svc.Ingest(context.Background(), req)
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
			_, httpStatus, err := svc.Ingest(context.Background(), req)
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

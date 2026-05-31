package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/google/uuid"

	"github.com/tim4net/agent-os/internal/db"
)

// testLogger is a discard logger for tests to avoid nil panics.
var testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

// --- fake TrackerQuerier ---

type fakeTrackerQuerier struct {
	mu sync.Mutex

	// upserted captures every UpsertTrackerItem call.
	upserted []db.UpsertTrackerItemParams

	// preseeded rows for List/Get queries.
	byProject []db.TrackerItem
	byTenant  []db.TrackerItem
	byID      []db.TrackerItem // keyed on project_id+external_ref
	projects  []db.Project
}

func (f *fakeTrackerQuerier) UpsertTrackerItem(_ context.Context, arg db.UpsertTrackerItemParams) (db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserted = append(f.upserted, arg)

	// Return a plausible row (simulates DB response).
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	return db.TrackerItem{
		ID:           pgtype.UUID{Bytes: id, Valid: true},
		ProjectID:    arg.ProjectID,
		ExternalRef:  arg.ExternalRef,
		Title:        arg.Title,
		Status:       arg.Status,
		ItemType:     arg.ItemType,
		CanonicalUrl: arg.CanonicalUrl,
		Payload:      arg.Payload,
		Tenant:       arg.Tenant,
		SyncedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
		CreatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}, nil
}

func (f *fakeTrackerQuerier) GetTrackerItem(_ context.Context, arg db.GetTrackerItemParams) (db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.byID {
		if pgtypeUUIDEquals(r.ProjectID, arg.ProjectID) && r.ExternalRef == arg.ExternalRef {
			return r, nil
		}
	}
	return db.TrackerItem{}, fmt.Errorf("not found")
}

func (f *fakeTrackerQuerier) ListTrackerItemsByProject(_ context.Context, arg db.ListTrackerItemsByProjectParams) ([]db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []db.TrackerItem
	for _, r := range f.byProject {
		if pgtypeUUIDEquals(r.ProjectID, arg.ProjectID) && r.Tenant == arg.Tenant {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeTrackerQuerier) ListTrackerItemsByTenant(_ context.Context, arg db.ListTrackerItemsByTenantParams) ([]db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []db.TrackerItem
	for _, r := range f.byTenant {
		if r.Tenant == arg.Tenant {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeTrackerQuerier) CountTrackerItemsByTenant(_ context.Context, tenant string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int64
	for _, r := range f.byTenant {
		if r.Tenant == tenant {
			count++
		}
	}
	return count, nil
}

func (f *fakeTrackerQuerier) GetTrackerProjects(_ context.Context, arg db.GetTrackerProjectsParams) ([]db.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []db.Project
	for _, p := range f.projects {
		if p.Tracker == arg.Tracker && p.Tenant == arg.Tenant {
			out = append(out, p)
		}
	}
	return out, nil
}

// failingQuerier wraps a TrackerQuerier and conditionally fails UpsertTrackerItem.
type failingQuerier struct {
	inner TrackerQuerier
	failOn func(db.UpsertTrackerItemParams) bool
}

func (f *failingQuerier) UpsertTrackerItem(ctx context.Context, arg db.UpsertTrackerItemParams) (db.TrackerItem, error) {
	if f.failOn(arg) {
		return db.TrackerItem{}, fmt.Errorf("synthetic DB failure")
	}
	return f.inner.UpsertTrackerItem(ctx, arg)
}

func (f *failingQuerier) GetTrackerItem(ctx context.Context, arg db.GetTrackerItemParams) (db.TrackerItem, error) {
	return f.inner.GetTrackerItem(ctx, arg)
}

func (f *failingQuerier) ListTrackerItemsByProject(ctx context.Context, arg db.ListTrackerItemsByProjectParams) ([]db.TrackerItem, error) {
	return f.inner.ListTrackerItemsByProject(ctx, arg)
}

func (f *failingQuerier) ListTrackerItemsByTenant(ctx context.Context, arg db.ListTrackerItemsByTenantParams) ([]db.TrackerItem, error) {
	return f.inner.ListTrackerItemsByTenant(ctx, arg)
}

func (f *failingQuerier) CountTrackerItemsByTenant(ctx context.Context, tenant string) (int64, error) {
	return f.inner.CountTrackerItemsByTenant(ctx, tenant)
}

func (f *failingQuerier) GetTrackerProjects(ctx context.Context, arg db.GetTrackerProjectsParams) ([]db.Project, error) {
	return f.inner.GetTrackerProjects(ctx, arg)
}

// --- helpers ---

func mustParseUUID(s string) pgtype.UUID {
	u, _ := uuid.Parse(s)
	return pgtype.UUID{Bytes: u, Valid: true}
}

// --- Tests ---

// TestShortcutSync_EndToEnd is the primary test for findings #1 and #2.
// It wires a fake TrackerQuerier + httptest.Server stub, calls the real Sync,
// and asserts captured UpsertTrackerItemParams carry correct external_ref, item_type, tenant,
// and a populated synced_at (from the returned db.TrackerItem).
func TestShortcutSync_EndToEnd(t *testing.T) {
	projectUUID := mustParseUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	shortcutProjectID := 42

	// Stub the Shortcut API with a single story.
	storyNum := 91130
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Shortcut-Token") != "test-token" {
			t.Errorf("expected Shortcut-Token header, got: %v", r.Header)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		resp := shortcutListResponse{
			Data: []ShortcutStory{
				{
					ID:         int64(storyNum * 100),
					Num:        storyNum,
					Name:       "SPOG timeline UI",
					EntityType: "feature",
					State:      "in progress",
					AppURL:     "https://app.shortcut.com/story/91130",
					UpdatedAt:  time.Now(),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer stub.Close()

	fake := &fakeTrackerQuerier{
		projects: []db.Project{
			{
				ID:           projectUUID,
				Slug:         "agent-os",
				Name:         "Agent OS",
				Tenant:       "personal",
				Tracker:      "shortcut",
				ExternalRef:  pgtype.Text{String: fmt.Sprintf("%d", shortcutProjectID), Valid: true},
				CreatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
				UpdatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			},
		},
	}

	client := &ShortcutClient{
		apiToken: "test-token",
		client:   &http.Client{Timeout: 5 * time.Second},
		baseURL:  stub.URL,
		log:      nil,
	}
	src := NewShortcutSourceWithClient(fake, client, testLogger)

	result, err := src.Sync(context.Background(), projectUUID, "personal")
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if result.Failed != 0 {
		t.Fatalf("Sync reported %d failures, want 0", result.Failed)
	}
	if result.Synced != 1 {
		t.Fatalf("Sync reported %d synced, want 1", result.Synced)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()

	if len(fake.upserted) != 1 {
		t.Fatalf("expected 1 upsert call, got %d", len(fake.upserted))
	}

	up := fake.upserted[0]

	// Finding #1: Assert the external_ref is derived from story.Num, not a hardcoded literal.
	expectedRef := fmt.Sprintf("SC-%d", storyNum)
	if up.ExternalRef != expectedRef {
		t.Errorf("external_ref = %q, want %q (SC-<num> from API, not hardcoded)", up.ExternalRef, expectedRef)
	}

	// Assert item_type is mapped correctly.
	if up.ItemType != "feature" {
		t.Errorf("item_type = %q, want %q", up.ItemType, "feature")
	}

	// Assert tenant is carried through.
	if up.Tenant != "personal" {
		t.Errorf("tenant = %q, want %q", up.Tenant, "personal")
	}

	// Assert canonical_url is populated.
	if !up.CanonicalUrl.Valid || up.CanonicalUrl.String == "" {
		t.Errorf("canonical_url should be valid and non-empty, got %+v", up.CanonicalUrl)
	}
}

// TestShortcutSync_FailureTracking asserts that Sync returns an error and a non-zero
// Failed count when upserts fail (Finding #4).
func TestShortcutSync_FailureTracking(t *testing.T) {
	projectUUID := mustParseUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := shortcutListResponse{
			Data: []ShortcutStory{
				{ID: 1, Num: 100, Name: "Ok Story", EntityType: "story", State: "done"},
				{ID: 2, Num: 200, Name: "Bad Story", EntityType: "bug", State: "todo"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer stub.Close()

	// Wrap fake with conditional failure.
	callCount := 0
	wrapper := &failingQuerier{
		inner: &fakeTrackerQuerier{
			projects: []db.Project{
				{
					ID:          projectUUID,
					Tenant:      "personal",
					Tracker:     "shortcut",
					ExternalRef: pgtype.Text{String: "99", Valid: true},
					CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
					UpdatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
				},
			},
		},
		failOn: func(arg db.UpsertTrackerItemParams) bool {
			callCount++
			return callCount == 2
		},
	}

	client := &ShortcutClient{
		apiToken: "test-token",
		client:   &http.Client{Timeout: 5 * time.Second},
		baseURL:  stub.URL,
	}
	src := NewShortcutSourceWithClient(wrapper, client, testLogger)

	result, err := src.Sync(context.Background(), projectUUID, "personal")
	if err == nil {
		t.Fatal("Sync should return error when an upsert fails")
	}
	if result.Synced != 1 {
		t.Errorf("synced = %d, want 1", result.Synced)
	}
	if result.Failed != 1 {
		t.Errorf("failed = %d, want 1", result.Failed)
	}
}

// TestShortcutList_TenantIsolation asserts that List scoped to one tenant
// does not return items from another tenant under the same project (Finding #3).
func TestShortcutList_TenantIsolation(t *testing.T) {
	projectUUID := mustParseUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

	fake := &fakeTrackerQuerier{
		byProject: []db.TrackerItem{
			// dayjob item under the same project
			{
				ID: pgtype.UUID{Bytes: uuid.MustParse("11111111-0000-0000-0000-000000000001"), Valid: true},
				ProjectID:   projectUUID,
				ExternalRef: "SC-500",
				Title:       "Dayjob Item",
				Tenant:      "dayjob",
				Status:      "done",
				ItemType:    "story",
				SyncedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
				CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
				UpdatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			},
			// personal item under the same project
			{
				ID: pgtype.UUID{Bytes: uuid.MustParse("11111111-0000-0000-0000-000000000002"), Valid: true},
				ProjectID:    projectUUID,
				ExternalRef:  "SC-600",
				Title:        "Personal Item",
				Tenant:       "personal",
				Status:       "todo",
				ItemType:     "feature",
				SyncedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
				CreatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
				UpdatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			},
		},
	}

	src := NewShortcutSource(fake, testLogger)

	items, err := src.List(context.Background(), projectUUID, "personal", 50, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ExternalRef != "SC-600" {
		t.Errorf("expected personal item SC-600, got %s", items[0].ExternalRef)
	}
	if items[0].Tenant != "personal" {
		t.Errorf("expected tenant personal, got %s", items[0].Tenant)
	}
}

// TestNormalizeItemType asserts the entity_type mapping (part of finding #2 — coverage).
func TestNormalizeItemType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"story", "story"},
		{"Story", "story"},
		{"bug", "bug"},
		{"chore", "chore"},
		{"task", "task"},
		{"feature", "feature"},
		{"Feature", "feature"},
		{"epic", "story"}, // unknown defaults to story
		{"", "story"},
	}
	for _, tt := range tests {
		got := normalizeItemType(tt.input)
		if got != tt.want {
			t.Errorf("normalizeItemType(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestSyncEndpoint_HTTPMethod verifies only GET is used against Shortcut (F5 gate).
func TestSyncEndpoint_HTTPMethod(t *testing.T) {
	projectUUID := mustParseUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	shortcutProjectID := 42

	var methods []string
	var mu sync.Mutex
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		mu.Unlock()
		resp := shortcutListResponse{Data: []ShortcutStory{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer stub.Close()

	fake := &fakeTrackerQuerier{
		projects: []db.Project{
			{
				ID:          projectUUID,
				Tenant:      "personal",
				Tracker:     "shortcut",
				ExternalRef: pgtype.Text{String: fmt.Sprintf("%d", shortcutProjectID), Valid: true},
				CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
				UpdatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			},
		},
	}

	client := &ShortcutClient{
		apiToken: "test-token",
		client:   &http.Client{Timeout: 5 * time.Second},
		baseURL:  stub.URL,
	}
	src := NewShortcutSourceWithClient(fake, client, testLogger)

	_, err := src.Sync(context.Background(), projectUUID, "personal")
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, m := range methods {
		if m != http.MethodGet {
			t.Errorf("F5 violation: Shortcut API received %s, only GET allowed", m)
		}
	}
}

// TestTrackerItemFromDB asserts the DB-to-domain mapping (Finding #1 — structural coverage).
func TestTrackerItemFromDB(t *testing.T) {
	now := time.Now()
	row := db.TrackerItem{
		ID:          pgtype.UUID{Bytes: uuid.MustParse("22222222-0000-0000-0000-000000000001"), Valid: true},
		ProjectID:   pgtype.UUID{Bytes: uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"), Valid: true},
		ExternalRef: "SC-91130",
		Title:       "SPOG timeline",
		Status:      "in progress",
		ItemType:    "feature",
		CanonicalUrl: pgtype.Text{String: "https://app.shortcut.com/story/91130", Valid: true},
		Tenant:      "personal",
		SyncedAt:    pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	entry := TrackerItemFromDB(row)

	if entry.ExternalRef != "SC-91130" {
		t.Errorf("ExternalRef = %q, want %q", entry.ExternalRef, "SC-91130")
	}
	if entry.ItemType != "feature" {
		t.Errorf("ItemType = %q, want %q", entry.ItemType, "feature")
	}
	if entry.Tenant != "personal" {
		t.Errorf("Tenant = %q, want %q", entry.Tenant, "personal")
	}
	if entry.CanonicalURL != "https://app.shortcut.com/story/91130" {
		t.Errorf("CanonicalURL = %q, want non-empty", entry.CanonicalURL)
	}
	if entry.SyncedAt.IsZero() {
		t.Error("SyncedAt should be populated, got zero")
	}
}

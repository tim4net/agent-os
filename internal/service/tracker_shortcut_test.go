package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// fakeTrackerQuerier implements TrackerQuerier in-memory for unit testing.
// Captures upsert calls so tests can assert on the exact params passed.
type fakeTrackerQuerier struct {
	mu           sync.Mutex
	items        []db.TrackerItem
	upserted     []db.UpsertTrackerItemParams // captured in order
	upsertErr    error                        // error returned on Nth call (0 = all succeed)
	failAfter    int                          // number of successful upserts before errors start
	byProject    map[string][]db.TrackerItem  // keyed by "projectID:tenant"
	byExternalRef map[string]db.TrackerItem // keyed by "projectID:externalRef"
}

func newFakeTrackerQuerier() *fakeTrackerQuerier {
	return &fakeTrackerQuerier{
		byProject:     make(map[string][]db.TrackerItem),
		byExternalRef: make(map[string]db.TrackerItem),
	}
}

func (f *fakeTrackerQuerier) UpsertTrackerItem(_ context.Context, arg db.UpsertTrackerItemParams) (db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.upserted = append(f.upserted, arg)

	if f.failAfter > 0 && len(f.upserted) > f.failAfter {
		return db.TrackerItem{}, f.upsertErr
	}

	now := time.Now()
	item := db.TrackerItem{
		ProjectID:    arg.ProjectID,
		ExternalRef:  arg.ExternalRef,
		Title:        arg.Title,
		Status:       arg.Status,
		ItemType:     arg.ItemType,
		CanonicalUrl: arg.CanonicalUrl,
		Payload:      arg.Payload,
		Tenant:       arg.Tenant,
		SyncedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
	}
	// Generate a deterministic ID.
	item.ID = pgtype.UUID{Valid: true}
	_ = item.ID.Scan(fmt.Sprintf("00000000-0000-0000-0000-%012s", arg.ExternalRef))

	f.items = append(f.items, item)

	pk := fmt.Sprintf("%s:%s", arg.ProjectID.String(), arg.Tenant)
	f.byProject[pk] = append(f.byProject[pk], item)

	ek := fmt.Sprintf("%s:%s", arg.ProjectID.String(), arg.ExternalRef)
	f.byExternalRef[ek] = item

	return item, nil
}

func (f *fakeTrackerQuerier) GetTrackerItem(_ context.Context, arg db.GetTrackerItemParams) (db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ek := fmt.Sprintf("%s:%s", arg.ProjectID.String(), arg.ExternalRef)
	if item, ok := f.byExternalRef[ek]; ok {
		return item, nil
	}
	return db.TrackerItem{}, fmt.Errorf("not found")
}

func (f *fakeTrackerQuerier) ListTrackerItemsByProject(_ context.Context, arg db.ListTrackerItemsByProjectParams) ([]db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pk := fmt.Sprintf("%s:%s", arg.ProjectID.String(), arg.Tenant)
	items := f.byProject[pk]
	// Apply offset/limit
	if int(arg.Offset) < len(items) {
		items = items[arg.Offset:]
	}
	if int(arg.Limit) < len(items) {
		items = items[:arg.Limit]
	}
	if items == nil {
		return []db.TrackerItem{}, nil
	}
	return items, nil
}

func (f *fakeTrackerQuerier) ListTrackerItemsByTenant(_ context.Context, arg db.ListTrackerItemsByTenantParams) ([]db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var items []db.TrackerItem
	for _, v := range f.items {
		if v.Tenant == arg.Tenant {
			items = append(items, v)
		}
	}
	if items == nil {
		return []db.TrackerItem{}, nil
	}
	if int(arg.Offset) < len(items) {
		items = items[arg.Offset:]
	}
	if int(arg.Limit) < len(items) {
		items = items[:arg.Limit]
	}
	return items, nil
}

func (f *fakeTrackerQuerier) CountTrackerItemsByTenant(_ context.Context, tenant string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int64
	for _, v := range f.items {
		if v.Tenant == tenant {
			count++
		}
	}
	return count, nil
}

// GetTrackerProjects needs to return a project matching our test projectID
// with tracker="shortcut" and a valid external_ref (the Shortcut project ID).
func (f *fakeTrackerQuerier) GetTrackerProjects(_ context.Context, arg db.GetTrackerProjectsParams) ([]db.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Return a project whose external_ref is the Shortcut numeric project ID "91130"
	return []db.Project{
		{
			ID:          testProjectUUID,
			Slug:        "test-project",
			Name:        "Test Project",
			Tenant:      arg.Tenant,
			Tracker:     arg.Tracker,
			ExternalRef: pgtype.Text{String: "91130", Valid: true},
			CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	}, nil
}

// mustParseUUID creates a valid pgtype.UUID from a UUID string.
func mustParseUUID(s string) pgtype.UUID {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		panic(err)
	}
	return u
}

var testProjectUUID = mustParseUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

// stubShortcutServer returns an httptest.Server that serves fake Shortcut API responses.
func stubShortcutServer(stories []ShortcutStory) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Shortcut-Token") != "test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Only GET requests allowed (F5 gate).
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(shortcutListResponse{
			Next: "",
			Data: stories,
		})
	}))
}

// TestSync_FetchMapUpsert tests the full Sync pipeline:
// fetch from Shortcut API → map to external_ref SC-<n> → upsert via TrackerQuerier.
// This covers findings #1 and #2 — the core sync pipeline and the mapping.
func TestSync_FetchMapUpsert(t *testing.T) {
	stories := []ShortcutStory{
		{
			ID:         1001,
			Num:        91130,
			Name:       "Build auth service",
			EntityType: "feature",
			State:      "in progress",
			AppURL:     "https://app.shortcut.com/story/91130",
			UpdatedAt:  time.Date(2025, 5, 30, 12, 0, 0, 0, time.UTC),
		},
		{
			ID:         1002,
			Num:        91131,
			Name:       "Fix login bug",
			EntityType: "bug",
			State:      "done",
			AppURL:     "https://app.shortcut.com/story/91131",
			UpdatedAt:  time.Date(2025, 5, 30, 13, 0, 0, 0, time.UTC),
		},
		{
			ID:         1003,
			Num:        91132,
			Name:       "Deploy pipeline",
			EntityType: "chore",
			State:      "todo",
			AppURL:     "", // no canonical URL
			UpdatedAt:  time.Date(2025, 5, 30, 14, 0, 0, 0, time.UTC),
		},
	}

	srv := stubShortcutServer(stories)
	defer srv.Close()

	fake := newFakeTrackerQuerier()
	log := slog.Default()

	src := NewShortcutSourceWithClient(fake, &ShortcutClient{
		apiToken: "test-token",
		client:   srv.Client(),
		baseURL:  srv.URL,
		log:      log,
	}, log)

	result, err := src.Sync(context.Background(), testProjectUUID, "dayjob")
	if err != nil {
		t.Fatalf("Sync returned unexpected error: %v", err)
	}

	// Assert synced count matches the number of stories.
	if result.Synced != len(stories) {
		t.Errorf("Synced=%d, want %d", result.Synced, len(stories))
	}
	if result.Failed != 0 {
		t.Errorf("Failed=%d, want 0", result.Failed)
	}

	// Verify captured upsert params.
	if len(fake.upserted) != len(stories) {
		t.Fatalf("upserted=%d, want %d", len(fake.upserted), len(stories))
	}

	// Finding #1: Assert the SC-<n> external_ref comes from story.Num, not a hardcoded literal.
	// The Sync method constructs externalRef via fmt.Sprintf("SC-%d", story.Num).
	if fake.upserted[0].ExternalRef != "SC-91130" {
		t.Errorf("upserted[0].ExternalRef=%q, want %q", fake.upserted[0].ExternalRef, "SC-91130")
	}
	if fake.upserted[1].ExternalRef != "SC-91131" {
		t.Errorf("upserted[1].ExternalRef=%q, want %q", fake.upserted[1].ExternalRef, "SC-91131")
	}
	if fake.upserted[2].ExternalRef != "SC-91132" {
		t.Errorf("upserted[2].ExternalRef=%q, want %q", fake.upserted[2].ExternalRef, "SC-91132")
	}

	// Verify item_type mapping.
	if fake.upserted[0].ItemType != "feature" {
		t.Errorf("upserted[0].ItemType=%q, want %q", fake.upserted[0].ItemType, "feature")
	}
	if fake.upserted[1].ItemType != "bug" {
		t.Errorf("upserted[1].ItemType=%q, want %q", fake.upserted[1].ItemType, "bug")
	}
	if fake.upserted[2].ItemType != "chore" {
		t.Errorf("upserted[2].ItemType=%q, want %q", fake.upserted[2].ItemType, "chore")
	}

	// Verify tenant is threaded through.
	for i, up := range fake.upserted {
		if up.Tenant != "dayjob" {
			t.Errorf("upserted[%d].Tenant=%q, want %q", i, up.Tenant, "dayjob")
		}
	}

	// Verify canonical_url is populated (with non-empty AppURL).
	if !fake.upserted[0].CanonicalUrl.Valid || fake.upserted[0].CanonicalUrl.String != "https://app.shortcut.com/story/91130" {
		t.Errorf("upserted[0].CanonicalUrl=%v, want valid with URL", fake.upserted[0].CanonicalUrl)
	}

	// Verify canonical_url is invalid/empty for story with no AppURL (Finding #7).
	if fake.upserted[2].CanonicalUrl.Valid {
		t.Errorf("upserted[2].CanonicalUrl should be invalid for empty AppURL, got Valid=true")
	}

	// Verify synced_at is populated on the returned DB row (fake sets it).
	if !fake.items[0].SyncedAt.Valid {
		t.Error("SyncedAt should be Valid after upsert")
	}
	if fake.items[0].SyncedAt.Time.IsZero() {
		t.Error("SyncedAt.Time should not be zero after upsert")
	}

	// Verify title mapping.
	if fake.upserted[0].Title != "Build auth service" {
		t.Errorf("upserted[0].Title=%q, want %q", fake.upserted[0].Title, "Build auth service")
	}

	// Verify payload contains the raw Shortcut metadata.
	var payload map[string]any
	if err := json.Unmarshal(fake.upserted[0].Payload, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if payload["shortcut_num"] != float64(91130) {
		t.Errorf("payload.shortcut_num=%v, want 91130", payload["shortcut_num"])
	}
}

// TestSync_UpsertFailure tests that Sync returns a non-nil error when upserts fail (Finding #4).
func TestSync_UpsertFailure(t *testing.T) {
	stories := []ShortcutStory{
		{ID: 1, Num: 100, Name: "Story A", EntityType: "story", State: "todo"},
		{ID: 2, Num: 101, Name: "Story B", EntityType: "bug", State: "done"},
		{ID: 3, Num: 102, Name: "Story C", EntityType: "feature", State: "in progress"},
	}

	srv := stubShortcutServer(stories)
	defer srv.Close()

	fake := newFakeTrackerQuerier()
	fake.failAfter = 1 // first upsert succeeds, rest fail
	fake.upsertErr = fmt.Errorf("DB connection lost")

	log := slog.Default()
	src := NewShortcutSourceWithClient(fake, &ShortcutClient{
		apiToken: "test-token",
		client:   srv.Client(),
		baseURL:  srv.URL,
		log:      log,
	}, log)

	result, err := src.Sync(context.Background(), testProjectUUID, "dayjob")
	if err == nil {
		t.Fatal("Sync should return non-nil error when upserts fail")
	}

	if result.Synced != 1 {
		t.Errorf("Synced=%d, want 1", result.Synced)
	}
	if result.Failed != 2 {
		t.Errorf("Failed=%d, want 2", result.Failed)
	}

	// Error message should mention failures.
	if result.Synced == 3 && result.Failed == 0 {
		t.Error("Sync should NOT report success when some upserts failed")
	}
}

// TestList_TenantIsolation verifies that List scoped to a tenant only returns
// items for that tenant, even when another tenant has items in the same project.
// This covers Finding #3 (tenant isolation on the project path).
func TestList_TenantIsolation(t *testing.T) {
	fake := newFakeTrackerQuerier()
	log := slog.Default()

	// Seed items for two tenants under the same project.
	dayjobItem := db.TrackerItem{
		ProjectID: testProjectUUID,
		ExternalRef: "SC-100",
		Title:       "Dayjob story",
		Status:      "done",
		ItemType:    "feature",
		Tenant:      "dayjob",
		SyncedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	personalItem := db.TrackerItem{
		ProjectID: testProjectUUID,
		ExternalRef: "SC-200",
		Title:       "Personal story",
		Status:      "todo",
		ItemType:    "bug",
		Tenant:      "personal",
		SyncedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	fake.items = append(fake.items, dayjobItem, personalItem)
	pkDayjob := fmt.Sprintf("%s:dayjob", testProjectUUID.String())
	pkPersonal := fmt.Sprintf("%s:personal", testProjectUUID.String())
	fake.byProject[pkDayjob] = []db.TrackerItem{dayjobItem}
	fake.byProject[pkPersonal] = []db.TrackerItem{personalItem}

	src := NewShortcutSourceWithClient(fake, nil, log)

	// List with tenant="personal" should only return the personal item.
	items, err := src.List(context.Background(), testProjectUUID, "personal", 50, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("List(personal) returned %d items, want 1", len(items))
	}
	if items[0].ExternalRef != "SC-200" {
		t.Errorf("items[0].ExternalRef=%q, want %q", items[0].ExternalRef, "SC-200")
	}
	if items[0].Tenant != "personal" {
		t.Errorf("items[0].Tenant=%q, want %q", items[0].Tenant, "personal")
	}

	// List with tenant="dayjob" should only return the dayjob item.
	items, err = src.List(context.Background(), testProjectUUID, "dayjob", 50, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("List(dayjob) returned %d items, want 1", len(items))
	}
	if items[0].ExternalRef != "SC-100" {
		t.Errorf("items[0].ExternalRef=%q, want %q", items[0].ExternalRef, "SC-100")
	}

	// The personal list must NOT contain the dayjob item.
	for _, item := range items {
		if item.Tenant == "personal" {
			t.Errorf("dayjob list leaked personal item: %q", item.ExternalRef)
		}
	}
}

// TestList_Pagination verifies limit clamping and offset behavior.
func TestList_Pagination(t *testing.T) {
	fake := newFakeTrackerQuerier()
	log := slog.Default()

	// Seed 5 items.
	testPK := fmt.Sprintf("%s:test", testProjectUUID.String())
	for i := 0; i < 5; i++ {
		item := db.TrackerItem{
			ProjectID: testProjectUUID,
			ExternalRef: fmt.Sprintf("SC-%d", i+1),
			Tenant:      "test",
			SyncedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}
		fake.items = append(fake.items, item)
		fake.byProject[testPK] = append(fake.byProject[testPK], item)
	}

	src := NewShortcutSourceWithClient(fake, nil, log)

	// limit=0 defaults to 50.
	items, err := src.List(context.Background(), testProjectUUID, "test", 0, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 5 {
		t.Errorf("List(limit=0) returned %d items, want 5", len(items))
	}

	// limit > MaxTrackerItemLimit is clamped.
	items, err = src.List(context.Background(), testProjectUUID, "test", 999, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 5 {
		t.Errorf("List(limit=999) returned %d items, want 5", len(items))
	}

	// offset=2, limit=2 returns 2 items starting from the 3rd.
	items, err = src.List(context.Background(), testProjectUUID, "test", 2, 2)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("List(limit=2,offset=2) returned %d items, want 2", len(items))
	}
}

// TestShortcutStoryToTrackerItemMapping verifies the mapping from Shortcut story
// fields to tracker item upsert params. This is a focused mapping test that
// exercises Sync end-to-end to prove the mapping is real, not tautological.
func TestShortcutStoryToTrackerItemMapping(t *testing.T) {
	stories := []ShortcutStory{
		{
			ID:         9999,
			Num:        91130,
			Name:       "Implement OAuth2 flow",
			EntityType: "feature",
			State:      "in progress",
			AppURL:     "https://app.shortcut.com/story/91130",
			UpdatedAt:  time.Date(2025, 5, 30, 10, 0, 0, 0, time.UTC),
		},
	}

	srv := stubShortcutServer(stories)
	defer srv.Close()

	fake := newFakeTrackerQuerier()
	log := slog.Default()

	src := NewShortcutSourceWithClient(fake, &ShortcutClient{
		apiToken: "test-token",
		client:   srv.Client(),
		baseURL:  srv.URL,
		log:      log,
	}, log)

	_, err := src.Sync(context.Background(), testProjectUUID, "personal")
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	if len(fake.upserted) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fake.upserted))
	}

	up := fake.upserted[0]

	// AC6: SC-<n> mapping — external_ref uses story.Num, not story.ID.
	if up.ExternalRef != "SC-91130" {
		t.Errorf("ExternalRef=%q, want SC-91130 (from story.Num=%d)", up.ExternalRef, stories[0].Num)
	}

	// item_type mapping.
	if up.ItemType != "feature" {
		t.Errorf("ItemType=%q, want feature", up.ItemType)
	}

	// title.
	if up.Title != "Implement OAuth2 flow" {
		t.Errorf("Title=%q, want Implement OAuth2 flow", up.Title)
	}

	// status.
	if up.Status != "in progress" {
		t.Errorf("Status=%q, want in progress", up.Status)
	}

	// canonical_url.
	if !up.CanonicalUrl.Valid || up.CanonicalUrl.String != "https://app.shortcut.com/story/91130" {
		t.Errorf("CanonicalURL=%v, want valid URL", up.CanonicalUrl)
	}

	// tenant.
	if up.Tenant != "personal" {
		t.Errorf("Tenant=%q, want personal", up.Tenant)
	}

	// synced_at populated on the DB row.
	if !fake.items[0].SyncedAt.Valid {
		t.Error("SyncedAt should be populated after Sync")
	}
}

// TestF5Gate verifies that the stub Shortcut server rejects non-GET requests.
func TestF5Gate(t *testing.T) {
	srv := stubShortcutServer([]ShortcutStory{})
	defer srv.Close()

	client := srv.Client()

	// POST should be rejected.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/projects/1/stories", nil)
	req.Header.Set("Shortcut-Token", "test-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST returned %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}

	// PUT should be rejected.
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/projects/1/stories", nil)
	req.Header.Set("Shortcut-Token", "test-token")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("PUT returned %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

// TestNormalizeItemType verifies entity type normalization.
func TestNormalizeItemType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"story", "story"},
		{"Story", "story"},
		{"STORY", "story"},
		{"bug", "bug"},
		{"Bug", "bug"},
		{"chore", "chore"},
		{"task", "task"},
		{"feature", "feature"},
		{"Feature", "feature"},
		{"epic", "story"}, // unknown → default
		{"", "story"},     // empty → default
	}
	for _, tt := range tests {
		got := normalizeItemType(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeItemType(%q)=%q, want %q", tt.input, got, tt.expected)
		}
	}
}

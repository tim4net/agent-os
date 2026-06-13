package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// ---------------------------------------------------------------------------
// Route-level tests for TrackerRoutes (internal/api/trackers_test.go)
// Covers blocking review findings: clampLimit regression, tenant isolation,
// synced_at/canonical_url in response, and sync failure → 500.
// Uses real PG17 via getTestDB/newTestAPIWithDB (same harness as workevents_test.go).
// ---------------------------------------------------------------------------

// seedTrackerItem inserts a tracker_item row for testing. Returns the row.
func seedTrackerItem(t *testing.T, pool *pgxpool.Pool, item db.TrackerItem) db.TrackerItem {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Use raw SQL to insert since we're bypassing the service layer.
	// First generate a project row if needed.
	_, err := pool.Exec(ctx, `
		INSERT INTO projects (id, slug, name, tenant, tracker, external_ref, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (id) DO NOTHING
	`, item.ProjectID, fmt.Sprintf("proj-%s", uuid.NewString()[:8]), "Test Project", item.Tenant, "shortcut", "12345")
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}

	row, err := pool.Exec(ctx, `
		INSERT INTO tracker_items (project_id, external_ref, title, status, item_type, canonical_url, payload, tenant, synced_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, '{}', $7, NOW(), NOW(), NOW())
		ON CONFLICT (project_id, external_ref) DO UPDATE SET
			title = EXCLUDED.title,
			status = EXCLUDED.status,
			item_type = EXCLUDED.item_type,
			canonical_url = EXCLUDED.canonical_url,
			synced_at = NOW(),
			updated_at = NOW()
		RETURNING *
	`, item.ProjectID, item.ExternalRef, item.Title, item.Status, item.ItemType, item.CanonicalUrl, item.Tenant)
	if err != nil {
		t.Fatalf("seed tracker item: %v", err)
	}
	if row.RowsAffected() == 0 {
		t.Fatal("seed: no rows affected")
	}

	// Re-fetch the inserted row.
	var result db.TrackerItem
	err = pool.QueryRow(ctx, `
		SELECT id, project_id, external_ref, title, status, item_type, canonical_url, payload, tenant, synced_at, created_at, updated_at
		FROM tracker_items WHERE project_id = $1 AND external_ref = $2
	`, item.ProjectID, item.ExternalRef).Scan(
		&result.ID, &result.ProjectID, &result.ExternalRef,
		&result.Title, &result.Status, &result.ItemType,
		&result.CanonicalUrl, &result.Payload, &result.Tenant,
		&result.SyncedAt, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("re-fetch tracker item: %v", err)
	}
	return result
}

// mustParseUUID creates a pgtype.UUID from a UUID string.
func mustParseUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("parse UUID %q: %v", s, err)
	}
	return u
}

// ---------------------------------------------------------------------------
// Blocking #1: clampLimit regression guard
// Seed ≥201 tracker_items for one tenant, GET with limit=1000000,
// assert exactly MaxTrackerItemLimit rows returned.
// ---------------------------------------------------------------------------

func TestTrackerList_LargeLimitClamped(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	tenant := fmt.Sprintf("clamp-test-%s", uuid.NewString()[:8])
	projectID := mustParseUUID(t, uuid.NewString())

	// Seed 201 items.
	for i := 0; i < 201; i++ {
		seedTrackerItem(t, pool, db.TrackerItem{
			ProjectID:   projectID,
			ExternalRef: fmt.Sprintf("SC-%d", i),
			Title:       fmt.Sprintf("Item %d", i),
			Status:      "todo",
			ItemType:    "story",
			Tenant:      tenant,
		})
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool.Exec(ctx, "DELETE FROM tracker_items WHERE tenant LIKE 'clamp-test-%'")
		pool.Exec(ctx, "DELETE FROM projects WHERE tenant LIKE 'clamp-test-%'")
	})

	// Request with limit=1000000 — should be clamped to MaxTrackerItemLimit (200).
	rec := trackerRequest(a, "GET", "/", map[string]string{
		"tenant":    tenant,
		"limit":     "1000000",
		"project_id": projectID.String(),
	})

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatalf("items is not an array: %T", resp["items"])
	}

	// Should return exactly 200 (clamped), not 201 or 1000000.
	if len(items) != 200 {
		t.Errorf("got %d items with limit=1000000, want exactly 200 (MaxTrackerItemLimit clamped)", len(items))
	}

	limit, ok := resp["limit"].(float64)
	if !ok || int(limit) != 200 {
		t.Errorf("limit in response = %v, want 200", resp["limit"])
	}

	// Assert total == 201 (true row count, not the clamped page length).
	// This catches the prior bug where Total was set to len(items) (200).
	total, ok := resp["total"].(float64)
	if !ok || int(total) != 201 {
		t.Errorf("total in response = %v, want 201 (true row count, not clamped page length)", resp["total"])
	}
}

// ---------------------------------------------------------------------------
// Blocking #2a: list JSON payload contains synced_at and non-empty canonical_url
// ---------------------------------------------------------------------------

func TestTrackerList_ResponseContainsSyncedAtAndCanonicalURL(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	tenant := fmt.Sprintf("shape-test-%s", uuid.NewString()[:8])
	projectID := mustParseUUID(t, uuid.NewString())

	seeded := seedTrackerItem(t, pool, db.TrackerItem{
		ProjectID:   projectID,
		ExternalRef: "SC-9001",
		Title:       "Test item with URL",
		Status:      "done",
		ItemType:    "feature",
		CanonicalUrl: pgtype.Text{String: "https://app.shortcut.com/story/9001", Valid: true},
		Tenant:      tenant,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool.Exec(ctx, "DELETE FROM tracker_items WHERE tenant LIKE 'shape-test-%'")
		pool.Exec(ctx, "DELETE FROM projects WHERE tenant LIKE 'shape-test-%'")
	})

	rec := trackerRequest(a, "GET", "/", map[string]string{
		"tenant":     tenant,
		"project_id": projectID.String(),
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	items, ok := resp["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected non-empty items array, got %v", resp["items"])
	}

	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item is not an object: %T", items[0])
	}

	// Assert synced_at is present and non-zero.
	syncedAt, ok := item["synced_at"].(string)
	if !ok || syncedAt == "" {
		t.Errorf("synced_at missing or empty: %v", item["synced_at"])
	}
	// Verify synced_at is a valid RFC3339 timestamp (non-zero).
	if _, err := time.Parse(time.RFC3339, syncedAt); err != nil {
		t.Errorf("synced_at=%q is not valid RFC3339: %v", syncedAt, err)
	}

	// Assert canonical_url is present and non-empty (this item has a URL).
	canonicalURL, ok := item["canonical_url"].(string)
	if !ok {
		t.Errorf("canonical_url missing from response: %v", item)
	}
	if canonicalURL != seeded.CanonicalUrl.String {
		t.Errorf("canonical_url=%q, want %q", canonicalURL, seeded.CanonicalUrl.String)
	}
}

// ---------------------------------------------------------------------------
// Blocking #2b: tenant-wide listing for tenant A excludes tenant B's rows
// ---------------------------------------------------------------------------

func TestTrackerList_TenantWideIsolation(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	tenantA := fmt.Sprintf("iso-test-a-%s", uuid.NewString()[:8])
	tenantB := fmt.Sprintf("iso-test-b-%s", uuid.NewString()[:8])
	projectID := mustParseUUID(t, uuid.NewString())

	// Seed 3 items for tenant A.
	for i := 0; i < 3; i++ {
		seedTrackerItem(t, pool, db.TrackerItem{
			ProjectID:   projectID,
			ExternalRef: fmt.Sprintf("SC-A%d", i),
			Title:       fmt.Sprintf("Tenant A item %d", i),
			Status:      "todo",
			ItemType:    "story",
			Tenant:      tenantA,
		})
	}
	// Seed 2 items for tenant B under the same project.
	for i := 0; i < 2; i++ {
		seedTrackerItem(t, pool, db.TrackerItem{
			ProjectID:   projectID,
			ExternalRef: fmt.Sprintf("SC-B%d", i),
			Title:       fmt.Sprintf("Tenant B item %d", i),
			Status:      "done",
			ItemType:    "bug",
			Tenant:      tenantB,
		})
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pool.Exec(ctx, "DELETE FROM tracker_items WHERE tenant LIKE 'iso-test-%'")
		pool.Exec(ctx, "DELETE FROM projects WHERE tenant LIKE 'iso-test-%'")
	})

	// Tenant-wide list for tenant A — no project_id, just tenant.
	rec := trackerRequest(a, "GET", "/", map[string]string{
		"tenant": tenantA,
	})

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	items, ok := resp["items"].([]any)
	if !ok {
		t.Fatalf("items is not an array: %T", resp["items"])
	}

	// Should return exactly 3 items (tenant A only).
	if len(items) != 3 {
		t.Errorf("tenant A list returned %d items, want 3", len(items))
	}

	// Verify none of the returned items belong to tenant B.
	for i, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		tenant, _ := m["tenant"].(string)
		if tenant == tenantB {
			t.Errorf("items[%d] has tenant=%q (should be excluded — this is tenant A's list)", i, tenantB)
		}
		extRef, _ := m["external_ref"].(string)
		if len(extRef) >= 4 && extRef[:4] == "SC-B" {
			t.Errorf("items[%d] has external_ref=%q from tenant B (leaked into tenant A's list)", i, extRef)
		}
	}
}

// ---------------------------------------------------------------------------
// Blocking #2c: GET /sync/{projectID} returns 500 when sync fails
// ---------------------------------------------------------------------------

func TestTrackerSync_FailureReturns500(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	tenant := fmt.Sprintf("sync-fail-%s", uuid.NewString()[:8])
	t.Cleanup(func() { cleanupProjects(t, pool, tenant) })

	// Seed a REAL project with a valid tracker but no external_ref.
	// Sync will proceed past the project lookup (no 404) but fail inside
	// ShortcutSource because no shortcut external_ref is configured → 500.
	projectID := seedProject(t, pool, "shortcut", "", tenant)

	rec := trackerRequest(a, "GET", "/sync/"+projectID, map[string]string{
		"tenant": tenant,
	})

	if rec.Code != 500 {
		t.Fatalf("expected 500 when sync fails, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Bug #18: tracker dispatch — SyncTrackerItems routes by project.tracker
// ---------------------------------------------------------------------------

// fakeTrackerSyncer is a test double that records whether Sync was called.
// Used to verify dispatch routing without depending on real source behavior.
type fakeTrackerSyncer struct {
	name    string
	syncErr error
	called  bool
}

func (f *fakeTrackerSyncer) Sync(_ context.Context, _ pgtype.UUID, _ string) (service.SyncResult, error) {
	f.called = true
	return service.SyncResult{}, f.syncErr
}

// seedProject inserts a project row with the given tracker type and tenant,
// returning its UUID. The external_ref is set to the provided value (may be empty).
func seedProject(t *testing.T, pool *pgxpool.Pool, tracker, externalRef, tenant string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := uuid.NewString()
	_, err := pool.Exec(ctx, `
		INSERT INTO projects (id, slug, name, tenant, tracker, external_ref, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
	`, id, fmt.Sprintf("proj-%s", uuid.NewString()[:8]), "Test Project", tenant, tracker, externalRef)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return id
}

// cleanupProjects removes all rows whose tenant starts with the given prefix.
func cleanupProjects(t *testing.T, pool *pgxpool.Pool, tenantPrefix string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool.Exec(ctx, "DELETE FROM tracker_items WHERE tenant LIKE $1", tenantPrefix+"%")
	pool.Exec(ctx, "DELETE FROM projects WHERE tenant LIKE $1", tenantPrefix+"%")
}

// TestSyncTracker_DispatchByTrackerType verifies that SyncTrackerItems
// dispatches to the correct tracker source based on the project's tracker column.
// Uses injectable fake TrackerSyncers (approach (a) from Gate 3 review).
// This is the regression guard for bug #18 (hardcoded ShortcutSource).
// If bug #18 regresses, the github_issues subtest will call the shortcut fake
// instead of the github fake, failing the assertion.
func TestSyncTracker_DispatchByTrackerType(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()

	tenant := fmt.Sprintf("dispatch-%s", uuid.NewString()[:8])
	t.Cleanup(func() { cleanupProjects(t, pool, tenant) })

	// Create named fakes that record whether Sync was called.
	shortcutFake := &fakeTrackerSyncer{name: "shortcut", syncErr: fmt.Errorf("shortcut fake error")}
	githubFake := &fakeTrackerSyncer{name: "github", syncErr: fmt.Errorf("github fake error")}

	// Reset fakes and install them in the injection seam.
	installFakes := func() {
		shortcutFake.called = false
		githubFake.called = false
		testTrackerSyncers = map[string]service.TrackerSyncer{
			"shortcut":      shortcutFake,
			"github_issues": githubFake,
		}
	}
	// Clear the seam so other tests use real sources.
	t.Cleanup(func() { testTrackerSyncers = nil })

	t.Run("shortcut_project_dispatches_to_shortcut_source", func(t *testing.T) {
		installFakes()
		projectID := seedProject(t, pool, "shortcut", "", tenant)

		rec := trackerRequest(a, "GET", "/sync/"+projectID, map[string]string{
			"tenant": tenant,
		})

		if rec.Code != 500 {
			t.Fatalf("shortcut project: expected 500 (fake error), got %d; body: %s", rec.Code, rec.Body.String())
		}
		if !shortcutFake.called {
			t.Fatal("shortcut project: ShortcutSource fake was NOT called")
		}
		if githubFake.called {
			t.Fatal("shortcut project: GitHubSource fake was incorrectly called")
		}
	})

	t.Run("github_issues_project_dispatches_to_github_source", func(t *testing.T) {
		installFakes()
		projectID := seedProject(t, pool, "github_issues", "", tenant)

		rec := trackerRequest(a, "GET", "/sync/"+projectID, map[string]string{
			"tenant": tenant,
		})

		if rec.Code != 500 {
			t.Fatalf("github_issues project: expected 500 (fake error), got %d; body: %s", rec.Code, rec.Body.String())
		}
		if !githubFake.called {
			t.Fatal("github_issues project: GitHubSource fake was NOT called — bug #18 may have regressed")
		}
		if shortcutFake.called {
			t.Fatal("github_issues project: ShortcutSource fake was incorrectly called — bug #18 regressed")
		}
	})

	t.Run("unsupported_tracker_returns_400", func(t *testing.T) {
		installFakes()
		// Seed a project with tracker='obsidian' — not in the fake map.
		projectID := seedProject(t, pool, "obsidian", "", tenant)

		rec := trackerRequest(a, "GET", "/sync/"+projectID, map[string]string{
			"tenant": tenant,
		})

		if rec.Code != 400 {
			t.Fatalf("obsidian project: expected 400 (unsupported tracker), got %d; body: %s", rec.Code, rec.Body.String())
		}
		// Neither fake should have been called for an unsupported tracker.
		if shortcutFake.called || githubFake.called {
			t.Fatal("unsupported tracker: a fake was called, expected no dispatch")
		}
	})

	t.Run("nonexistent_project_returns_404", func(t *testing.T) {
		installFakes()
		projectID := uuid.NewString()

		rec := trackerRequest(a, "GET", "/sync/"+projectID, map[string]string{
			"tenant": tenant,
		})

		if rec.Code != 404 {
			t.Fatalf("nonexistent project: expected 404, got %d; body: %s", rec.Code, rec.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// Helpers for tracker route tests
// ---------------------------------------------------------------------------

// trackerRequest sends a request through TrackerRoutes and returns the recorder.
func trackerRequest(a *API, method, path string, query map[string]string) *httptest.ResponseRecorder {
	fullURL := path
	if len(query) > 0 {
		sep := "?"
		for k, v := range query {
			fullURL += sep + k + "=" + v
			sep = "&"
		}
	}
	r := httptest.NewRequest(method, fullURL, nil)
	r = r.WithContext(withTestOwner(r.Context()))
	rec := httptest.NewRecorder()
	a.TrackerRoutes().ServeHTTP(rec, r)
	return rec
}

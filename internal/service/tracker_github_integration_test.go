package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

// ---------------------------------------------------------------------------
// Real-PG integration tests for GitHubSource (WP-F).
// Skip-guarded on AOS_TEST_DATABASE_URL — mirrors against actual Postgres
// to prove ON CONFLICT dedup, synced_at population, and tenant isolation
// via the real SQL predicates (not fakes).
//
// Run: AOS_TEST_DATABASE_URL="postgres://aos_test@localhost:15432/aos_test?sslmode=disable" \
//       go test -run TestIntegrationGitHub -v ./internal/service/
// ---------------------------------------------------------------------------

func getGitHubTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AOS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test DB: %v", err)
	}
	t.Cleanup(func() {
		// Clean up all test rows by tenant prefix after each test.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, "DELETE FROM tracker_items WHERE tenant LIKE 'gh-integ-%'")
		_, _ = pool.Exec(ctx, "DELETE FROM projects WHERE tenant LIKE 'gh-integ-%'")
		pool.Close()
	})
	return pool
}

// seedGitHubProject inserts a project row with tracker="github_issues" and the
// given external_ref (owner/repo). Returns the project ID.
func seedGitHubProject(t *testing.T, pool *pgxpool.Pool, tenant, slug, externalRef string) pgtype.UUID {
	t.Helper()
	projectID := mustParseUUID(uuid.NewString())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `
		INSERT INTO projects (id, slug, name, tenant, tracker, external_ref, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'github_issues', $5, NOW(), NOW())
		ON CONFLICT (id) DO NOTHING
	`, projectID, slug, "Integration Test: "+slug, tenant, externalRef)
	if err != nil {
		t.Fatalf("seed github project: %v", err)
	}
	return projectID
}

// TestIntegrationGitHub_SyncRoundTrip verifies the full GitHubSource.Sync → List
// pipeline against real Postgres:
//   - Seeds a github_issues project
//   - Syncs with httptest GitHub stub
//   - Asserts rows land in tracker_items with #<n> external_ref and populated synced_at
func TestIntegrationGitHub_SyncRoundTrip(t *testing.T) {
	pool := getGitHubTestDB(t)
	queries := db.New(pool)
	log := slog.Default()

	tenant := fmt.Sprintf("gh-integ-sync-%s", uuid.NewString()[:8])
	projectID := seedGitHubProject(t, pool, tenant, "agent-os", "tim4net/agent-os")

	// Stub GitHub API with 3 issues.
	issues := []githubIssueEnvelope{
		testGitHubIssue(14, "WP-F: GitHub Issues tracker", "open",
			"https://github.com/tim4net/agent-os/issues/14", "wave:2"),
		testGitHubIssue(10, "WP-A2: Durable ingest-key store", "open",
			"https://github.com/tim4net/agent-os/issues/10"),
		testGitHubIssue(7, "WP-A: Generic work-event ingestion", "closed",
			"https://github.com/tim4net/agent-os/issues/7"),
	}
	issues[0].Body = "Second tracker source..."
	issues[0].User = &githubUser{Login: "tim4net"}

	srv := stubGitHubServer(issues)
	defer srv.Close()

	src := NewGitHubSourceWithClient(queries, &GitHubClient{
		apiToken: "test-token",
		client:   srv.Client(),
		baseURL:  srv.URL,
		log:      log,
	}, log)

	// Sync.
	ctx := context.Background()
	result, err := src.Sync(ctx, projectID, tenant)
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if result.Synced != 3 {
		t.Errorf("Synced=%d, want 3", result.Synced)
	}
	if result.Failed != 0 {
		t.Errorf("Failed=%d, want 0", result.Failed)
	}

	// List — prove rows landed in real DB with correct external_refs and synced_at.
	items, err := src.List(ctx, projectID, tenant, 50, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("List returned %d items, want 3", len(items))
	}

	// Build a set of external_refs for assertion.
	refSet := make(map[string]bool)
	for _, it := range items {
		refSet[it.ExternalRef] = true
		// Assert synced_at is populated (non-zero).
		if it.SyncedAt.IsZero() {
			t.Errorf("item %s has zero synced_at", it.ExternalRef)
		}
		// Assert canonical_url is populated.
		if it.CanonicalURL == "" {
			t.Errorf("item %s has empty canonical_url", it.ExternalRef)
		}
		// Assert item_type is "task".
		if it.ItemType != "task" {
			t.Errorf("item %s has item_type=%q, want %q", it.ExternalRef, it.ItemType, "task")
		}
	}
	for _, want := range []string{"#14", "#10", "#7"} {
		if !refSet[want] {
			t.Errorf("missing external_ref %q in List results", want)
		}
	}
}

// TestIntegrationGitHub_SyncIdempotency runs Sync twice and asserts the row
// count is stable — proves the real ON CONFLICT (project_id, external_ref)
// dedup works, unlike the fake which appends.
func TestIntegrationGitHub_SyncIdempotency(t *testing.T) {
	pool := getGitHubTestDB(t)
	queries := db.New(pool)
	log := slog.Default()

	tenant := fmt.Sprintf("gh-integ-dedup-%s", uuid.NewString()[:8])
	projectID := seedGitHubProject(t, pool, tenant, "agent-os", "tim4net/agent-os")

	issues := []githubIssueEnvelope{
		testGitHubIssue(1, "First issue", "open", "https://github.com/tim4net/agent-os/issues/1"),
		testGitHubIssue(2, "Second issue", "closed", "https://github.com/tim4net/agent-os/issues/2"),
	}

	srv := stubGitHubServer(issues)
	defer srv.Close()

	src := NewGitHubSourceWithClient(queries, &GitHubClient{
		apiToken: "test-token",
		client:   srv.Client(),
		baseURL:  srv.URL,
		log:      log,
	}, log)

	ctx := context.Background()

	// First sync.
	result1, err := src.Sync(ctx, projectID, tenant)
	if err != nil {
		t.Fatalf("first Sync error: %v", err)
	}
	if result1.Synced != 2 {
		t.Errorf("first Synced=%d, want 2", result1.Synced)
	}

	// Second sync (same data).
	result2, err := src.Sync(ctx, projectID, tenant)
	if err != nil {
		t.Fatalf("second Sync error: %v", err)
	}
	if result2.Synced != 2 {
		t.Errorf("second Synced=%d, want 2", result2.Synced)
	}

	// Row count must be exactly 2 — ON CONFLICT dedup, not append.
	items, err := src.List(ctx, projectID, tenant, 50, 0)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("after 2 syncs, List returned %d items — ON CONFLICT dedup broken (want 2)", len(items))
	}
}

// TestIntegrationGitHub_TenantIsolation seeds items for two tenants under the
// same project and asserts that List for one tenant returns zero of the
// other tenant's rows — proves the real WHERE ... AND tenant=$2 SQL predicate.
func TestIntegrationGitHub_TenantIsolation(t *testing.T) {
	pool := getGitHubTestDB(t)
	log := slog.Default()

	tenantA := fmt.Sprintf("gh-integ-iso-a-%s", uuid.NewString()[:8])
	tenantB := fmt.Sprintf("gh-integ-iso-b-%s", uuid.NewString()[:8])
	sharedProjectID := mustParseUUID(uuid.NewString())

	// Seed a shared project row — both tenants share the same project_id.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, err := pool.Exec(ctx, `
		INSERT INTO projects (id, slug, name, tenant, tracker, external_ref, created_at, updated_at)
		VALUES ($1, 'shared-proj', 'Shared', $2, 'github_issues', 'tim4net/agent-os', NOW(), NOW())
		ON CONFLICT (id) DO NOTHING
	`, sharedProjectID, tenantA)
	if err != nil {
		cancel()
		t.Fatalf("seed project A: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO projects (id, slug, name, tenant, tracker, external_ref, created_at, updated_at)
		VALUES ($1, 'shared-proj', 'Shared', $2, 'github_issues', 'tim4net/agent-os', NOW(), NOW())
		ON CONFLICT (id) DO NOTHING
	`, sharedProjectID, tenantB)
	if err != nil {
		cancel()
		t.Fatalf("seed project B: %v", err)
	}
	cancel()

	// Sync tenant A (2 issues).
	queriesA := db.New(pool)
	srvA := stubGitHubServer([]githubIssueEnvelope{
		testGitHubIssue(100, "Tenant A issue", "open", "https://github.com/tim4net/agent-os/issues/100"),
		testGitHubIssue(101, "Another A issue", "open", "https://github.com/tim4net/agent-os/issues/101"),
	})
	defer srvA.Close()
	srcA := NewGitHubSourceWithClient(queriesA, &GitHubClient{
		apiToken: "test-token", client: srvA.Client(), baseURL: srvA.URL, log: log,
	}, log)

	_, err = srcA.Sync(context.Background(), sharedProjectID, tenantA)
	if err != nil {
		t.Fatalf("Sync tenant A: %v", err)
	}

	// Sync tenant B (1 issue, different number).
	queriesB := db.New(pool)
	srvB := stubGitHubServer([]githubIssueEnvelope{
		testGitHubIssue(200, "Tenant B issue", "closed", "https://github.com/tim4net/agent-os/issues/200"),
	})
	defer srvB.Close()
	srcB := NewGitHubSourceWithClient(queriesB, &GitHubClient{
		apiToken: "test-token", client: srvB.Client(), baseURL: srvB.URL, log: log,
	}, log)

	_, err = srcB.Sync(context.Background(), sharedProjectID, tenantB)
	if err != nil {
		t.Fatalf("Sync tenant B: %v", err)
	}

	// List tenant A — must NOT include tenant B's #200.
	itemsA, err := srcA.List(context.Background(), sharedProjectID, tenantA, 50, 0)
	if err != nil {
		t.Fatalf("List tenant A: %v", err)
	}
	if len(itemsA) != 2 {
		t.Fatalf("List(tenantA) returned %d items, want 2", len(itemsA))
	}
	for _, it := range itemsA {
		if it.ExternalRef == "#200" {
			t.Errorf("tenant A list leaked tenant B item %q", it.ExternalRef)
		}
		if it.Tenant != tenantA {
			t.Errorf("tenant A list returned item with tenant=%q", it.Tenant)
		}
	}

	// List tenant B — must NOT include tenant A's #100 or #101.
	itemsB, err := srcB.List(context.Background(), sharedProjectID, tenantB, 50, 0)
	if err != nil {
		t.Fatalf("List tenant B: %v", err)
	}
	if len(itemsB) != 1 {
		t.Fatalf("List(tenantB) returned %d items, want 1", len(itemsB))
	}
	if itemsB[0].ExternalRef != "#200" {
		t.Errorf("tenant B item has external_ref=%q, want %q", itemsB[0].ExternalRef, "#200")
	}
	if itemsB[0].Tenant != tenantB {
		t.Errorf("tenant B list returned item with tenant=%q", itemsB[0].Tenant)
	}
}

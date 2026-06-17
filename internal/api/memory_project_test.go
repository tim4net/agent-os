package api

// Integration tests for per-project memory_index isolation.
//
// These tests prove AC-6/AC-7/AC-10: that SearchMemory with a project_id
// filter returns ONLY rows tagged with that project, with zero cross-project
// leakage — the core RAG isolation guarantee of issue #93.
//
// They require a throwaway Postgres with all migrations applied.  They skip
// automatically unless AOS_TEST_DSN is set, keeping the unit suite hermetic:
//
//	AOS_TEST_DSN=postgres://test:***@localhost:55432/test?sslmode=disable \
//	  go test ./internal/api/ -run 'TestMemoryProjectIsolation|TestSearchMemoryWithProjectFilter' -count=1
//
// NEVER run these against the live agent-os-db.

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

// setupMemoryTestDB connects to the test Postgres (via AOS_TEST_DSN), applies
// a clean slate to the tables this suite touches, and returns a db.Queries +
// raw pool.  It creates two throwaway projects ("alpha" and "beta") whose UUIDs
// are returned for use by individual tests.
func setupMemoryTestDB(t *testing.T) (queries *db.Queries, pool *pgxpool.Pool, alphaID, betaID pgtype.UUID) {
	t.Helper()
	dsn := os.Getenv("AOS_TEST_DSN")
	if dsn == "" {
		t.Skip("AOS_TEST_DSN not set — skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	// Clean slate — truncate in dependency order.
	if _, err := pool.Exec(ctx, "TRUNCATE memory_index CASCADE"); err != nil {
		t.Fatalf("truncate memory_index (is the schema migrated?): %v", err)
	}

	// Create two throwaway projects and capture their UUIDs.
	alphaID = scanUUID(t, pool, ctx, "alpha")
	betaID = scanUUID(t, pool, ctx, "beta")

	queries = db.New(pool)
	return queries, pool, alphaID, betaID
}

// scanUUID inserts (or reuses) a project row with the given slug and returns
// its UUID.
func scanUUID(t *testing.T, pool *pgxpool.Pool, ctx context.Context, slug string) pgtype.UUID {
	t.Helper()
	// Delete any stale project with this slug first, then insert fresh.
	if _, err := pool.Exec(ctx, "DELETE FROM projects WHERE slug = $1", slug); err != nil {
		t.Fatalf("delete stale project %q: %v", slug, err)
	}
	var id pgtype.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, tenant)
		VALUES ($1, $1, 'personal')
		RETURNING id`, slug).Scan(&id)
	if err != nil {
		t.Fatalf("insert project %q (is the schema migrated?): %v", slug, err)
	}
	return id
}

// TestMemoryProjectIsolation proves AC-10: upsert notes under two distinct
// projects, search scoped to ONE project_id, and confirm that zero results
// from the other project leak through.
func TestMemoryProjectIsolation(t *testing.T) {
	queries, _, alphaID, betaID := setupMemoryTestDB(t)
	ctx := context.Background()

	// Upsert a note under project alpha.
	if _, err := queries.UpsertMemory(ctx, db.UpsertMemoryParams{
		FilePath:  "projects/alpha/architecture.md",
		Title:     pgtype.Text{String: "Alpha Architecture", Valid: true},
		Content:   pgtype.Text{String: "Alpha project uses a microservice approach for the payment gateway.", Valid: true},
		Tags:      []string{},
		ProjectID: alphaID,
	}); err != nil {
		t.Fatalf("upsert alpha note: %v", err)
	}

	// Upsert a note under project beta — same topic keyword to tempt leakage.
	if _, err := queries.UpsertMemory(ctx, db.UpsertMemoryParams{
		FilePath:  "projects/beta/architecture.md",
		Title:     pgtype.Text{String: "Beta Architecture", Valid: true},
		Content:   pgtype.Text{String: "Beta project uses a microservice approach for the payment gateway too.", Valid: true},
		Tags:      []string{},
		ProjectID: betaID,
	}); err != nil {
		t.Fatalf("upsert beta note: %v", err)
	}

	// Upsert a note with NO project_id (NULL) — should never appear when a
	// specific project_id filter is active.
	if _, err := queries.UpsertMemory(ctx, db.UpsertMemoryParams{
		FilePath:  "inbox/unfiled.md",
		Title:     pgtype.Text{String: "Unfiled", Valid: true},
		Content:   pgtype.Text{String: "The payment gateway microservice approach is interesting.", Valid: true},
		Tags:      []string{},
		ProjectID: pgtype.UUID{}, // Valid=false → NULL
	}); err != nil {
		t.Fatalf("upsert unfiled note: %v", err)
	}

	// Search scoped to alpha — the query term matches all three rows' content,
	// so without the filter all three would return.  With the filter we expect
	// ONLY the alpha row.
	results, err := queries.SearchMemory(ctx, db.SearchMemoryParams{
		WebsearchToTsquery: "payment gateway microservice",
		Limit:              10,
		ProjectID:          alphaID,
	})
	if err != nil {
		t.Fatalf("SearchMemory(alpha): %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result for project alpha, got %d (cross-project leakage!): %+v", len(results), results)
	}
	if results[0].FilePath != "projects/alpha/architecture.md" {
		t.Fatalf("alpha search returned wrong file: %q", results[0].FilePath)
	}
	if !results[0].ProjectID.Valid || results[0].ProjectID != alphaID {
		t.Fatalf("alpha result has wrong project_id: %v", results[0].ProjectID)
	}

	// Search scoped to beta — symmetric check.
	results, err = queries.SearchMemory(ctx, db.SearchMemoryParams{
		WebsearchToTsquery: "payment gateway microservice",
		Limit:              10,
		ProjectID:          betaID,
	})
	if err != nil {
		t.Fatalf("SearchMemory(beta): %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for project beta, got %d: %+v", len(results), results)
	}
	if results[0].FilePath != "projects/beta/architecture.md" {
		t.Fatalf("beta search returned wrong file: %q", results[0].FilePath)
	}
}

// TestSearchMemoryWithProjectFilter proves AC-6: that the project_id IS NULL
// branch of the query returns ALL rows (the "no filter" / global search mode),
// while a non-NULL project_id returns only that project's rows.
func TestSearchMemoryWithProjectFilter(t *testing.T) {
	queries, _, alphaID, betaID := setupMemoryTestDB(t)
	ctx := context.Background()

	// Seed two rows, one per project, sharing a keyword.
	for _, p := range []struct {
		path string
		pid  pgtype.UUID
		body string
	}{
		{"projects/alpha/deploy.md", alphaID, "Continuous deployment pipeline for alpha."},
		{"projects/beta/deploy.md", betaID, "Continuous deployment pipeline for beta."},
	} {
		if _, err := queries.UpsertMemory(ctx, db.UpsertMemoryParams{
			FilePath:  p.path,
			Title:     pgtype.Text{String: p.path, Valid: true},
			Content:   pgtype.Text{String: p.body, Valid: true},
			Tags:      []string{},
			ProjectID: p.pid,
		}); err != nil {
			t.Fatalf("upsert %q: %v", p.path, err)
		}
	}

	// ── No filter (project_id IS NULL) → both rows returned ──────────────
	all, err := queries.SearchMemory(ctx, db.SearchMemoryParams{
		WebsearchToTsquery: "continuous deployment",
		Limit:              10,
		ProjectID:          pgtype.UUID{}, // Valid=false → IS NULL branch
	})
	if err != nil {
		t.Fatalf("SearchMemory(no filter): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("unfiltered search should return 2 rows, got %d", len(all))
	}

	// ── Filter to alpha → only alpha row ─────────────────────────────────
	alphaOnly, err := queries.SearchMemory(ctx, db.SearchMemoryParams{
		WebsearchToTsquery: "continuous deployment",
		Limit:              10,
		ProjectID:          alphaID,
	})
	if err != nil {
		t.Fatalf("SearchMemory(alpha filter): %v", err)
	}
	if len(alphaOnly) != 1 || alphaOnly[0].FilePath != "projects/alpha/deploy.md" {
		t.Fatalf("alpha-filtered search should return only alpha row, got %+v", alphaOnly)
	}

	// ── Filter to beta → only beta row ───────────────────────────────────
	betaOnly, err := queries.SearchMemory(ctx, db.SearchMemoryParams{
		WebsearchToTsquery: "continuous deployment",
		Limit:              10,
		ProjectID:          betaID,
	})
	if err != nil {
		t.Fatalf("SearchMemory(beta filter): %v", err)
	}
	if len(betaOnly) != 1 || betaOnly[0].FilePath != "projects/beta/deploy.md" {
		t.Fatalf("beta-filtered search should return only beta row, got %+v", betaOnly)
	}
}

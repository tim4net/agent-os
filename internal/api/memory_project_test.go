package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// newTestAPIForMemory creates a test API backed by a real database.
func newTestAPIForMemory(t *testing.T) (*API, *pgxpool.Pool) {
	t.Helper()
	pool := getTestDB(t)

	// Clean up test data
	_, _ = pool.Exec(context.Background(), "TRUNCATE memory_index, projects CASCADE")

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "TRUNCATE memory_index, projects CASCADE")
		pool.Close()
	})

	queries := db.New(pool)
	memory := NewMemoryAPI(queries, "/tmp/nonexistent-obsidian", "", "")
	return &API{
		queries: queries,
		pool:    pool,
		memory:  memory,
	}, pool
}

// TestMemoryProjectIsolation verifies that search with a project_id filter
// returns only rows belonging to that project and zero cross-project leakage.
func TestMemoryProjectIsolation(t *testing.T) {
	api, pool := newTestAPIForMemory(t)
	queries := db.New(pool)
	ctx := context.Background()

	// Create two projects
	project1ID := pgtype.UUID{Valid: true}
	copy(project1ID.Bytes[:], uuid.New().Bytes())
	project2ID := pgtype.UUID{Valid: true}
	copy(project2ID.Bytes[:], uuid.New().Bytes())

	// Use raw SQL to insert test projects (owner_id is required)
	ownerID := pgtype.UUID{Valid: true}
	copy(ownerID.Bytes[:], [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})

	_, err := pool.Exec(ctx,
		"INSERT INTO projects (id, slug, name, tenant, owner_id) VALUES ($1, $2, $3, 'test', $4)",
		project1ID, "project-alpha", "Project Alpha", ownerID)
	if err != nil {
		t.Fatalf("create project1: %v", err)
	}
	_, err = pool.Exec(ctx,
		"INSERT INTO projects (id, slug, name, tenant, owner_id) VALUES ($1, $2, $3, 'test', $4)",
		project2ID, "project-beta", "Project Beta", ownerID)
	if err != nil {
		t.Fatalf("create project2: %v", err)
	}

	// Upsert memory entries for both projects with common keywords
	commonContent := "architecture design patterns microservices testing"
	_, err = queries.UpsertMemory(ctx, db.UpsertMemoryParams{
		FilePath:  "projects/alpha/notes/architecture.md",
		Title:     pgtype.Text{String: "Alpha Architecture", Valid: true},
		Content:   pgtype.Text{String: "Alpha project: " + commonContent, Valid: true},
		Tags:      []string{},
		OwnerID:   ownerID,
		ProjectID: project1ID,
	})
	if err != nil {
		t.Fatalf("upsert alpha memory: %v", err)
	}

	_, err = queries.UpsertMemory(ctx, db.UpsertMemoryParams{
		FilePath:  "projects/beta/notes/architecture.md",
		Title:     pgtype.Text{String: "Beta Architecture", Valid: true},
		Content:   pgtype.Text{String: "Beta project: " + commonContent, Valid: true},
		Tags:      []string{},
		OwnerID:   ownerID,
		ProjectID: project2ID,
	})
	if err != nil {
		t.Fatalf("upsert beta memory: %v", err)
	}

	// Also insert an unscoped (project_id = NULL) entry
	_, err = queries.UpsertMemory(ctx, db.UpsertMemoryParams{
		FilePath:  "personal/notes.md",
		Title:     pgtype.Text{String: "Personal Notes", Valid: true},
		Content:   pgtype.Text{String: "Personal: " + commonContent, Valid: true},
		Tags:      []string{},
		OwnerID:   ownerID,
		ProjectID: pgtype.UUID{}, // NULL
	})
	if err != nil {
		t.Fatalf("upsert unscoped memory: %v", err)
	}

	// Wait a moment for full-text index to be ready (usually instant)
	time.Sleep(100 * time.Millisecond)

	// Search scoped to project1 — should get only alpha entry
	results, err := queries.SearchMemory(ctx, db.SearchMemoryParams{
		OwnerID:            ownerID,
		WebsearchToTsquery: "architecture design patterns",
		Limit:              10,
		ProjectID:          project1ID,
	})
	if err != nil {
		t.Fatalf("search project1: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results for project1 search, got 0")
	}
	for _, r := range results {
		if r.ProjectID.Valid && r.ProjectID != project1ID {
			t.Errorf("cross-project leakage: got project_id=%v, want %v (file=%s)",
				r.ProjectID, project1ID, r.FilePath)
		}
		if !r.ProjectID.Valid {
			t.Errorf("cross-project leakage: got NULL project_id for scoped search (file=%s)",
				r.FilePath)
		}
	}

	// Search scoped to project2 — should get only beta entry
	results2, err := queries.SearchMemory(ctx, db.SearchMemoryParams{
		OwnerID:            ownerID,
		WebsearchToTsquery: "architecture design patterns",
		Limit:              10,
		ProjectID:          project2ID,
	})
	if err != nil {
		t.Fatalf("search project2: %v", err)
	}

	if len(results2) == 0 {
		t.Fatal("expected results for project2 search, got 0")
	}
	for _, r := range results2 {
		if r.ProjectID.Valid && r.ProjectID != project2ID {
			t.Errorf("cross-project leakage: got project_id=%v, want %v (file=%s)",
				r.ProjectID, project2ID, r.FilePath)
		}
		if !r.ProjectID.Valid {
			t.Errorf("cross-project leakage: got NULL project_id for scoped search (file=%s)",
				r.FilePath)
		}
	}

	// Search with no project_id — should get all three
	resultsAll, err := queries.SearchMemory(ctx, db.SearchMemoryParams{
		OwnerID:            ownerID,
		WebsearchToTsquery: "architecture design patterns",
		Limit:              10,
		ProjectID:          pgtype.UUID{}, // NULL = no filter
	})
	if err != nil {
		t.Fatalf("search all: %v", err)
	}

	if len(resultsAll) < 3 {
		t.Errorf("expected at least 3 results for unscoped search, got %d", len(resultsAll))
	}
}

// TestMemorySearchHTTPProjectFilter tests the /api/memory/search endpoint
// with the optional project_id query parameter.
func TestMemorySearchHTTPProjectFilter(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	api, pool := newTestAPIForMemory(t)
	queries := db.New(pool)
	ctx := context.Background()

	// Create a project
	projectID := pgtype.UUID{Valid: true}
	copy(projectID.Bytes[:], uuid.New().Bytes())

	ownerID := pgtype.UUID{Valid: true}
	copy(ownerID.Bytes[:], [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})

	_, err := pool.Exec(ctx,
		"INSERT INTO projects (id, slug, name, tenant, owner_id) VALUES ($1, $2, $3, 'test', $4)",
		projectID, "test-project", "Test Project", ownerID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Insert test data
	_, err = queries.UpsertMemory(ctx, db.UpsertMemoryParams{
		FilePath:  "test-project/notes.md",
		Title:     pgtype.Text{String: "Test Note", Valid: true},
		Content:   pgtype.Text{String: "quantum computing algorithms and quantum error correction", Valid: true},
		Tags:      []string{},
		OwnerID:   ownerID,
		ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("upsert memory: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Search with project_id filter via HTTP
	url := fmt.Sprintf("/api/memory/search?q=quantum+computing&project_id=%s", projectID.String())
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var hits []SearchHit
	if err := json.NewDecoder(rec.Body).Decode(&hits); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
}

// TestDeriveProjectIDIntegration tests the path prefix derivation logic
// through the indexer with a real project cache.
func TestDeriveProjectIDIntegration(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	pool := getTestDB(t)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "TRUNCATE projects CASCADE")
		pool.Close()
	})

	queries := db.New(pool)
	ctx := context.Background()

	ownerID := pgtype.UUID{Valid: true}
	copy(ownerID.Bytes[:], [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})

	// Create test projects
	_, err := pool.Exec(ctx,
		"INSERT INTO projects (id, slug, name, tenant, owner_id) VALUES ($1, $2, $3, 'test', $4)",
		pgtype.UUID{Valid: true, Bytes: [16]byte{1}}, "riftwing", "Riftwing", ownerID)
	if err != nil {
		t.Fatalf("create riftwing project: %v", err)
	}
	_, err = pool.Exec(ctx,
		"INSERT INTO projects (id, slug, name, tenant, owner_id) VALUES ($1, $2, $3, 'test', $4)",
		pgtype.UUID{Valid: true, Bytes: [16]byte{2}}, "agent-os", "Agent OS", ownerID)
	if err != nil {
		t.Fatalf("create agent-os project: %v", err)
	}

	// Create indexer and refresh cache
	mi := service.NewMemoryIndexer(queries, nil, "/tmp/nonexistent")
	mi.WithProjectPathMappings([]service.ProjectPathMapping{
		{PathPrefix: "projects/riftwing", Slug: "riftwing"},
		{PathPrefix: "Riftwing", Slug: "riftwing"},
		{PathPrefix: "projects/agent-os", Slug: "agent-os"},
	})

	// Manually refresh the project cache (calls ListAllProjects)
	projects, err := queries.ListAllProjects(ctx)
	if err != nil {
		t.Fatalf("ListAllProjects: %v", err)
	}

	slugToID := make(map[string]pgtype.UUID)
	for _, p := range projects {
		slugToID[p.Slug] = p.ID
	}

	// Test path resolution
	tests := []struct {
		path    string
		wantSlug string
	}{
		{"projects/riftwing/design.md", "riftwing"},
		{"Riftwing/notes.md", "riftwing"},
		{"projects/agent-os/README.md", "agent-os"},
		{"personal/journal.md", ""},
	}

	for _, tt := range tests {
		// Manually call DeriveProjectID through reflection or exported method
		got := mi.DeriveProjectID(tt.path)
		if tt.wantSlug == "" {
			if got.Valid {
				t.Errorf("DeriveProjectID(%q): expected no match, got project UUID", tt.path)
			}
		} else {
			if !got.Valid {
				t.Errorf("DeriveProjectID(%q): expected match for %q, got no match", tt.path, tt.wantSlug)
			} else if got != slugToID[tt.wantSlug] {
				t.Errorf("DeriveProjectID(%q): got wrong project UUID for slug %q", tt.path, tt.wantSlug)
			}
		}
	}
}

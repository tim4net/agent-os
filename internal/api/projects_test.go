package api

// Tests for workspace (project) surfaces — issue #134.
//
// Two tiers:
//
//  1. Hermetic unit tests (always run): slugify, handler auth/validation guards,
//     and route registration — no database required.
//
//  2. Integration tests (skip unless AOS_TEST_DATABASE_URL is set): full CRUD,
//     the /surface aggregate, and per-project scoping/isolation for agents,
//     memory, and artifacts.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

// withChiParam injects a chi URL route param into ctx so handlers that call
// chi.URLParam work in unit tests without a full chi router.
func withChiParam(ctx context.Context, key, val string) context.Context {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return context.WithValue(ctx, chi.RouteCtxKey, rctx)
}

// ── helpers ─────────────────────────────────────────────────────────────────

// newWorkspaceTestAPI returns an API backed by the real test DB plus a temp
// artifacts dir. Skips when no test DB is configured.
func newWorkspaceTestAPI(t *testing.T) (*API, *pgxpool.Pool) {
	t.Helper()
	pool := getTestDB(t)
	queries := db.New(pool)
	a := &API{
		queries:   queries,
		pool:      pool,
		artifacts: NewArtifactAPI(queries, t.TempDir()),
		memory:    NewMemoryAPI(queries, t.TempDir(), "", ""),
	}
	return a, pool
}

// wsReq builds a request carrying the seed owner identity (mirrors renameReq).
func wsReq(method, path string, body []byte) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	return r.WithContext(withTestOwner(r.Context()))
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// uniqueSlug makes a project slug unique to this test run so concurrent or
// repeated runs never collide on the projects.slug UNIQUE constraint.
func uniqueSlug(t *testing.T) string {
	t.Helper()
	return "wp134-" + strings.ToLower(sanitizeForSlug(t.Name()))
}

func sanitizeForSlug(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			out = append(out, byte(r))
		} else if r >= 'A' && r <= 'Z' {
			out = append(out, byte(r-'A'+'a'))
		} else {
			out = append(out, '-')
		}
	}
	return strings.Trim(string(out), "-")
}

// ── 1. hermetic unit tests ───────────────────────────────────────────────────

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Agent OS":         "agent-os",
		"  Hello, World! ": "hello-world",
		"My---Project":     "my-project",
		"UPPER Case":       "upper-case",
		"!!!":              "",
		"":                 "",
	}
	for in, want := range cases {
		got := slugify(in)
		if got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestProjectHandlers_RequireOwner proves every workspace handler fails closed
// (401) when no owner identity is present — no DB needed since the guard runs
// before any query.
func TestProjectHandlers_RequireOwner(t *testing.T) {
	a := &API{}

	handlers := map[string]http.HandlerFunc{
		"ListWorkspaces":    a.ListWorkspaces,
		"CreateWorkspace":   a.CreateWorkspace,
		"GetWorkspace":      a.GetWorkspace,
		"UpdateWorkspace":   a.UpdateWorkspace,
		"WorkspaceSurface":  a.WorkspaceSurface,
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/projects/00000000-0000-0000-0000-000000000001", nil)
			// No owner injected.
			h(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s: expected 401, got %d (body=%s)", name, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestCreateWorkspace_Validation exercises request validation without a DB.
func TestCreateWorkspace_Validation(t *testing.T) {
	a := &API{}

	t.Run("empty name → 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := wsReq(http.MethodPost, "/api/projects", mustMarshal(t, CreateWorkspaceRequest{Name: "  "}))
		a.CreateWorkspace(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed JSON → 400", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := wsReq(http.MethodPost, "/api/projects", []byte("{not json"))
		a.CreateWorkspace(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

// TestGetWorkspace_InvalidID proves a non-UUID path param yields 400 (no DB).
func TestGetWorkspace_InvalidID(t *testing.T) {
	a := &API{}

	for _, h := range []http.HandlerFunc{a.GetWorkspace, a.WorkspaceSurface, a.UpdateWorkspace} {
		rec := httptest.NewRecorder()
		req := wsReq(http.MethodGet, "/api/projects/not-a-uuid", nil)
		// chi URL params aren't populated by httptest.NewRequest; simulate the
		// param via the request's route context.
		req = req.WithContext(withChiParam(req.Context(), "id", "not-a-uuid"))
		h(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for bad UUID, got %d (body=%s)", rec.Code, rec.Body.String())
		}
	}
}

// TestProjectRoutes_Registered proves the workspace subrouter is wired and
// dispatches to handlers (without depending on a database).
func TestProjectRoutes_Registered(t *testing.T) {
	a := &API{}
	r := a.ProjectRoutes()

	// GET / with no owner → 401 (proves the route exists and reaches the handler).
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET / without owner: expected 401, got %d", rec.Code)
	}

	// POST / with no owner → 401.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST / without owner: expected 401, got %d", rec.Code)
	}

	// GET /{id}/surface with no owner → 401 (proves nested route registered).
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/00000000-0000-0000-0000-000000000001/surface", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /{id}/surface without owner: expected 401, got %d", rec.Code)
	}
}

// ── 2. integration tests (DB-backed) ─────────────────────────────────────────
//
// These create real projects, agents, memory, and artifacts and prove:
//   - workspace CRUD works end-to-end,
//   - the /surface aggregate ties the three resources together,
//   - scoping is enforced and there is ZERO cross-project leakage (negative test).

// deleteProjectBySlug removes a project and its dependent memory_index rows.
// memory_index.project_id is ON DELETE NO ACTION (migration 026), so the
// referencing notes must go first. agents/artifacts use ON DELETE SET NULL
// (migration 029) and need no pre-clearing.
func deleteProjectBySlug(pool *pgxpool.Pool, slug string) {
	pool.Exec(context.Background(),
		"DELETE FROM memory_index WHERE project_id = (SELECT id FROM projects WHERE slug = $1)", slug)
	pool.Exec(context.Background(), "DELETE FROM projects WHERE slug = $1", slug)
}

// createWorkspaceViaAPI POSTs a workspace and returns the created project view.
// It is self-cleaning: it removes any stale same-slug project from a prior run
// before creating, and deletes the project (by slug) when the test ends.
func createWorkspaceViaAPI(t *testing.T, a *API, name string) projectView {
	t.Helper()
	derivedSlug := slugify(name)
	// Defensive: clear a stale project with this slug from a prior run.
	deleteProjectBySlug(a.pool, derivedSlug)

	rec := httptest.NewRecorder()
	body := mustMarshal(t, CreateWorkspaceRequest{Name: name})
	a.ProjectRoutes().ServeHTTP(rec, wsReq(http.MethodPost, "/", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace %q: expected 201, got %d (body=%s)", name, rec.Code, rec.Body.String())
	}
	var pv projectView
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatalf("decode created workspace: %v", err)
	}
	t.Cleanup(func() { deleteProjectBySlug(a.pool, pv.Slug) })
	return pv
}

func TestWorkspaceCRUD_Integration(t *testing.T) {
	a, pool := newWorkspaceTestAPI(t)
	ctx := context.Background()
	slug := uniqueSlug(t)
	// Ensure a clean slate for this slug.
	pool.Exec(ctx, "DELETE FROM projects WHERE slug = $1", slug)

	// ── Create ──────────────────────────────────────────────────────────────
	created := createWorkspaceViaAPI(t, a, "Acme "+slug)
	wantSlug := slugify("Acme " + slug)
	if created.Slug != wantSlug {
		t.Fatalf("expected slug %q (derived from name), got %q", wantSlug, created.Slug)
	}
	if created.Name == "" || created.Tracker != "agent_os_native" {
		t.Fatalf("unexpected workspace view: %+v", created)
	}
	id := created.ID.(string)
	if id == "" {
		t.Fatal("created workspace has empty ID")
	}

	// ── Duplicate slug → 409 ────────────────────────────────────────────────
	rec := httptest.NewRecorder()
	a.ProjectRoutes().ServeHTTP(rec, wsReq(http.MethodPost, "/", mustMarshal(t, CreateWorkspaceRequest{Name: "Acme " + slug})))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate slug: expected 409, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	// ── List ────────────────────────────────────────────────────────────────
	rec = httptest.NewRecorder()
	a.ProjectRoutes().ServeHTTP(rec, wsReq(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list workspaces: expected 200, got %d", rec.Code)
	}
	var list []projectView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, p := range list {
		if p.Slug == wantSlug {
			found = true
		}
	}
	if !found {
		t.Fatalf("created workspace %q not in list (%d items)", wantSlug, len(list))
	}

	// ── Get ─────────────────────────────────────────────────────────────────
	rec = httptest.NewRecorder()
	a.ProjectRoutes().ServeHTTP(rec, wsReq(http.MethodGet, "/"+id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get workspace: expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var got projectView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Slug != wantSlug {
		t.Fatalf("get returned wrong slug: got %q, want %q", got.Slug, wantSlug)
	}

	// ── Update repo_url ─────────────────────────────────────────────────────
	repo := "https://github.com/example/repo.git"
	rec = httptest.NewRecorder()
	a.ProjectRoutes().ServeHTTP(rec, wsReq(http.MethodPatch, "/"+id, mustMarshal(t, UpdateWorkspaceRequest{
		RepoURL: &repo,
	})))
	if rec.Code != http.StatusOK {
		t.Fatalf("update workspace: expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var updated projectView
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.RepoURL != repo {
		t.Fatalf("repo_url not updated: got %v", updated.RepoURL)
	}

	// ── Get unknown → 404 ───────────────────────────────────────────────────
	rec = httptest.NewRecorder()
	a.ProjectRoutes().ServeHTTP(rec, wsReq(http.MethodGet, "/00000000-0000-0000-0000-000000000099", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown workspace: expected 404, got %d", rec.Code)
	}
}

// TestWorkspaceSurface_Scoping_Integration is the core AC-2 test: it creates
// two workspaces, assigns agents/memory/artifacts to each, then proves the
// /surface endpoint returns ONLY that workspace's resources — no leakage.
func TestWorkspaceSurface_Scoping_Integration(t *testing.T) {
	a, pool := newWorkspaceTestAPI(t)
	ctx := context.Background()
	owner := testOwnerID()

	// Two distinct workspaces.
	alpha := createWorkspaceViaAPI(t, a, "Alpha "+uniqueSlug(t))
	beta := createWorkspaceViaAPI(t, a, "Beta "+uniqueSlug(t))

	var alphaID, betaID pgtype.UUID
	if err := alphaID.Scan(alpha.ID.(string)); err != nil {
		t.Fatal(err)
	}
	if err := betaID.Scan(beta.ID.(string)); err != nil {
		t.Fatal(err)
	}

	// ── Seed agents ─────────────────────────────────────────────────────────
	// Clean any stale agents with our test names first.
	pool.Exec(ctx, "DELETE FROM agents WHERE name IN ('alpha-bot','beta-bot')")
	alphaBot := mustCreateAgent(t, pool, owner, "alpha-bot")
	betaBot := mustCreateAgent(t, pool, owner, "beta-bot")
	if _, err := a.queries.SetAgentProject(ctx, db.SetAgentProjectParams{ID: alphaBot, ProjectID: alphaID, OwnerID: owner}); err != nil {
		t.Fatalf("set alpha agent project: %v", err)
	}
	if _, err := a.queries.SetAgentProject(ctx, db.SetAgentProjectParams{ID: betaBot, ProjectID: betaID, OwnerID: owner}); err != nil {
		t.Fatalf("set beta agent project: %v", err)
	}

	// ── Seed memory (one note per project) ──────────────────────────────────
	if _, err := a.queries.UpsertMemory(ctx, db.UpsertMemoryParams{
		OwnerID: owner, FilePath: "alpha/notes.md",
		Title: pgtype.Text{String: "Alpha", Valid: true},
		Content: pgtype.Text{String: "alpha project secret architecture details", Valid: true},
		Tags: []string{}, ProjectID: alphaID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.queries.UpsertMemory(ctx, db.UpsertMemoryParams{
		OwnerID: owner, FilePath: "beta/notes.md",
		Title: pgtype.Text{String: "Beta", Valid: true},
		Content: pgtype.Text{String: "beta project secret architecture details", Valid: true},
		Tags: []string{}, ProjectID: betaID,
	}); err != nil {
		t.Fatal(err)
	}

	// ── Seed artifacts (one per project) ────────────────────────────────────
	alphaArt := mustCreateArtifact(t, pool, owner)
	betaArt := mustCreateArtifact(t, pool, owner)
	if err := a.queries.SetArtifactProject(ctx, db.SetArtifactProjectParams{ID: alphaArt, ProjectID: alphaID, OwnerID: owner}); err != nil {
		t.Fatal(err)
	}
	if err := a.queries.SetArtifactProject(ctx, db.SetArtifactProjectParams{ID: betaArt, ProjectID: betaID, OwnerID: owner}); err != nil {
		t.Fatal(err)
	}

	// ── Alpha surface: exactly 1 agent, 1 memory, 1 artifact ────────────────
	surf := getSurface(t, a, alpha.ID.(string))
	if surf.Agents.Total != 1 {
		t.Fatalf("alpha agents: want 1, got %d", surf.Agents.Total)
	}
	if surf.Memory.Total != 1 {
		t.Fatalf("alpha memory: want 1, got %d", surf.Memory.Total)
	}
	if surf.Artifacts.Total != 1 {
		t.Fatalf("alpha artifacts: want 1, got %d", surf.Artifacts.Total)
	}
	// Negative: confirm the alpha surface does NOT contain the beta agent name.
	if hasAgentNamed(surf, "beta-bot") {
		t.Fatal("NEGATIVE FAIL: beta agent leaked into alpha workspace surface")
	}

	// ── Beta surface: symmetric ─────────────────────────────────────────────
	surfBeta := getSurface(t, a, beta.ID.(string))
	if surfBeta.Agents.Total != 1 || surfBeta.Memory.Total != 1 || surfBeta.Artifacts.Total != 1 {
		t.Fatalf("beta surface counts: agents=%d memory=%d artifacts=%d (all want 1)",
			surfBeta.Agents.Total, surfBeta.Memory.Total, surfBeta.Artifacts.Total)
	}
	if hasAgentNamed(surfBeta, "alpha-bot") {
		t.Fatal("NEGATIVE FAIL: alpha agent leaked into beta workspace surface")
	}
}

// TestListAgents_ProjectScoping_Integration proves the ?project_id= filter on
// GET /api/agents returns only that workspace's agents.
func TestListAgents_ProjectScoping_Integration(t *testing.T) {
	a, pool := newWorkspaceTestAPI(t)
	ctx := context.Background()
	owner := testOwnerID()

	pool.Exec(ctx, "DELETE FROM agents WHERE name IN ('ws-scope-a1','ws-scope-a2')")
	proj := createWorkspaceViaAPI(t, a, "Scope "+uniqueSlug(t))
	var pid pgtype.UUID
	if err := pid.Scan(proj.ID.(string)); err != nil {
		t.Fatal(err)
	}

	in := mustCreateAgent(t, pool, owner, "ws-scope-a1")
	mustCreateAgent(t, pool, owner, "ws-scope-a2") // not assigned to project
	if _, err := a.queries.SetAgentProject(ctx, db.SetAgentProjectParams{ID: in, ProjectID: pid, OwnerID: owner}); err != nil {
		t.Fatal(err)
	}

	// Scoped query → only the assigned agent.
	rec := httptest.NewRecorder()
	req := wsReq(http.MethodGet, "/?project_id="+proj.ID.(string), nil)
	a.ListAgents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var agents []agentView
	if err := json.Unmarshal(rec.Body.Bytes(), &agents); err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Name != "ws-scope-a1" {
		t.Fatalf("scoped agents: want exactly [ws-scope-a1], got %+v", agents)
	}
}

// TestListArtifacts_ProjectScoping_Integration proves the ?project_id= filter
// on GET /api/artifacts returns only that workspace's artifacts.
func TestListArtifacts_ProjectScoping_Integration(t *testing.T) {
	a, pool := newWorkspaceTestAPI(t)
	ctx := context.Background()
	owner := testOwnerID()

	proj := createWorkspaceViaAPI(t, a, "ArtScope "+uniqueSlug(t))
	var pid pgtype.UUID
	if err := pid.Scan(proj.ID.(string)); err != nil {
		t.Fatal(err)
	}

	assigned := mustCreateArtifact(t, pool, owner)
	mustCreateArtifact(t, pool, owner) // unassigned (global)
	if err := a.queries.SetArtifactProject(ctx, db.SetArtifactProjectParams{ID: assigned, ProjectID: pid, OwnerID: owner}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := wsReq(http.MethodGet, "/?project_id="+proj.ID.(string), nil)
	a.artifacts.ArtifactRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var resp listArtifactsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 {
		t.Fatalf("scoped artifacts: want 1, got %d", resp.Total)
	}
}

// ── small DB seed helpers ────────────────────────────────────────────────────

func mustCreateAgent(t *testing.T, pool *pgxpool.Pool, owner pgtype.UUID, name string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO agents (owner_id, name, display_name, harness, base_url, metadata, visible)
		 VALUES ($1, $2, $2, 'generic', 'http://test', '{}'::jsonb, true) RETURNING id`,
		owner, name).Scan(&id)
	if err != nil {
		t.Fatalf("create agent %q: %v", name, err)
	}
	return id
}

func mustCreateArtifact(t *testing.T, pool *pgxpool.Pool, owner pgtype.UUID) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO artifacts (owner_id, type, title, metadata)
		 VALUES ($1, 'text', 'test artifact', '{}'::jsonb) RETURNING id`,
		owner).Scan(&id)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	return id
}

func getSurface(t *testing.T, a *API, id string) WorkspaceSurface {
	t.Helper()
	rec := httptest.NewRecorder()
	a.ProjectRoutes().ServeHTTP(rec, wsReq(http.MethodGet, "/"+id+"/surface", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("surface: expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var s WorkspaceSurface
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode surface: %v", err)
	}
	return s
}

func hasAgentNamed(s WorkspaceSurface, name string) bool {
	for _, item := range s.Agents.Items {
		if m, ok := item.(map[string]any); ok {
			if n, _ := m["name"].(string); n == name {
				return true
			}
		}
	}
	return false
}

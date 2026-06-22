package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ──────────────────────────────────────────────────────────────────────────
// Workspace surfaces (issue #134)
//
// A "workspace" IS a project. This file adds the HTTP API that was missing:
// the ability to create / list / select a workspace, and a /surface endpoint
// that ties together the three per-project resources — agents, memory, and
// artifacts — into one scoped view. It builds directly on the project_id
// scoping that migration 026 already gave memory_index and that migration 029
// extends to agents + artifacts.
// ──────────────────────────────────────────────────────────────────────────

// projectView is the DTO for a workspace (project) in API responses.
type projectView struct {
	ID        any    `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Tenant    string `json:"tenant"`
	Tracker   string `json:"tracker"`
	RepoURL   any    `json:"repo_url"`
	CreatedAt any    `json:"created_at"`
	UpdatedAt any    `json:"updated_at"`
}

func projectToView(p db.Project) projectView {
	repoURL := ""
	if p.RepoUrl.Valid {
		repoURL = p.RepoUrl.String
	}
	return projectView{
		ID:        p.ID,
		Slug:      p.Slug,
		Name:      p.Name,
		Tenant:    p.Tenant,
		Tracker:   p.Tracker,
		RepoURL:   repoURL,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

func projectsToView(in []db.Project) []projectView {
	out := make([]projectView, 0, len(in))
	for _, p := range in {
		out = append(out, projectToView(p))
	}
	return out
}

// slugifyRe collapses runs of non-alphanumeric characters into a single '-'.
var slugifyRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify derives a URL-safe slug from an arbitrary name. Lower-cases,
// replaces non-alphanumeric runs with '-', and trims leading/trailing '-'.
// Returns "" for an all-symbol input.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugifyRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// CreateWorkspaceRequest is the JSON body for POST /api/projects.
type CreateWorkspaceRequest struct {
	Name    string `json:"name"`
	Slug    string `json:"slug"`     // optional; derived from name when empty
	RepoURL string `json:"repo_url"` // optional
	Tenant  string `json:"tenant"`   // optional; defaults to "personal"
}

// WorkspaceSurface is the aggregate view of a workspace: the counts and items
// of its three scoped resources (agents, memory, artifacts).
type WorkspaceSurface struct {
	Project   projectView              `json:"project"`
	Agents    surfaceBucket[any]       `json:"agents"`
	Memory    surfaceBucket[any]       `json:"memory"`
	Artifacts surfaceBucket[any]       `json:"artifacts"`
}

type surfaceBucket[T any] struct {
	Total int   `json:"total"`
	Items []T   `json:"items"`
}

// ProjectRoutes returns a Chi router for workspace (project) endpoints.
func (a *API) ProjectRoutes() http.Handler {
	r := chi.NewRouter()

	r.Get("/", a.ListWorkspaces)
	r.Post("/", a.CreateWorkspace)
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", a.GetWorkspace)
		r.Patch("/", a.UpdateWorkspace)
		r.Get("/surface", a.WorkspaceSurface)
	})

	return r
}

// ListWorkspaces handles GET /api/projects — lists all workspaces for the owner.
func (a *API) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	projects, err := a.queries.ListProjects(r.Context(), ownerID)
	if err != nil {
		http.Error(w, "failed to list workspaces", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projectsToView(projects))
}

// CreateWorkspace handles POST /api/projects — creates a new workspace.
func (a *API) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	var req CreateWorkspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		http.Error(w, "could not derive a valid slug from name; provide an explicit slug", http.StatusBadRequest)
		return
	}

	tenant := strings.TrimSpace(req.Tenant)
	if tenant == "" {
		tenant = "personal"
	}

	project, err := a.queries.CreateProject(r.Context(), db.CreateProjectParams{
		OwnerID: ownerID,
		Slug:    slug,
		Name:    name,
		Tenant:  tenant,
		Tracker: "agent_os_native",
		RepoUrl: pgtype.Text{String: strings.TrimSpace(req.RepoURL), Valid: req.RepoURL != ""},
	})
	if err != nil {
		// 23505 = unique_violation → slug already taken.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "a workspace with that slug already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create workspace: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(projectToView(project))
}

// UpdateWorkspaceRequest is the JSON body for PATCH /api/projects/{id}.
type UpdateWorkspaceRequest struct {
	Name    *string `json:"name"`
	RepoURL *string `json:"repo_url"`
}

// UpdateWorkspace handles PATCH /api/projects/{id} — updates mutable fields.
func (a *API) UpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	id, err := parseProjectIDParam(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	proj, err := a.queries.GetProject(r.Context(), db.GetProjectParams{ID: id, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	var req UpdateWorkspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Name != nil {
		proj.Name = strings.TrimSpace(*req.Name)
	}
	if req.RepoURL != nil {
		ru := strings.TrimSpace(*req.RepoURL)
		proj.RepoUrl = pgtype.Text{String: ru, Valid: ru != ""}
	}

	updated, err := a.queries.UpdateProjectTracker(r.Context(), db.UpdateProjectTrackerParams{
		ID:          proj.ID,
		Tracker:     proj.Tracker,
		ExternalRef: proj.ExternalRef,
		RepoUrl:     proj.RepoUrl,
		OwnerID:     ownerID,
	})
	if err != nil {
		http.Error(w, "failed to update workspace", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projectToView(updated))
}

// GetWorkspace handles GET /api/projects/{id} — selects a single workspace.
func (a *API) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	id, err := parseProjectIDParam(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	project, err := a.queries.GetProject(r.Context(), db.GetProjectParams{ID: id, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projectToView(project))
}

// WorkspaceSurface handles GET /api/projects/{id}/surface — the aggregate
// per-project view: scoped agents, memory, and artifacts. This is the
// "workspace concept tying together agents + memory + artifacts per project".
func (a *API) WorkspaceSurface(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	id, err := parseProjectIDParam(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	project, err := a.queries.GetProject(r.Context(), db.GetProjectParams{ID: id, OwnerID: ownerID})
	if err != nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	ctx := r.Context()
	surface := WorkspaceSurface{Project: projectToView(project)}

	// Agents scoped to this workspace.
	agents, err := a.queries.ListAgentsByProject(ctx, db.ListAgentsByProjectParams{
		OwnerID:   ownerID,
		ProjectID: id,
	})
	if err != nil {
		http.Error(w, "failed to load workspace agents", http.StatusInternalServerError)
		return
	}
	agentItems := make([]any, 0, len(agents))
	for _, ag := range agents {
		agentItems = append(agentItems, sanitizeAgent(ag))
	}
	surface.Agents = surfaceBucket[any]{Total: len(agents), Items: agentItems}

	// Memory scoped to this workspace (count only; search is a separate API).
	memTotal, err := a.queries.CountMemoryByProject(ctx, db.CountMemoryByProjectParams{
		OwnerID:   ownerID,
		ProjectID: id,
	})
	if err != nil {
		http.Error(w, "failed to load workspace memory", http.StatusInternalServerError)
		return
	}
	surface.Memory = surfaceBucket[any]{Total: int(memTotal), Items: []any{}}

	// Artifacts scoped to this workspace.
	artifacts, err := a.queries.ListArtifactsByProject(ctx, db.ListArtifactsByProjectParams{
		OwnerID:   ownerID,
		ProjectID: id,
		Column3:   "", // no type filter
		Limit:     50,
		Offset:    0,
	})
	if err != nil {
		http.Error(w, "failed to load workspace artifacts", http.StatusInternalServerError)
		return
	}
	artifactItems := make([]any, 0, len(artifacts))
	for _, ar := range artifacts {
		artifactItems = append(artifactItems, artifactToResponse(ar))
	}
	surface.Artifacts = surfaceBucket[any]{Total: len(artifacts), Items: artifactItems}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(surface)
}

// parseProjectIDParam extracts and validates the {id} URL param.
func parseProjectIDParam(r *http.Request) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(chi.URLParam(r, "id")); err != nil {
		return id, errors.New("invalid workspace ID")
	}
	return id, nil
}

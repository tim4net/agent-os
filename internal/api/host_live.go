package api

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tim4net/agent-os/internal/service"
)

// HostLiveWorktree is a single worktree in the GET /api/host/live snapshot.
// It surfaces host-side git/worktree info the containerised API otherwise
// lacks (issue #132 AC: "host can expose git/worktree info").
type HostLiveWorktree struct {
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	Dirty       bool   `json:"dirty"`
	HEAD        string `json:"head"`
	IsMain      bool   `json:"is_main"`
	ExternalRef string `json:"external_ref,omitempty"`
}

// HostLiveResponse is the body for GET /api/host/live. It is a read-only,
// on-demand snapshot of host-side capabilities computed by the API process
// itself — distinct from the DB-backed heartbeat table at /host/liveness.
type HostLiveResponse struct {
	Host        string             `json:"host"`
	PID         int                `json:"pid"`
	RepoPath    string             `json:"repo_path"`
	Worktrees   []HostLiveWorktree `json:"worktrees"`
	WorktreeErr string             `json:"worktree_error,omitempty"`
	GeneratedAt string             `json:"generated_at"`
}

// HostLiveRoutes returns a Chi router for the live host surface (issue #132).
// Mounted at /api/host/live by the integrator (router.go).
func (a *API) HostLiveRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.GetHostLive)
	return r
}

// GetHostLive handles GET /api/host/live.
//
// It serves real host data: the API host's hostname + PID and a git worktree
// scan of the configured repo path (repo_path query param, else AOS_REPO_PATH
// env var, default /repos/agent-os).
//
// Worktree scanning is best-effort. If the repo is not accessible from this
// process (e.g. the containerised API without the host repo mounted) the
// response still returns 200 with hostname/PID populated and a worktree_error
// field describing why the scan failed. This lets callers distinguish "host
// repo not mounted here" from "endpoint broken", and lets the containerised
// API surface partial host state. An actual endpoint failure (e.g. encoding)
// returns 500.
func (a *API) GetHostLive(w http.ResponseWriter, r *http.Request) {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}

	repoPath := r.URL.Query().Get("repo_path")
	if repoPath == "" {
		repoPath = os.Getenv("AOS_REPO_PATH")
		if repoPath == "" {
			repoPath = "/repos/agent-os"
		}
	}

	resp := HostLiveResponse{
		Host:        host,
		PID:         os.Getpid(),
		RepoPath:    repoPath,
		Worktrees:   []HostLiveWorktree{},
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	scanner := service.NewWorktreeScanner(repoPath)
	trees, scanErr := scanner.Scan(r.Context())
	if scanErr != nil {
		slog.Default().Warn("host/live: worktree scan failed", "error", scanErr, "repo", repoPath)
		resp.WorktreeErr = scanErr.Error()
	} else {
		for _, wt := range trees {
			resp.Worktrees = append(resp.Worktrees, HostLiveWorktree{
				Path:        wt.Path,
				Branch:      wt.Branch,
				Dirty:       wt.Dirty,
				HEAD:        wt.HEAD,
				IsMain:      wt.IsMain,
				ExternalRef: wt.ExternalRef,
			})
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

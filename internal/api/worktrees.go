package api

import (
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/tim4net/agent-os/internal/service"
)

// WorktreeResponse is a single worktree in the API response.
type WorktreeResponse struct {
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	Dirty       bool   `json:"dirty"`
	HEAD        string `json:"head"`
	IsMain      bool   `json:"is_main"`
	ExternalRef string `json:"external_ref,omitempty"`
}

// WorktreeListResponse is the response for GET /api/worktrees.
type WorktreeListResponse struct {
	Worktrees []WorktreeResponse `json:"worktrees"`
	RepoPath  string             `json:"repo_path"`
}

// WorktreeRoutes returns a Chi router for worktree endpoints (WP-N).
// Mounted at /api/worktrees by the integrator (router.go).
func (a *API) WorktreeRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.ListWorktrees)
	return r
}

// ListWorktrees handles GET /api/worktrees?repo_path=...
// Lists active git worktrees with branch + dirty flag.
// If the branch carries an SC id, the response includes the external_ref.
// The repo_path query parameter defaults to the AOS_REPO_PATH env var or /repos/agent-os.
func (a *API) ListWorktrees(w http.ResponseWriter, r *http.Request) {
	repoPath := r.URL.Query().Get("repo_path")
	if repoPath == "" {
		repoPath = os.Getenv("AOS_REPO_PATH")
		if repoPath == "" {
			repoPath = "/repos/agent-os"
		}
	}

	// AOS_GIT_BIN overrides the git executable used by the scanner (defaults
	// to "git"). Operators may point it at a non-standard git, and tests use a
	// nonexistent binary to force the git-unavailable path (issue #123).
	gitBin := os.Getenv("AOS_GIT_BIN")
	if gitBin == "" {
		gitBin = "git"
	}

	scanner := service.NewWorktreeScannerWithGit(repoPath, gitBin)
	worktrees, err := scanner.Scan(r.Context())
	if err != nil {
		// Distinguish "git not available" (503, degraded) from genuine scan
		// failures (500). Never leak raw exec internals to the client: log the
		// detail server-side and return a clean message (issue #123).
		if errors.Is(err, service.ErrGitUnavailable) {
			slog.Default().Error("worktrees: git unavailable", "error", err, "repo", repoPath)
			writeError(w, http.StatusServiceUnavailable, "git is not available in this environment; worktree listing is unavailable")
			return
		}
		slog.Default().Error("worktrees: scan failed", "error", err, "repo", repoPath)
		writeError(w, http.StatusInternalServerError, "failed to scan worktrees")
		return
	}

	resp := WorktreeListResponse{
		Worktrees: make([]WorktreeResponse, 0, len(worktrees)),
		RepoPath:  repoPath,
	}
	for _, wt := range worktrees {
		resp.Worktrees = append(resp.Worktrees, WorktreeResponse{
			Path:        wt.Path,
			Branch:      wt.Branch,
			Dirty:       wt.Dirty,
			HEAD:        wt.HEAD,
			IsMain:      wt.IsMain,
			ExternalRef: wt.ExternalRef,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

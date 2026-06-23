package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Issue #132: GET /api/host/live — live host surface tests
// ---------------------------------------------------------------------------

// TestHTTPHostLive_Returns200WithHostData asserts the happy path: a 200 with
// the documented JSON shape (hostname, pid, repo_path, worktrees, generated_at).
func TestHTTPHostLive_Returns200WithHostData(t *testing.T) {
	a := &API{}

	srv := httptest.NewServer(a.HostLiveRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/?repo_path=/nonexistent/repo")
	if err != nil {
		t.Fatalf("GET host/live: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body HostLiveResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Host == "" {
		t.Fatal("expected non-empty host")
	}
	if body.PID <= 0 {
		t.Fatalf("expected positive pid, got %d", body.PID)
	}
	if body.RepoPath != "/nonexistent/repo" {
		t.Fatalf("expected repo_path %q, got %q", "/nonexistent/repo", body.RepoPath)
	}
	if body.GeneratedAt == "" {
		t.Fatal("expected non-empty generated_at")
	}
	// worktrees must always be a non-nil slice so callers get "[]" not null.
	if body.Worktrees == nil {
		t.Fatal("expected non-nil worktrees slice")
	}
}

// TestHTTPHostLive_BadRepoPath_Returns200WithError is the negative/tolerance
// test (AC #3 no regression): an inaccessible repo_path must NOT 500. The
// endpoint returns 200 with a worktree_error field so the containerised API
// can surface partial host state and distinguish "repo not mounted" from a
// broken endpoint.
func TestHTTPHostLive_BadRepoPath_Returns200WithError(t *testing.T) {
	a := &API{}

	srv := httptest.NewServer(a.HostLiveRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/?repo_path=/definitely/does/not/exist/repo")
	if err != nil {
		t.Fatalf("GET host/live: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (tolerant of scan failure), got %d", resp.StatusCode)
	}

	var body HostLiveResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.WorktreeErr == "" {
		t.Fatal("expected non-empty worktree_error for inaccessible repo")
	}
	if len(body.Worktrees) != 0 {
		t.Fatalf("expected zero worktrees on scan failure, got %d", len(body.Worktrees))
	}
}

// TestHTTPHostLive_SurfacesRealWorktrees is the core AC #1/#2 test: with a
// real host-side git repo, the endpoint surfaces its worktrees — the
// "host can expose git/worktree info the API container lacks" capability.
func TestHTTPHostLive_SurfacesRealWorktrees(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	workDir := filepath.Join(tmpDir, "work")

	run := func(dir string, args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = cleanGitEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %s (err=%v)", dir, args, string(out), err)
		}
	}

	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repoDir, "git", "init")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "initial")

	// Add a worktree on an SC-correlated branch so external_ref surfaces too.
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repoDir, "git", "worktree", "add", filepath.Join(workDir, "wt-live"), "-b", "wp-132/SC-7-host-live")

	a := &API{}
	srv := httptest.NewServer(a.HostLiveRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/?repo_path=" + repoDir)
	if err != nil {
		t.Fatalf("GET host/live: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body HostLiveResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RepoPath != repoDir {
		t.Fatalf("expected repo_path %q, got %q", repoDir, body.RepoPath)
	}
	if body.WorktreeErr != "" {
		t.Fatalf("expected no worktree_error, got %q", body.WorktreeErr)
	}

	found := false
	for _, wt := range body.Worktrees {
		if strings.Contains(wt.Path, "wt-live") {
			found = true
			if wt.Branch != "wp-132/SC-7-host-live" {
				t.Fatalf("expected branch wp-132/SC-7-host-live, got %q", wt.Branch)
			}
			if wt.ExternalRef != "SC-7" {
				t.Fatalf("expected external_ref SC-7, got %q", wt.ExternalRef)
			}
		}
	}
	if !found {
		t.Fatalf("wt-live worktree not surfaced in host/live snapshot; got %d worktrees", len(body.Worktrees))
	}
}

// TestHTTPHostLive_RepoPathQueryOverridesEnv asserts the repo_path query param
// takes precedence over the AOS_REPO_PATH env var.
func TestHTTPHostLive_RepoPathQueryOverridesEnv(t *testing.T) {
	t.Setenv("AOS_REPO_PATH", "/env/configured/repo")

	a := &API{}
	srv := httptest.NewServer(a.HostLiveRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/?repo_path=/query/param/repo")
	if err != nil {
		t.Fatalf("GET host/live: %v", err)
	}
	defer resp.Body.Close()

	var body HostLiveResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RepoPath != "/query/param/repo" {
		t.Fatalf("expected query param to override env, got repo_path %q", body.RepoPath)
	}
}

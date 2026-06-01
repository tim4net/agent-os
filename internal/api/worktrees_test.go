package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tim4net/agent-os/internal/service"
)

// ---------------------------------------------------------------------------
// WP-N: Worktrees API route tests (httptest with real temp git repos)
// ---------------------------------------------------------------------------

// TestHTTPWorktrees_ListReturns200 tests the happy path: a temp repo with
// worktrees returns 200 and the expected JSON shape.
func TestHTTPWorktrees_ListReturns200(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	workDir := filepath.Join(tmpDir, "work")

	run := func(dir string, args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
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

	// Create a worktree on a namespaced branch carrying an SC ref
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repoDir, "git", "worktree", "add", filepath.Join(workDir, "wt-sc"), "-b", "wp-n/SC-42-fix")

	// Build an API with a nil queries (worktrees don't need DB)
	a := &API{
		queries: nil,
		bus:     service.NewEventBus(),
	}

	srv := httptest.NewServer(a.WorktreeRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/?repo_path=" + repoDir)
	if err != nil {
		t.Fatalf("GET worktrees: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body WorktreeListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RepoPath != repoDir {
		t.Fatalf("expected repo_path %q, got %q", repoDir, body.RepoPath)
	}
	if len(body.Worktrees) < 1 {
		t.Fatalf("expected at least 1 worktree, got %d", len(body.Worktrees))
	}

	// Find the SC branch worktree and assert external_ref surfaces
	found := false
	for _, wt := range body.Worktrees {
		if wt.Branch == "wp-n/SC-42-fix" {
			found = true
			if wt.ExternalRef == "" {
				t.Fatal("expected external_ref for SC branch, got empty")
			}
		}
	}
	if !found {
		t.Fatal("worktree with branch wp-n/SC-42-fix not found")
	}
}

// TestHTTPWorktrees_BadRepoPath_Returns500 tests that a nonexistent repo_path
// causes a 500 error.
func TestHTTPWorktrees_BadRepoPath_Returns500(t *testing.T) {
	a := &API{
		queries: nil,
		bus:     service.NewEventBus(),
	}

	srv := httptest.NewServer(a.WorktreeRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/?repo_path=/nonexistent/path/to/repo")
	if err != nil {
		t.Fatalf("GET worktrees: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 for bad repo_path, got %d", resp.StatusCode)
	}
}

// TestHTTPWorktrees_ExternalRefSurfacesForSCBranch tests that an SC-prefixed
// branch name surfaces as external_ref in the response.
func TestHTTPWorktrees_ExternalRefSurfacesForSCBranch(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	workDir := filepath.Join(tmpDir, "work")

	run := func(dir string, args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
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

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repoDir, "git", "worktree", "add", filepath.Join(workDir, "wt-sc2"), "-b", "wp-o1/SC-91130-pog")

	scanner := service.NewWorktreeScanner(repoDir)
	trees, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	found := false
	for _, wt := range trees {
		if wt.Branch == "wp-o1/SC-91130-pog" {
			found = true
			if wt.ExternalRef == "" {
				t.Fatalf("expected external_ref for wp-o1/SC-91130-pog, got empty")
			}
		}
	}
	if !found {
		t.Fatal("worktree with branch wp-o1/SC-91130-pog not found")
	}
}

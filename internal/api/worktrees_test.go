package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tim4net/agent-os/internal/service"
)

// ---------------------------------------------------------------------------
// WP-N: Worktrees API route tests (httptest with real temp git repos)
// ---------------------------------------------------------------------------

// cleanGitEnv returns a copy of os.Environ() with GIT_DIR, GIT_WORK_TREE, and
// GIT_INDEX_FILE removed so that tests never inherit state from a parent
// git process (e.g. a pre-push hook).  It also neutralises GIT_CONFIG_*
// to prevent picking up user-level gitconfig.
func cleanGitEnv() []string {
	drop := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
	}
	filtered := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				if drop[kv[:i]] {
					goto skip
				}
				break
			}
		}
		filtered = append(filtered, kv)
	skip:
	}
	filtered = append(filtered, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	return filtered
}

// TestHTTPWorktrees_ListReturns200 tests the happy path: a temp repo with
// worktrees returns 200 and the expected JSON shape.
func TestHTTPWorktrees_ListReturns200(t *testing.T) {
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

// TestHTTPWorktrees_GitUnavailable_Returns503 is the negative test for
// issue #123: when git is not available the endpoint must degrade to 503 with
// a clean message, never an unhandled 500 or a leaked stack trace. It forces
// the git-unavailable path via the AOS_GIT_BIN override.
func TestHTTPWorktrees_GitUnavailable_Returns503(t *testing.T) {
	t.Setenv("AOS_GIT_BIN", "/nonexistent/path/to/git")

	a := &API{
		queries: nil,
		bus:     service.NewEventBus(),
	}

	srv := httptest.NewServer(a.WorktreeRoutes())
	defer srv.Close()

	// repo_path is irrelevant: the scanner fails at LookPath before touching it.
	resp, err := http.Get(srv.URL + "/?repo_path=/anywhere")
	if err != nil {
		t.Fatalf("GET worktrees: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when git unavailable, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	msg := body["error"]
	// The message must be clean — no leaked exec internals, binary path, or stack trace.
	for _, leak := range []string{"exec", "fork", "/nonexistent", "panic", "goroutine"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("error message leaks internals (%q present): %q", leak, msg)
		}
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

// TestHTTPWorktrees_SurvivesPoisonedGitEnv is a regression guard for issue #71.
// It creates TWO repos, then poisons GIT_DIR/GIT_WORK_TREE to point at the
// SECOND repo while exercising the scanner against the FIRST.  Without the
// env-scrub fix in scrubbedGitEnv, the poisoned env causes git subprocesses
// to operate on the WRONG repository — the second repo instead of the first.
// This manifests as scanner.Scan() returning worktrees from the second repo
// (wrong count) and worktree paths outside the test tmpDir.
//
// Mutation check: reverting scrubbedGitEnv to plain append(os.Environ(), …)
// turns this test RED because the scanner would list the second (poison) repo's
// worktrees instead of the first (target) repo's.
func TestHTTPWorktrees_SurvivesPoisonedGitEnv(t *testing.T) {
	tmpDir := t.TempDir()

	// Repo 1 — the repo under test (has 1 worktree).
	repoDir1 := filepath.Join(tmpDir, "repo1")
	workDir1 := filepath.Join(tmpDir, "work1")
	if err := os.MkdirAll(repoDir1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workDir1, 0o755); err != nil {
		t.Fatal(err)
	}

	// Repo 2 — the "poison" repo (has 2 worktrees, different branch names).
	repoDir2 := filepath.Join(tmpDir, "repo2")
	workDir2 := filepath.Join(tmpDir, "work2")
	if err := os.MkdirAll(repoDir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workDir2, 0o755); err != nil {
		t.Fatal(err)
	}

	// Helper that scrubs env for safe test setup.
	run := func(dir string, args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = cleanGitEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %s (err=%v)", dir, args, string(out), err)
		}
	}

	// Set up repo 1 with 1 worktree.
	run(repoDir1, "git", "init")
	run(repoDir1, "git", "config", "user.email", "test@test.com")
	run(repoDir1, "git", "config", "user.name", "test")
	run(repoDir1, "git", "commit", "--allow-empty", "-m", "initial")
	run(repoDir1, "git", "worktree", "add", filepath.Join(workDir1, "wt1"), "-b", "wp-reg/SC-71-regression")

	// Set up repo 2 with 2 worktrees — this is the "poison" source.
	run(repoDir2, "git", "init")
	run(repoDir2, "git", "config", "user.email", "test@test.com")
	run(repoDir2, "git", "config", "user.name", "test")
	run(repoDir2, "git", "commit", "--allow-empty", "-m", "initial")
	run(repoDir2, "git", "worktree", "add", filepath.Join(workDir2, "wt2a"), "-b", "poison/branch-a")
	run(repoDir2, "git", "worktree", "add", filepath.Join(workDir2, "wt2b"), "-b", "poison/branch-b")

	// POISON the process environment to point at repo 2.
	// After this, any child process that reads os.Environ() without scrubbing
	// will think we're operating on repo2 instead of repo1.
	t.Setenv("GIT_DIR", filepath.Join(repoDir2, ".git"))
	t.Setenv("GIT_WORK_TREE", repoDir2)

	// Scan repo 1 — must return only repo 1's worktrees despite the poison.
	scanner := service.NewWorktreeScanner(repoDir1)
	trees, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan with poisoned env: %v", err)
	}

	// Exactly 2 worktrees from repo 1: the bare repo + wt1.
	if len(trees) != 2 {
		t.Fatalf("expected exactly 2 worktrees from repo1, got %d — env scrub is broken (listed repo2's worktrees)", len(trees))
	}

	found := false
	for _, wt := range trees {
		// Every worktree path must be under tmpDir/repo1 or tmpDir/work1,
		// never under repo2.
		if strings.HasPrefix(wt.Path, repoDir2) || strings.HasPrefix(wt.Path, workDir2) {
			t.Fatalf("worktree path %q is from poison repo — env scrub is broken", wt.Path)
		}
		if wt.Branch == "wp-reg/SC-71-regression" {
			found = true
			if wt.ExternalRef == "" {
				t.Fatalf("expected external_ref for %s, got empty", wt.Branch)
			}
		}
	}
	if !found {
		t.Fatal("worktree with branch wp-reg/SC-71-regression not found")
	}

	// HTTP endpoint must also return 200 with correct data.
	a := &API{
		queries: nil,
		bus:     service.NewEventBus(),
	}
	srv := httptest.NewServer(a.WorktreeRoutes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/?repo_path=" + repoDir1)
	if err != nil {
		t.Fatalf("GET worktrees with poisoned env: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with poisoned env, got %d", resp.StatusCode)
	}

	var body WorktreeListResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RepoPath != repoDir1 {
		t.Fatalf("expected repo_path %q, got %q", repoDir1, body.RepoPath)
	}
	if len(body.Worktrees) != 2 {
		t.Fatalf("expected 2 worktrees in HTTP response, got %d", len(body.Worktrees))
	}
}

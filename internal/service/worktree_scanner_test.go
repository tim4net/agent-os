package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// WP-N: WorktreeScanner unit tests
// ---------------------------------------------------------------------------

func TestParseWorktreeList_EmptyInput(t *testing.T) {
	trees, err := parseWorktreeList("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trees) != 0 {
		t.Fatalf("expected 0 worktrees, got %d", len(trees))
	}
}

func TestParseWorktreeList_SingleWorktree(t *testing.T) {
	input := `worktree /repos/agent-os
HEAD abc1234
branch refs/heads/main`

	trees, err := parseWorktreeList(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(trees))
	}
	if trees[0].Path != "/repos/agent-os" {
		t.Fatalf("expected path /repos/agent-os, got %s", trees[0].Path)
	}
	if trees[0].Branch != "main" {
		t.Fatalf("expected branch main, got %s", trees[0].Branch)
	}
	if trees[0].HEAD != "abc1234" {
		t.Fatalf("expected HEAD abc1234, got %s", trees[0].HEAD)
	}
}

func TestParseWorktreeList_MultipleWorktrees(t *testing.T) {
	input := `worktree /repos/agent-os
HEAD abc1234
branch refs/heads/main

worktree /home/tim/work/agent-os/wp-g
HEAD def5678
branch refs/heads/wp-g`

	trees, err := parseWorktreeList(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(trees))
	}
	if trees[0].Path != "/repos/agent-os" {
		t.Fatalf("expected first path /repos/agent-os, got %s", trees[0].Path)
	}
	if trees[0].Branch != "main" {
		t.Fatalf("expected first branch main, got %s", trees[0].Branch)
	}
	if trees[1].Path != "/home/tim/work/agent-os/wp-g" {
		t.Fatalf("expected second path /home/tim/work/agent-os/wp-g, got %s", trees[1].Path)
	}
	if trees[1].Branch != "wp-g" {
		t.Fatalf("expected second branch wp-g, got %s", trees[1].Branch)
	}
}

func TestParseWorktreeList_NamespacedBranch(t *testing.T) {
	input := `worktree /home/tim/work/agent-os/wp-o1
HEAD deadbeef
branch refs/heads/wp-o1/SC-91130-pog-timeline

worktree /home/tim/work/agent-os/wp-n
HEAD cafebabe
branch refs/heads/wp-n/issue-28-host-reporter`

	trees, err := parseWorktreeList(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(trees))
	}
	if trees[0].Branch != "wp-o1/SC-91130-pog-timeline" {
		t.Fatalf("expected namespaced branch wp-o1/SC-91130-pog-timeline, got %s", trees[0].Branch)
	}
	if trees[1].Branch != "wp-n/issue-28-host-reporter" {
		t.Fatalf("expected namespaced branch wp-n/issue-28-host-reporter, got %s", trees[1].Branch)
	}
}

func TestParseWorktreeList_DetachedHEAD(t *testing.T) {
	input := `worktree /tmp/repo
HEAD abc1234 detached`

	trees, err := parseWorktreeList(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(trees))
	}
	if trees[0].HEAD != "abc1234" {
		t.Fatalf("expected HEAD abc1234, got %s", trees[0].HEAD)
	}
	// Detached HEAD should have empty branch
	if trees[0].Branch != "" {
		t.Fatalf("expected empty branch for detached, got %s", trees[0].Branch)
	}
}

func TestExtractExternalRef(t *testing.T) {
	tests := []struct {
		branch string
		want   string
	}{
		{"wp-g/SC-91130-s pog-timeline", "SC-91130"},
		{"wp-g/sc-91130-s pog-timeline", "sc-91130"}, // case preserved
		{"main", ""},
		{"wp-n/issue-28-host-reporter", ""},
		{"SC-12345", "SC-12345"},
		{"feature/sc-999-thing", "sc-999"},
		{"misc-123", ""},  // N2: negative test — must not match as SC ref
		{"bugfix/misc-456-fix", ""}, // N2: negative test — misc is not SC
	}
	for _, tt := range tests {
		got := extractExternalRef(tt.branch)
		if got != tt.want {
			t.Errorf("extractExternalRef(%q) = %q, want %q", tt.branch, got, tt.want)
		}
	}
}

func TestWorktreeScanner_RealRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real git test in short mode")
	}

	// Create a temp repo with worktrees
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	workDir := filepath.Join(tmpDir, "work")

	// Init repo
	run := func(name string, args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = name
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %s (err=%v)", name, args, string(out), err)
		}
	}

	must(os.MkdirAll(repoDir, 0755))
	run(repoDir, "git", "init")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "initial")

	// Create a worktree
	must(os.MkdirAll(workDir, 0755))
	run(repoDir, "git", "worktree", "add", filepath.Join(workDir, "wt-1"), "-b", "feature/sc-123-test")

	// Scan
	scanner := NewWorktreeScanner(repoDir)
	trees, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if len(trees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(trees))
	}

	// Verify main worktree
	t.Logf("found %d worktrees", len(trees))
	for _, wt := range trees {
		t.Logf("  Path=%q Branch=%q HEAD=%q ExternalRef=%q", wt.Path, wt.Branch, wt.HEAD, wt.ExternalRef)
	}
	found := false
	for _, wt := range trees {
		if wt.Branch == "feature/sc-123-test" {
			found = true
			if wt.ExternalRef != "sc-123" {
				t.Fatalf("expected external_ref sc-123, got %q", wt.ExternalRef)
			}
		}
	}
	if !found {
		t.Fatal("worktree with feature/sc-123-test branch not found")
	}
}

func TestWorktreeScanner_DirtyWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real git test in short mode")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	workDir := filepath.Join(tmpDir, "work")

	run := func(name string, args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = name
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %s (err=%v)", name, args, string(out), err)
		}
	}

	must(os.MkdirAll(repoDir, 0o755))
	run(repoDir, "git", "init")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "initial")

	// Create a worktree
	wtPath := filepath.Join(workDir, "wt-dirty")
	must(os.MkdirAll(workDir, 0o755))
	run(repoDir, "git", "worktree", "add", wtPath, "-b", "dirty-test")

	// Create an untracked file in the worktree
	must(os.WriteFile(filepath.Join(wtPath, "untracked.txt"), []byte("dirty"), 0o644))

	scanner := NewWorktreeScanner(repoDir)
	trees, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	found := false
	for _, wt := range trees {
		if wt.Branch == "dirty-test" {
			found = true
			if !wt.Dirty {
				t.Fatal("expected Dirty==true for worktree with untracked file, got false")
			}
		}
	}
	if !found {
		t.Fatal("worktree with branch dirty-test not found")
	}
}

func TestWorktreeScanner_CleanWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real git test in short mode")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	workDir := filepath.Join(tmpDir, "work")

	run := func(name string, args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = name
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %s (err=%v)", name, args, string(out), err)
		}
	}

	must(os.MkdirAll(repoDir, 0o755))
	run(repoDir, "git", "init")
	run(repoDir, "git", "config", "user.email", "test@test.com")
	run(repoDir, "git", "config", "user.name", "test")
	run(repoDir, "git", "commit", "--allow-empty", "-m", "initial")

	// Create a worktree (clean — no modifications)
	wtPath := filepath.Join(workDir, "wt-clean")
	must(os.MkdirAll(workDir, 0o755))
	run(repoDir, "git", "worktree", "add", wtPath, "-b", "clean-test")

	scanner := NewWorktreeScanner(repoDir)
	trees, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	found := false
	for _, wt := range trees {
		if wt.Branch == "clean-test" {
			found = true
			if wt.Dirty {
				t.Fatal("expected Dirty==false for clean worktree, got true")
			}
		}
	}
	if !found {
		t.Fatal("worktree with branch clean-test not found")
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

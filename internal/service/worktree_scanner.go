package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// ErrGitUnavailable is returned by Scan when the configured git executable
// cannot be found on PATH (e.g. git is not installed in the API container).
// The HTTP handler maps it to 503 so the endpoint degrades gracefully instead
// of returning an unhandled 500 with a leaked stack trace (issue #123).
var ErrGitUnavailable = errors.New("git is not available")

// scrubbedGitEnv returns os.Environ() with GIT_DIR, GIT_WORK_TREE,
// GIT_INDEX_FILE removed (so a parent git process — e.g. a pre-push hook —
// does not poison child git commands) and GIT_CONFIG_GLOBAL / GIT_CONFIG_SYSTEM
// neutralised to /dev/null for test isolation.
func scrubbedGitEnv() []string {
	drop := map[string]bool{
		"GIT_DIR=": true, "GIT_WORK_TREE=": true, "GIT_INDEX_FILE=": true,
	}
	filtered := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				if drop[kv[:i+1]] {
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

// Worktree represents a single git worktree with metadata.
type Worktree struct {
	Path   string `json:"path"`    // absolute filesystem path
	Branch string `json:"branch"`  // checked-out branch name
	Dirty  bool   `json:"dirty"`   // true if working tree has uncommitted changes
	HEAD   string `json:"head"`    // short SHA of HEAD commit
	IsMain bool   `json:"is_main"` // true if on the default branch
}

// WorktreeInfo augments a Worktree with optional correlation metadata.
type WorktreeInfo struct {
	Worktree
	// Correlation: if the branch name carries an SC id (e.g. SC-12345),
	// this is the parsed external_ref. Empty if none.
	ExternalRef string `json:"external_ref,omitempty"`
}

// WorktreeScanner reads `git worktree list` from a local repo and enriches
// each worktree with branch + dirty status. It is safe for concurrent use.
type WorktreeScanner struct {
	gitDir  string // path to the .git directory (or repo root)
	gitBin  string // git executable path (defaults to "git")
	mu      sync.Mutex
	lastErr error
}

// NewWorktreeScanner creates a scanner for the given repo root directory that
// uses the "git" found on PATH. The directory must contain a .git subdirectory
// or be a bare repo.
func NewWorktreeScanner(repoRoot string) *WorktreeScanner {
	return NewWorktreeScannerWithGit(repoRoot, "git")
}

// NewWorktreeScannerWithGit creates a scanner that invokes the given git
// executable path instead of the default "git". Operators may point it at a
// non-standard git, and tests use a nonexistent binary to force the
// ErrGitUnavailable path (issue #123).
func NewWorktreeScannerWithGit(repoRoot, gitBin string) *WorktreeScanner {
	return &WorktreeScanner{gitDir: repoRoot, gitBin: gitBin}
}

// Scan runs `git worktree list` and `git status` for each worktree to
// determine branch, dirty state, and HEAD SHA.
// Returns enriched WorktreeInfo structs, or an error.
func (s *WorktreeScanner) Scan(ctx context.Context) ([]WorktreeInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Fail fast if the git binary is not available (issue #123). Inside the
	// API container git may be absent; we surface a distinct, checkable error
	// so the handler can degrade to 503 instead of an unhandled 500.
	if _, err := exec.LookPath(s.gitBin); err != nil {
		s.lastErr = fmt.Errorf("%w: %v", ErrGitUnavailable, err)
		return nil, s.lastErr
	}

	// Step 1: parse `git worktree list` output
	// Format: <path> <commit-sha> [<branch-or-detached>]
	listCmd := exec.CommandContext(ctx, s.gitBin, "-C", s.gitDir, "worktree", "list", "--porcelain")
	listCmd.Env = scrubbedGitEnv()
	out, err := listCmd.Output()
	if err != nil {
		s.lastErr = fmt.Errorf("git worktree list: %w", err)
		return nil, s.lastErr
	}

	worktrees, err := parseWorktreeList(string(out))
	if err != nil {
		s.lastErr = fmt.Errorf("parse worktree list: %w", err)
		return nil, s.lastErr
	}

	// Step 2: determine the default branch name so we can set IsMain.
	defaultBranch := detectDefaultBranch(ctx, s.gitDir, s.gitBin)

	// Step 3: check dirty status for each worktree
	results := make([]WorktreeInfo, 0, len(worktrees))
	for _, wt := range worktrees {
		info := WorktreeInfo{Worktree: wt}

		// Mark default-branch worktrees
		if wt.Branch != "" && wt.Branch == defaultBranch {
			info.IsMain = true
		}

		// Check if dirty
		statusCmd := exec.CommandContext(ctx, s.gitBin, "-C", wt.Path, "status", "--porcelain")
		statusCmd.Env = scrubbedGitEnv()
		statusOut, statusErr := statusCmd.Output()
		if statusErr != nil {
			return nil, fmt.Errorf("git status for %s: %w", wt.Path, statusErr)
		} else if len(strings.TrimSpace(string(statusOut))) > 0 {
			info.Dirty = true
		}

		// Parse external_ref from branch name (e.g. "wp-g/SC-91130-..." → "SC-91130")
		info.ExternalRef = extractExternalRef(wt.Branch)

		results = append(results, info)
	}

	return results, nil
}

// LastError returns the most recent scan error (for diagnostics).
func (s *WorktreeScanner) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// parseWorktreeList parses the --porcelain output of `git worktree list`.
// Each worktree block is separated by a blank line.
// Fields we care about: worktree <path>, HEAD <sha>, branch <ref> or detached.
func parseWorktreeList(output string) ([]Worktree, error) {
	var worktrees []Worktree
	blocks := strings.Split(output, "\n\n")
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		wt := Worktree{}
		lines := strings.Split(block, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "worktree ") {
				wt.Path = strings.TrimPrefix(line, "worktree ")
			} else if strings.HasPrefix(line, "HEAD ") {
				sha := strings.TrimPrefix(line, "HEAD ")
				// Strip any trailing annotation like "(detached)"
				parts := strings.Fields(sha)
				if len(parts) > 0 {
					wt.HEAD = parts[0]
				}
			} else if strings.HasPrefix(line, "branch ") {
				ref := strings.TrimPrefix(line, "branch ")
				// refs/heads/<branch> → <branch>
				wt.Branch = strings.TrimPrefix(ref, "refs/heads/")
			}
		}
		if wt.Path != "" {
			// Default HEAD if not parsed
			if wt.HEAD == "" {
				// Fallback: try to get short SHA
				wt.HEAD = "unknown"
			}
			worktrees = append(worktrees, wt)
		}
	}
	return worktrees, nil
}

// detectDefaultBranch returns the default branch name for a repository.
// It tries `git symbolic-ref refs/remotes/origin/HEAD` first (works in clones),
// then falls back to `git branch --show-current` (works in local repos).
// Returns empty string if detection fails (non-fatal).
func detectDefaultBranch(ctx context.Context, gitDir, gitBin string) string {
	// Try origin/HEAD symbolic ref (yields "origin/main" → "main")
	cmd := exec.CommandContext(ctx, gitBin, "-C", gitDir, "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Env = scrubbedGitEnv()
	out, err := cmd.Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		// refs/remotes/origin/main → main
		return strings.TrimPrefix(ref, "refs/remotes/origin/")
	}

	// Fallback: current branch of the repo
	cmd = exec.CommandContext(ctx, gitBin, "-C", gitDir, "branch", "--show-current")
	cmd.Env = scrubbedGitEnv()
	out, err = cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}

	return ""
}

// extractExternalRef attempts to extract a Shortcut-style external reference
// (SC-<n>) from a branch name. Returns empty string if none found.
// Uses regex anchored to segment boundary to avoid false-positives like
// "misc-123" matching as "SC-123".
func extractExternalRef(branch string) string {
	re := regexp.MustCompile(`(?i)(?:^|[/_-])(SC-\d+)`)
	m := re.FindStringSubmatch(branch)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

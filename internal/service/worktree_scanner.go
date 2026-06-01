package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Worktree represents a single git worktree with metadata.
type Worktree struct {
	Path      string `json:"path"`       // absolute filesystem path
	Branch    string `json:"branch"`     // checked-out branch name
	Dirty     bool   `json:"dirty"`      // true if working tree has uncommitted changes
	HEAD      string `json:"head"`       // short SHA of HEAD commit
	IsMain    bool   `json:"is_main"`    // true if on the default branch
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
	mu      sync.Mutex
	lastErr error
}

// NewWorktreeScanner creates a scanner for the given repo root directory.
// The directory must contain a .git subdirectory or be a bare repo.
func NewWorktreeScanner(repoRoot string) *WorktreeScanner {
	return &WorktreeScanner{gitDir: repoRoot}
}

// Scan runs `git worktree list` and `git status` for each worktree to
// determine branch, dirty state, and HEAD SHA.
// Returns enriched WorktreeInfo structs, or an error.
func (s *WorktreeScanner) Scan(ctx context.Context) ([]WorktreeInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Step 1: parse `git worktree list` output
	// Format: <path> <commit-sha> [<branch-or-detached>]
	listCmd := exec.CommandContext(ctx, "git", "-C", s.gitDir, "worktree", "list", "--porcelain")
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
	defaultBranch := detectDefaultBranch(ctx, s.gitDir)

	// Step 3: check dirty status for each worktree
	results := make([]WorktreeInfo, 0, len(worktrees))
	for _, wt := range worktrees {
		info := WorktreeInfo{Worktree: wt}

		// Mark default-branch worktrees
		if wt.Branch != "" && wt.Branch == defaultBranch {
			info.IsMain = true
		}

		// Check if dirty
		statusCmd := exec.CommandContext(ctx, "git", "-C", wt.Path, "status", "--porcelain")
		statusOut, statusErr := statusCmd.Output()
		if statusErr == nil && len(strings.TrimSpace(string(statusOut))) > 0 {
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
				if idx := strings.LastIndex(ref, "/"); idx >= 0 {
					wt.Branch = ref[idx+1:]
				} else {
					wt.Branch = ref
				}
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
func detectDefaultBranch(ctx context.Context, gitDir string) string {
	// Try origin/HEAD symbolic ref (yields "origin/main" → "main")
	cmd := exec.CommandContext(ctx, "git", "-C", gitDir, "symbolic-ref", "refs/remotes/origin/HEAD")
	out, err := cmd.Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		// refs/remotes/origin/main → main
		if idx := strings.LastIndex(ref, "/"); idx >= 0 {
			return ref[idx+1:]
		}
		return ref
	}

	// Fallback: current branch of the repo
	cmd = exec.CommandContext(ctx, "git", "-C", gitDir, "branch", "--show-current")
	out, err = cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}

	return ""
}

// extractExternalRef attempts to extract a Shortcut-style external reference
// (SC-<n>) from a branch name. Returns empty string if none found.
func extractExternalRef(branch string) string {
	// Match patterns like SC-12345 in branch names
	lower := strings.ToLower(branch)
	if idx := strings.Index(lower, "sc-"); idx >= 0 {
		// Extract SC-<digits> from the branch
		// Pattern: SC-<digits>, stopping at first non-digit character after SC-
		remaining := branch[idx:]
		end := 0
		// Skip past "sc-" prefix (3 chars)
		if len(remaining) >= 3 {
			end = 3 // past "sc-"
			for end < len(remaining) && remaining[end] >= '0' && remaining[end] <= '9' {
				end++
			}
		}
		if end > 3 { // Must have at least one digit after "SC-"
			return remaining[:end]
		}
	}
	return ""
}

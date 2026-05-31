package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

const githubAPIBaseURL = "https://api.github.com"

// githubUser is a GitHub API user shape (login only).
type githubUser struct {
	Login string `json:"login"`
}

// githubLabel is a GitHub API label shape (name only).
type githubLabel struct {
	Name string `json:"name"`
}

// GitHubIssue is the deserialized shape of a GitHub Issue API response.
// Only the fields we need are populated.
type GitHubIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"` // open|closed
	HTMLURL   string    `json:"html_url"`
	Body      string    `json:"body,omitempty"`
	Labels    []string  `json:"labels,omitempty"` // simplified — label names if we flatten
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone,omitempty"`
	User *githubUser `json:"user,omitempty"`
}

// githubIssueEnvelope wraps the raw GitHub API issue response (with label objects).
// GitHub's /repos/{owner}/{repo}/issues endpoint returns BOTH issues and pull
// requests. Pull requests include a non-nil "pull_request" key; issues omit it.
// We must skip envelopes where PullRequest is non-nil to avoid mirroring PRs
// as tracker_items (violates the dogfood AC — this repo's PRs must not pollute
// the issue mirror).
type githubIssueEnvelope struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	HTMLURL   string    `json:"html_url"`
	Body      string    `json:"body,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone,omitempty"`
	User        *githubUser       `json:"user,omitempty"`
	Labels      []githubLabel     `json:"labels,omitempty"`
	PullRequest *json.RawMessage  `json:"pull_request,omitempty"`
}

// toGitHubIssue flattens the raw envelope into our simplified struct, extracting label names.
func (e *githubIssueEnvelope) toGitHubIssue() GitHubIssue {
	issue := GitHubIssue{
		Number:    e.Number,
		Title:     e.Title,
		State:     e.State,
		HTMLURL:   e.HTMLURL,
		Body:      e.Body,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
		Milestone: e.Milestone,
		User:      e.User,
	}
	for _, l := range e.Labels {
		issue.Labels = append(issue.Labels, l.Name)
	}
	return issue
}

// GitHubClient is a thin read-only HTTP wrapper for the GitHub REST API.
// Only GET requests are issued — no POST/PUT/PATCH/DELETE (F5 gate).
type GitHubClient struct {
	apiToken string
	client   *http.Client
	baseURL  string // overridable for testing
	log      *slog.Logger
}

// NewGitHubClient creates a read-only GitHub API client from the GITHUB_TOKEN env var.
func NewGitHubClient(log *slog.Logger) *GitHubClient {
	token := os.Getenv("GITHUB_TOKEN")
	return &GitHubClient{
		apiToken: token,
		client:   &http.Client{Timeout: 30 * time.Second},
		baseURL:  githubAPIBaseURL,
		log:      log,
	}
}

// listIssues fetches all open and closed issues for a given repo (owner/repo).
// Paginates through the API's page-based pagination. Only uses GET (F5 gate).
func (c *GitHubClient) listIssues(ctx context.Context, owner, repo string) ([]GitHubIssue, error) {
	if c.apiToken == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN not configured")
	}

	const maxIssues = 10000
	const maxPages = 200
	var allIssues []GitHubIssue

	for page := 1; page <= maxPages; page++ {
		url := fmt.Sprintf("%s/repos/%s/%s/issues?state=all&per_page=100&page=%d",
			c.baseURL, owner, repo, page)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("github: create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github: request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("github: read body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github: API returned %d: %s", resp.StatusCode, string(body))
		}

		// GitHub returns an array directly (not wrapped in an object).
		var envelopes []githubIssueEnvelope
		if err := json.Unmarshal(body, &envelopes); err != nil {
			return nil, fmt.Errorf("github: unmarshal: %w", err)
		}

		if len(envelopes) == 0 {
			break // no more issues
		}

		for _, env := range envelopes {
			// Skip pull requests — the /issues endpoint returns both issues
			// and PRs; PRs have a non-nil pull_request key. Mirroring PRs
			// as tracker_items violates the dogfood AC.
			if env.PullRequest != nil {
				continue
			}
			allIssues = append(allIssues, env.toGitHubIssue())
		}

		// Cap total issues to prevent unbounded fetch.
		if len(allIssues) > maxIssues {
			return nil, fmt.Errorf("github: exceeded maximum issue cap of %d", maxIssues)
		}

		// If we got fewer than 100, we're on the last page.
		if len(envelopes) < 100 {
			break
		}
	}

	return allIssues, nil
}

// GitHubSource implements TrackerSource + TrackerSyncer for GitHub Issues (WP-F).
// Read-only: only GET calls to the GitHub REST API. No writes to GitHub.
type GitHubSource struct {
	client *GitHubClient
	q      TrackerQuerier
	log    *slog.Logger
}

// NewGitHubSource creates a new GitHub Issues tracker source.
func NewGitHubSource(q TrackerQuerier, log *slog.Logger) *GitHubSource {
	return &GitHubSource{
		client: NewGitHubClient(log),
		q:      q,
		log:    log,
	}
}

// NewGitHubSourceWithClient creates a GitHubSource with an injected client (for testing).
func NewGitHubSourceWithClient(q TrackerQuerier, client *GitHubClient, log *slog.Logger) *GitHubSource {
	return &GitHubSource{
		client: client,
		q:      q,
		log:    log,
	}
}

// List returns tracker items for a project from the DB (already synced), tenant-scoped.
func (s *GitHubSource) List(ctx context.Context, projectID pgtype.UUID, tenant string, limit, offset int) ([]TrackerItemEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxTrackerItemLimit {
		limit = MaxTrackerItemLimit
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.q.ListTrackerItemsByProject(ctx, db.ListTrackerItemsByProjectParams{
		ProjectID: projectID,
		Tenant:    tenant,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		return nil, fmt.Errorf("github: list items: %w", err)
	}

	items := make([]TrackerItemEntry, 0, len(rows))
	for _, r := range rows {
		items = append(items, TrackerItemFromDB(r))
	}
	return items, nil
}

// Get returns a single tracker item from the DB.
func (s *GitHubSource) Get(ctx context.Context, projectID pgtype.UUID, externalRef string) (*TrackerItemEntry, error) {
	row, err := s.q.GetTrackerItem(ctx, db.GetTrackerItemParams{
		ProjectID:   projectID,
		ExternalRef: externalRef,
	})
	if err != nil {
		return nil, fmt.Errorf("github: get item: %w", err)
	}
	entry := TrackerItemFromDB(row)
	return &entry, nil
}

// Sync fetches issues from GitHub and upserts them into tracker_items.
// Returns SyncResult with synced/failed counts.
// Returns a non-nil error if any upsert failed.
func (s *GitHubSource) Sync(ctx context.Context, projectID pgtype.UUID, tenant string) (SyncResult, error) {
	// Find the github_issues project's external_ref to know which repo to poll.
	projects, err := s.q.GetTrackerProjects(ctx, db.GetTrackerProjectsParams{
		Tracker: "github_issues",
		Tenant:  tenant,
	})
	if err != nil {
		return SyncResult{}, fmt.Errorf("github: get projects: %w", err)
	}

	// Find the matching project — the external_ref holds "owner/repo".
	var repoRef string
	for _, p := range projects {
		if pgtypeUUIDEquals(p.ID, projectID) && p.ExternalRef.Valid {
			repoRef = strings.TrimSpace(p.ExternalRef.String)
			break
		}
	}
	if repoRef == "" {
		return SyncResult{}, fmt.Errorf("github: project %s has no github_issues external_ref configured", projectID.String())
	}

	// Parse the "owner/repo" from external_ref.
	parts := strings.SplitN(repoRef, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return SyncResult{}, fmt.Errorf("github: invalid external_ref %q (expected owner/repo)", repoRef)
	}
	owner, repo := parts[0], parts[1]

	// Fetch issues from GitHub (read-only GET only).
	issues, err := s.client.listIssues(ctx, owner, repo)
	if err != nil {
		return SyncResult{}, fmt.Errorf("github: fetch issues: %w", err)
	}

	if len(issues) == 0 {
		return SyncResult{}, fmt.Errorf("github: no issues found for %s/%s — this may indicate a misconfigured external_ref or empty repo", owner, repo)
	}

	synced := 0
	failed := 0
	var firstErr error
	for _, issue := range issues {
		externalRef := "#" + strconv.Itoa(issue.Number)
		itemType := githubStateToItemType(issue.State)

		// Serialize raw issue metadata as payload.
		payload, err := json.Marshal(map[string]any{
			"github_number":    issue.Number,
			"github_state":     issue.State,
			"github_body":      issue.Body,
			"github_labels":     issue.Labels,
			"github_created_at": issue.CreatedAt.Format(time.RFC3339),
			"github_updated_at": issue.UpdatedAt.Format(time.RFC3339),
		})
		if err != nil {
			s.log.Warn("github: failed to marshal issue payload", "external_ref", externalRef, "error", err)
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("marshal payload for %s: %w", externalRef, err)
			}
			continue
		}

		canonicalURL := pgtype.Text{String: issue.HTMLURL, Valid: issue.HTMLURL != ""}

		_, err = s.q.UpsertTrackerItem(ctx, db.UpsertTrackerItemParams{
			ProjectID:    projectID,
			ExternalRef:  externalRef,
			Title:        issue.Title,
			Status:       issue.State,
			ItemType:     itemType,
			CanonicalUrl: canonicalURL,
			Payload:      payload,
			Tenant:       tenant,
		})
		if err != nil {
			s.log.Warn("github: failed to upsert item", "external_ref", externalRef, "error", err)
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("upsert %s: %w", externalRef, err)
			}
			continue
		}
		synced++
	}

	result := SyncResult{Synced: synced, Failed: failed}

	if failed > 0 {
		s.log.Warn("github: sync completed with failures",
			"project", projectID.String(),
			"synced", synced,
			"failed", failed,
		)
		return result, fmt.Errorf("github: sync had %d failure(s); first: %w", failed, firstErr)
	}

	s.log.Info("github: sync complete", "project", projectID.String(), "items", synced)
	return result, nil
}

// githubStateToItemType maps GitHub issue states to the canonical item_type vocabulary.
// GitHub issues have no entity types like Shortcut stories/bugs/chores — every
// issue is normalized to "task" regardless of state or labels.
func githubStateToItemType(state string) string {
	return "task"
}

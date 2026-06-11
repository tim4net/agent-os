package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

const shortcutBaseURL = "https://app.shortcut.com/api/v3"

// MaxTrackerItemLimit caps pagination for tracker item listings (DoS guard).
// Exported so the API layer can use the same constant.
const MaxTrackerItemLimit = 200

// ShortcutStory is the deserialized shape of a Shortcut API story response.
type ShortcutStory struct {
	ID          int64     `json:"id"`
	Num         int       `json:"num"`
	Name        string    `json:"name"`
	EntityType  string    `json:"entity_type"` // story|bug|chore|task
	State       string    `json:"state"`        // done|in progress|todo|canceled|blocked
	AppURL      string    `json:"app_url"`
	Description string    `json:"description,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ShortcutClient is a thin read-only HTTP wrapper for the Shortcut REST API.
// Only GET requests are issued — no POST/PUT/PATCH/DELETE (F5 gate).
type ShortcutClient struct {
	apiToken string
	client   *http.Client
	baseURL  string // overridable for testing
	log      *slog.Logger
}

// NewShortcutClient creates a read-only Shortcut API client from the SHORTCUT_API_TOKEN env var.
func NewShortcutClient(log *slog.Logger) *ShortcutClient {
	token := os.Getenv("SHORTCUT_API_TOKEN")
	return &ShortcutClient{
		apiToken: token,
		client:   &http.Client{Timeout: 30 * time.Second},
		baseURL:  shortcutBaseURL,
		log:      log,
	}
}

// shortcutListResponse wraps the Shortcut API list endpoint cursor response.
type shortcutListResponse struct {
	Next string          `json:"next"`
	Data []ShortcutStory `json:"data"`
}

// listStories fetches all stories for a given Shortcut project (by numeric project ID).
// Paginates through the API's cursor-based pagination. Only uses GET (F5 gate).
// Caps total stories at maxShortcutStoryCap, detects repeated-cursor loops,
// and enforces a hard page-count bound to prevent unbounded fetch against a
// misbehaving Shortcut API (cursor cycles that return empty Data pages).
func (c *ShortcutClient) listStories(ctx context.Context, shortcutProjectID int64) ([]ShortcutStory, error) {
	if c.apiToken == "" {
		return nil, fmt.Errorf("SHORTCUT_API_TOKEN not configured")
	}

	const maxShortcutStoryCap = 10000
	const maxPages = 500 // hard page-count bound — prevents infinite loops
	var allStories []ShortcutStory
	cursor := ""

	for page := 0; page < maxPages; page++ {
		url := fmt.Sprintf("%s/projects/%d/stories", c.baseURL, shortcutProjectID)
		if cursor != "" {
			url += "?next=" + cursor
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("shortcut: create request: %w", err)
		}
		req.Header.Set("Shortcut-Token", c.apiToken)

		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("shortcut: request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("shortcut: read body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("shortcut: API returned %d: %s", resp.StatusCode, string(body))
		}

		var result shortcutListResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("shortcut: unmarshal: %w", err)
		}

		allStories = append(allStories, result.Data...)

		// Cap total stories to prevent unbounded fetch (M3).
		if len(allStories) > maxShortcutStoryCap {
			return nil, fmt.Errorf("shortcut: exceeded maximum story cap of %d", maxShortcutStoryCap)
		}

		if result.Next == "" {
			break
		}

		// Guard against self-referential cursor (M3).
		if result.Next == cursor {
			return nil, fmt.Errorf("shortcut: self-referential cursor detected")
		}
		cursor = result.Next
	}

	return allStories, nil
}

// TrackerQuerier is the subset of db.Queries needed for tracker item persistence.
// DIP: keeps the Shortcut service unit-testable with a fake.
type TrackerQuerier interface {
	UpsertTrackerItem(ctx context.Context, arg db.UpsertTrackerItemParams) (db.TrackerItem, error)
	GetTrackerItem(ctx context.Context, arg db.GetTrackerItemParams) (db.TrackerItem, error)
	ListTrackerItemsByProject(ctx context.Context, arg db.ListTrackerItemsByProjectParams) ([]db.TrackerItem, error)
	ListTrackerItemsByTenant(ctx context.Context, arg db.ListTrackerItemsByTenantParams) ([]db.TrackerItem, error)
	CountTrackerItemsByProject(ctx context.Context, arg db.CountTrackerItemsByProjectParams) (int64, error)
	CountTrackerItemsByTenant(ctx context.Context, arg db.CountTrackerItemsByTenantParams) (int64, error)
	GetTrackerProjects(ctx context.Context, arg db.GetTrackerProjectsParams) ([]db.Project, error)
}

// ShortcutSource implements TrackerSource + TrackerSyncer for Shortcut (WP-E).
// Read-only: only GET calls to the Shortcut REST API. No writes.
type ShortcutSource struct {
	client *ShortcutClient
	q      TrackerQuerier
	log    *slog.Logger
}

// NewShortcutSource creates a new Shortcut tracker source.
func NewShortcutSource(q TrackerQuerier, log *slog.Logger) *ShortcutSource {
	return &ShortcutSource{
		client: NewShortcutClient(log),
		q:      q,
		log:    log,
	}
}

// NewShortcutSourceWithClient creates a ShortcutSource with an injected client (for testing).
func NewShortcutSourceWithClient(q TrackerQuerier, client *ShortcutClient, log *slog.Logger) *ShortcutSource {
	return &ShortcutSource{
		client: client,
		q:      q,
		log:    log,
	}
}

// List returns tracker items for a project from the DB (already synced), tenant-scoped.
func (s *ShortcutSource) List(ctx context.Context, projectID pgtype.UUID, tenant string, limit, offset int) ([]TrackerItemEntry, error) {
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
		return nil, fmt.Errorf("shortcut: list items: %w", err)
	}

	items := make([]TrackerItemEntry, 0, len(rows))
	for _, r := range rows {
		items = append(items, TrackerItemFromDB(r))
	}
	return items, nil
}

// Get returns a single tracker item from the DB.
func (s *ShortcutSource) Get(ctx context.Context, projectID pgtype.UUID, externalRef string) (*TrackerItemEntry, error) {
	row, err := s.q.GetTrackerItem(ctx, db.GetTrackerItemParams{
		ProjectID:   projectID,
		ExternalRef: externalRef,
	})
	if err != nil {
		return nil, fmt.Errorf("shortcut: get item: %w", err)
	}
	entry := TrackerItemFromDB(row)
	return &entry, nil
}

// Sync fetches stories from Shortcut and upserts them into tracker_items.
// Returns SyncResult with synced/failed counts.
// Returns a non-nil error if any upsert failed (Finding #4: never silently drop).
func (s *ShortcutSource) Sync(ctx context.Context, projectID pgtype.UUID, tenant string) (SyncResult, error) {
	// Find the shortcut project's external_ref to know which Shortcut project to poll.
	projects, err := s.q.GetTrackerProjects(ctx, db.GetTrackerProjectsParams{
		Tracker: "shortcut",
		Tenant:  tenant,
	})
	if err != nil {
		return SyncResult{}, fmt.Errorf("shortcut: get projects: %w", err)
	}

	// Find the matching project — the external_ref holds the Shortcut numeric project ID.
	var shortcutProjectRef string
	for _, p := range projects {
		if pgtypeUUIDEquals(p.ID, projectID) && p.ExternalRef.Valid {
			shortcutProjectRef = strings.TrimSpace(p.ExternalRef.String)
			break
		}
	}
	if shortcutProjectRef == "" {
		return SyncResult{}, fmt.Errorf("shortcut: project %s has no shortcut external_ref configured", projectID.String())
	}

	// Parse the Shortcut project numeric ID from external_ref.
	var shortcutProjectID int64
	if _, err := fmt.Sscanf(shortcutProjectRef, "%d", &shortcutProjectID); err != nil {
		return SyncResult{}, fmt.Errorf("shortcut: invalid external_ref %q (expected numeric Shortcut project ID)", shortcutProjectRef)
	}

	// Fetch stories from Shortcut (read-only GET only).
	stories, err := s.client.listStories(ctx, shortcutProjectID)
	if err != nil {
		return SyncResult{}, fmt.Errorf("shortcut: fetch stories: %w", err)
	}

	synced := 0
	failed := 0
	var firstErr error
	for _, story := range stories {
		externalRef := fmt.Sprintf("SC-%d", story.Num)
		itemType := normalizeItemType(story.EntityType)

		// Serialize raw story metadata as payload (preserves Shortcut-specific data).
		payload, err := json.Marshal(map[string]any{
			"shortcut_id":   story.ID,
			"shortcut_num":   story.Num,
			"description":    story.Description,
			"entity_type":    story.EntityType,
			"shortcut_state": story.State,
		})
		if err != nil {
			s.log.Warn("shortcut: failed to marshal story payload", "external_ref", externalRef, "error", err)
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("marshal payload for %s: %w", externalRef, err)
			}
			continue
		}

		canonicalURL := pgtype.Text{String: story.AppURL, Valid: story.AppURL != ""}

		_, err = s.q.UpsertTrackerItem(ctx, db.UpsertTrackerItemParams{
			ProjectID:    projectID,
			ExternalRef:  externalRef,
			Title:        story.Name,
			Status:       story.State,
			ItemType:     itemType,
			CanonicalUrl: canonicalURL,
			Payload:      payload,
			Tenant:       tenant,
		})
		if err != nil {
			s.log.Warn("shortcut: failed to upsert item", "external_ref", externalRef, "error", err)
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
		s.log.Warn("shortcut: sync completed with failures",
			"project", projectID.String(),
			"synced", synced,
			"failed", failed,
		)
		return result, fmt.Errorf("shortcut: sync had %d failure(s); first: %w", failed, firstErr)
	}

	s.log.Info("shortcut: sync complete", "project", projectID.String(), "items", synced)
	return result, nil
}

// normalizeItemType maps Shortcut entity types to the canonical item_type vocabulary.
func normalizeItemType(entityType string) string {
	switch strings.ToLower(entityType) {
	case "story":
		return "story"
	case "bug":
		return "bug"
	case "chore":
		return "chore"
	case "task":
		return "task"
	case "feature":
		return "feature"
	default:
		return "story"
	}
}

// pgtypeUUIDEquals compares two pgtype.UUID values for equality.
func pgtypeUUIDEquals(a, b pgtype.UUID) bool {
	if a.Valid != b.Valid {
		return false
	}
	if !a.Valid {
		return true // both NULL
	}
	return a.Bytes == b.Bytes
}

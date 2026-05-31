package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// TrackerItemEntry is the domain representation of a tracker item for API responses.
// Decoupled from db.TrackerItem (the raw DB row) so we control the JSON shape
// and normalize pgtype wrappers to plain Go types.
// Carries synced_at + canonical_url to mark items non-authoritative (ADR-001 F4).
type TrackerItemEntry struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	ExternalRef  string    `json:"external_ref"`           // SC-<n> or #<n>
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	ItemType     string    `json:"item_type"`              // story|bug|chore|task|feature
	CanonicalURL string    `json:"canonical_url"`           // link back to source (always emitted, empty string if absent)
	Tenant       string    `json:"tenant"`
	SyncedAt     time.Time `json:"synced_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TrackerItemListResponse is the paginated response for tracker item listings.
type TrackerItemListResponse struct {
	Items  []TrackerItemEntry `json:"items"`
	Total  int64              `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// TrackerSource is the read-only interface for pluggable tracker backends (contract §8).
// Structural enforcement that the dashboard never writes trackers (ADR-001 D4/D5).
// Implementations: Shortcut (WP-E), GitHub Issues (WP-F).
type TrackerSource interface {
	// List fetches tracker items for a given project, tenant-scoped.
	// limit<=0 defaults to 50, offset<0 clamps to 0.
	List(ctx context.Context, projectID pgtype.UUID, tenant string, limit, offset int) ([]TrackerItemEntry, error)

	// Get fetches a single tracker item by project + external_ref.
	Get(ctx context.Context, projectID pgtype.UUID, externalRef string) (*TrackerItemEntry, error)
}

// TrackerSyncer is the write/sync interface for pluggable tracker backends.
// Sync performs DB upserts, so it is separated from the read-only TrackerSource
// to prevent a holder of the read-only interface from triggering writes.
type TrackerSyncer interface {
	// Sync fetches latest state from the external tracker and upserts into DB.
	// Returns the number of items synced and a count of failures.
	// Returns a non-nil error if any item failed to upsert.
	Sync(ctx context.Context, projectID pgtype.UUID, tenant string) (SyncResult, error)
}

// SyncResult captures the outcome of a Sync operation.
type SyncResult struct {
	Synced int `json:"synced"`
	Failed int `json:"failed"`
}

// TrackerItemFromDB maps a generated db.TrackerItem row to the domain TrackerItemEntry.
// Exported so the API layer can use it when it queries DB directly for tenant-wide listings.
func TrackerItemFromDB(r db.TrackerItem) TrackerItemEntry {
	e := TrackerItemEntry{
		ExternalRef: r.ExternalRef,
		Title:       r.Title,
		Status:      r.Status,
		ItemType:    r.ItemType,
		Tenant:      r.Tenant,
	}
	if r.ID.Valid {
		e.ID = r.ID.String()
	}
	if r.ProjectID.Valid {
		e.ProjectID = r.ProjectID.String()
	}
	if r.CanonicalUrl.Valid {
		e.CanonicalURL = r.CanonicalUrl.String
	}
	if r.SyncedAt.Valid {
		e.SyncedAt = r.SyncedAt.Time
	}
	if r.CreatedAt.Valid {
		e.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		e.UpdatedAt = r.UpdatedAt.Time
	}
	return e
}

package service

import (
	"context"
	"time"

	"github.com/tim4net/agent-os/internal/db"
)

// WorkUnit is the correlated (or uncorrelated) grouping of work-events that share the
// correlation key (project_id, external_ref, branch, sha) — contract §7, ADR-001 D6.
// A unit with Correlated=false is the bucket of events that carry no key part; it is
// always surfaced, never dropped (ADR-001 F3).
type WorkUnit struct {
	ProjectKey   string    `json:"project_key"`
	ExternalRef  string    `json:"external_ref,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	Sha          string    `json:"sha,omitempty"`
	EventCount   int64     `json:"event_count"`
	SessionCount int64     `json:"session_count"`
	FirstEventAt time.Time `json:"first_event_at"`
	LastEventAt  time.Time `json:"last_event_at"`
	Correlated   bool      `json:"correlated"`
}

// WorkUnitLister is the subset of db.Queries the correlation engine needs. Declaring it
// here (consumer-side interface) keeps the engine testable with a fake — DIP.
type WorkUnitLister interface {
	ListWorkUnits(ctx context.Context, arg db.ListWorkUnitsParams) ([]db.ListWorkUnitsRow, error)
	CountWorkUnits(ctx context.Context) (int64, error)
}

// CorrelationEngine groups work-events into work-units.
type CorrelationEngine struct {
	q WorkUnitLister
}

// NewCorrelationEngine constructs the engine over any WorkUnitLister.
func NewCorrelationEngine(q WorkUnitLister) *CorrelationEngine {
	return &CorrelationEngine{q: q}
}

// ListWorkUnits returns one WorkUnit per correlation group, newest activity first.
// limit<=0 defaults to 50; offset<0 clamps to 0.
func (e *CorrelationEngine) ListWorkUnits(ctx context.Context, limit, offset int32) ([]WorkUnit, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := e.q.ListWorkUnits(ctx, db.ListWorkUnitsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, err
	}
	units := make([]WorkUnit, 0, len(rows))
	for _, r := range rows {
		units = append(units, rowToWorkUnit(r))
	}
	return units, nil
}

// Count returns the total number of work-unit groups (for pagination).
func (e *CorrelationEngine) Count(ctx context.Context) (int64, error) {
	return e.q.CountWorkUnits(ctx)
}

// rowToWorkUnit maps a generated row to the domain type, normalizing pgtype wrappers.
func rowToWorkUnit(r db.ListWorkUnitsRow) WorkUnit {
	u := WorkUnit{
		ProjectKey:   r.ProjectKey,
		ExternalRef:  r.ExternalRef,
		Branch:       r.Branch,
		Sha:          r.Sha,
		EventCount:   r.EventCount,
		SessionCount: r.SessionCount,
	}
	if r.Correlated.Valid {
		u.Correlated = r.Correlated.Bool
	}
	if r.FirstEventAt.Valid {
		u.FirstEventAt = r.FirstEventAt.Time
	}
	if r.LastEventAt.Valid {
		u.LastEventAt = r.LastEventAt.Time
	}
	return u
}

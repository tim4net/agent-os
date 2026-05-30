package service

import (
	"context"
	"time"

	"github.com/tim4net/agent-os/internal/db"
)

// WorkUnit is the correlated (or uncorrelated) grouping of work-events sharing the
// correlation key (tenant, project_id, external_ref, branch, sha) — contract §7 + ADR-002.
// Correlated=false means the group carries no code/tracker anchor (external_ref/branch/sha);
// such units are still surfaced (grouped by tenant+project), never dropped (ADR-001 F3).
type WorkUnit struct {
	Tenant       string    `json:"tenant,omitempty"`
	ProjectKey   string    `json:"project_key,omitempty"`
	ExternalRef  string    `json:"external_ref,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	Sha          string    `json:"sha,omitempty"`
	EventCount   int64     `json:"event_count"`
	SessionCount int64     `json:"session_count"`
	FirstEventAt time.Time `json:"first_event_at"`
	LastEventAt  time.Time `json:"last_event_at"`
	Correlated   bool      `json:"correlated"`
}

// WorkUnitLister is the consumer-side subset of db.Queries the engine needs (DIP),
// keeping the engine unit-testable with a fake.
type WorkUnitLister interface {
	ListWorkUnits(ctx context.Context, arg db.ListWorkUnitsParams) ([]db.ListWorkUnitsRow, error)
	CountWorkUnits(ctx context.Context) (int64, error)
}

// maxWorkUnitLimit caps a single page to bound query/memory cost (DoS guard).
const maxWorkUnitLimit = 200

// CorrelationEngine groups work-events into work-units.
type CorrelationEngine struct {
	q WorkUnitLister
}

// NewCorrelationEngine constructs the engine over any WorkUnitLister.
func NewCorrelationEngine(q WorkUnitLister) *CorrelationEngine {
	return &CorrelationEngine{q: q}
}

// ListWorkUnits returns one WorkUnit per correlation group, newest activity first.
// limit<=0 defaults to 50 and is hard-capped at maxWorkUnitLimit; offset<0 clamps to 0.
func (e *CorrelationEngine) ListWorkUnits(ctx context.Context, limit, offset int) ([]WorkUnit, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxWorkUnitLimit {
		limit = maxWorkUnitLimit
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := e.q.ListWorkUnits(ctx, db.ListWorkUnitsParams{Limit: int32(limit), Offset: int32(offset)})
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
		Tenant:       r.Tenant,
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

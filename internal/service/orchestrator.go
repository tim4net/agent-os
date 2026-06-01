package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ControlMode represents the orchestrator operating mode.
type ControlMode string

const (
	ModeContinuous ControlMode = "continuous"
	ModeTick       ControlMode = "tick"
	ModeStopped    ControlMode = "stopped"
)

// WorkUnitStatus represents a work unit lifecycle state.
type WorkUnitStatus string

const (
	StatusQueued   WorkUnitStatus = "queued"
	StatusInFlight WorkUnitStatus = "in_flight"
	StatusDone     WorkUnitStatus = "done"
	StatusFailed   WorkUnitStatus = "failed"
)

// Orchestrator wraps the sqlc-generated queries for the work-unit queue.
type Orchestrator struct {
	q *db.Queries
}

// NewOrchestrator creates a new Orchestrator backed by the given queries.
func NewOrchestrator(q *db.Queries) *Orchestrator {
	return &Orchestrator{q: q}
}

// Enqueue adds a new work unit to the queue.
func (o *Orchestrator) Enqueue(ctx context.Context, wpRef string, payload json.RawMessage) (*db.WorkUnit, error) {
	row, err := o.q.EnqueueWorkUnit(ctx, db.EnqueueWorkUnitParams{
		WpRef:   wpRef,
		Payload: payload,
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ClaimNext claims the next available queued work unit (FIFO).
// Returns (nil, nil) when no units are available.
func (o *Orchestrator) ClaimNext(ctx context.Context) (*db.WorkUnit, error) {
	row, err := o.q.ClaimNextWorkUnit(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// Complete marks an in-flight work unit as done.
func (o *Orchestrator) Complete(ctx context.Context, id int64) (*db.WorkUnit, error) {
	row, err := o.q.CompleteWorkUnit(ctx, id)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Fail marks an in-flight work unit as failed and records the error message.
func (o *Orchestrator) Fail(ctx context.Context, id int64, errMsg string) (*db.WorkUnit, error) {
	row, err := o.q.FailWorkUnit(ctx, db.FailWorkUnitParams{
		ID: id,
		Error: pgtype.Text{
			String: errMsg,
			Valid:  true,
		},
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// GetState returns the singleton control_state row.
func (o *Orchestrator) GetState(ctx context.Context) (*db.ControlState, error) {
	row, err := o.q.GetControlState(ctx)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// SetMode updates the orchestrator operating mode.
func (o *Orchestrator) SetMode(ctx context.Context, mode ControlMode) (*db.ControlState, error) {
	row, err := o.q.SetControlMode(ctx, db.ControlMode(mode))
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// RunLoop drives the orchestrator based on the current control_state mode.
//   - stopped: return immediately.
//   - tick: claim and dispatch exactly ONE unit, then return. The caller (an
//     external scheduler or cron) is responsible for honoring cadence_seconds
//     and re-invoking RunLoop at the desired interval.
//   - continuous: loop claiming and dispatching until context is cancelled or
//     mode changes to non-continuous. On empty queue, idle-waits cadence_seconds
//     and retries.
//
// The dispatchFn is inert — callers provide their own processing logic.
func RunLoop(ctx context.Context, queries *db.Queries, dispatchFn func(ctx context.Context, unit *db.WorkUnit) error, log *slog.Logger) error {
	state, err := queries.GetControlState(ctx)
	if err != nil {
		return err
	}

	switch db.ControlMode(state.Mode) {
	case db.ControlModeStopped:
		return nil

	case db.ControlModeTick:
		row, err := queries.ClaimNextWorkUnit(ctx)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if err := dispatchFn(ctx, &row); err != nil {
			log.Error("dispatch failed", "unit_id", row.ID, "error", err)
		}
		return nil

	case db.ControlModeContinuous:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// Re-read mode each iteration.
			state, err := queries.GetControlState(ctx)
			if err != nil {
				return err
			}
			if db.ControlMode(state.Mode) != db.ControlModeContinuous {
				return nil
			}

			row, err := queries.ClaimNextWorkUnit(ctx)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					// Queue is empty — idle-wait and retry so newly-enqueued
					// work is picked up without restarting the loop.
					time.Sleep(time.Duration(state.CadenceSeconds) * time.Second)
					continue
				}
				return err
			}
			if err := dispatchFn(ctx, &row); err != nil {
				log.Error("dispatch failed", "unit_id", row.ID, "error", err)
			}
		}

	default:
		return nil
	}
}

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionStatus represents the derived liveness status of an agent session.
// This is NEVER stored — it is always derived from events + server clock (contract §4).
type SessionStatus struct {
	Harness         string    `json:"harness"`
	SessionID       string    `json:"session_id"`
	Host            string    `json:"host"`
	PID             *int32    `json:"pid,omitempty"`
	LivenessMode    string    `json:"liveness_mode"`
	Tenant          string    `json:"tenant"`
	Status          string    `json:"status"` // running|stale|pending|done|failed|cancelled
	LastEventAt     time.Time `json:"last_event_at"`
	LastEventKind   string    `json:"last_event_kind"`
	LastEventStatus string    `json:"last_event_status,omitempty"`
}

// Liveness configuration constants (contract §4).
const (
	// SupervisedTimeout is the heartbeat window for supervised sessions.
	// If no heartbeat within this window, the session is stale.
	SupervisedTimeout = 5 * time.Minute

	// BoundedMaxAge is the backstop ceiling for bounded sessions when
	// no host reporter covers that host (contract §4).
	BoundedMaxAge = 6 * time.Hour
)

// FleetResponse is the API response for GET /api/fleet.
type FleetResponse struct {
	Sessions []SessionStatus `json:"sessions"`
	Total    int64           `json:"total"`
}

// SessionLivenessService computes session liveness from persisted events.
// It is a PURE FUNCTION of (events, server clock) — no in-memory timers,
// no materialized state. A dashboard restart never loses or fakes state.
type SessionLivenessService struct {
	pool *pgxpool.Pool
}

// NewSessionLivenessService creates a new liveness service.
func NewSessionLivenessService(pool *pgxpool.Pool) *SessionLivenessService {
	return &SessionLivenessService{pool: pool}
}

// GetFleet returns all sessions for a tenant with derived liveness status.
// Tenant is required (ADR-002 — never empty).
func (s *SessionLivenessService) GetFleet(ctx context.Context, tenant string) (*FleetResponse, error) {
	if tenant == "" {
		return nil, fmt.Errorf("tenant is required")
	}

	now := time.Now()

	// Query all sessions for this tenant using a raw SQL query.
	sessions, err := s.querySessions(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}

	// Derive liveness for each session.
	result := make([]SessionStatus, 0, len(sessions))
	for _, sess := range sessions {
		status, err := deriveSessionStatus(ctx, s.pool, sess, now)
		if err != nil {
			return nil, fmt.Errorf("derive status for session %s: %w", sess.SessionID, err)
		}
		result = append(result, status)
	}

	return &FleetResponse{
		Sessions: result,
		Total:    int64(len(result)),
	}, nil
}

// sessionRow is an internal representation of a session's latest event.
type sessionRow struct {
	Harness        string
	SessionID      string
	Host           string
	PID            *int32
	LivenessMode   string
	Tenant         string
	LastEventKind  string
	LastEventStatus string
	ReceivedAt     time.Time
}

// querySessions fetches the latest event for each distinct (harness, session_id)
// pair within a tenant.
func (s *SessionLivenessService) querySessions(ctx context.Context, tenant string) ([]sessionRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (harness, session_id)
			harness, session_id, host, pid,
			liveness_mode, tenant,
			kind, status, received_at
		FROM work_events
		WHERE tenant = $1
		  AND kind IN ('session.start', 'session.heartbeat', 'session.end')
		ORDER BY harness, session_id, received_at DESC
	`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []sessionRow
	for rows.Next() {
		var r sessionRow
		if err := rows.Scan(
			&r.Harness, &r.SessionID, &r.Host, &r.PID,
			&r.LivenessMode, &r.Tenant,
			&r.LastEventKind, &r.LastEventStatus, &r.ReceivedAt,
		); err != nil {
			return nil, err
		}
		sessions = append(sessions, r)
	}
	return sessions, rows.Err()
}

// deriveSessionStatus computes the liveness status for a single session
// based on the contract §4 liveness rules.
//
// Liveness is a PURE FUNCTION of (persisted events, server clock now):
//
//	terminal_seen := exists event kind=session.end (status terminal)
//	if terminal_seen           -> state = (that status); ABSORBING
//	elif liveness_mode=supervised:
//	    last_hb := max(received_at where kind in {session.start,session.heartbeat})
//	    state = running if (now - last_hb) < 5m else stale
//	elif liveness_mode=bounded:
//	    # degrade gracefully if WP-N (host reporter) not merged
//	    else (no reporter coverage) -> pending if age <= 6h, stale if age > 6h
//	    backstop: bounded_max_age (6h) -> stale
func deriveSessionStatus(ctx context.Context, pool *pgxpool.Pool, row sessionRow, now time.Time) (SessionStatus, error) {
	status := SessionStatus{
		Harness:         row.Harness,
		SessionID:       row.SessionID,
		Host:            row.Host,
		PID:             row.PID,
		LivenessMode:    row.LivenessMode,
		Tenant:          row.Tenant,
		LastEventAt:     row.ReceivedAt,
		LastEventKind:   row.LastEventKind,
		LastEventStatus: row.LastEventStatus,
	}

	// Step 1: Check for terminal event (session.end).
	// If terminal_seen -> state = (that status); ABSORBING (later non-terminal events inert).
	var terminalStatus string
	err := pool.QueryRow(ctx, `
		SELECT status
		FROM work_events
		WHERE harness = $1 AND session_id = $2 AND tenant = $3 AND kind = 'session.end'
		ORDER BY received_at DESC
		LIMIT 1
	`, row.Harness, row.SessionID, row.Tenant).Scan(&terminalStatus)
	if err == nil {
		// Terminal event exists — session is done.
		status.Status = terminalStatus
		return status, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		// DB error — propagate, never swallow.
		return status, fmt.Errorf("query terminal event for session %s: %w", row.SessionID, err)
	}

	// Step 2: No terminal event. Derive from liveness_mode.
	var derived string
	var deriveErr error
	switch row.LivenessMode {
	case "supervised":
		derived, deriveErr = deriveSupervisedStatus(ctx, pool, row.Harness, row.SessionID, row.Tenant, now)
	case "bounded":
		derived, deriveErr = deriveBoundedStatus(ctx, pool, row.Harness, row.SessionID, row.Tenant, now)
	default:
		// Unknown liveness_mode — assume bounded backstop.
		derived, deriveErr = deriveBoundedStatus(ctx, pool, row.Harness, row.SessionID, row.Tenant, now)
	}
	if deriveErr != nil {
		return status, deriveErr
	}
	status.Status = derived
	return status, nil
}

// deriveSupervisedStatus computes status for supervised sessions.
// running if (now - last_hb) < 5m, else stale.
// running requires positive proof — absence of proof => stale (never "online").
func deriveSupervisedStatus(ctx context.Context, pool *pgxpool.Pool, harness, sessionID, tenant string, now time.Time) (string, error) {
	var lastHB time.Time
	err := pool.QueryRow(ctx, `
		SELECT received_at
		FROM work_events
		WHERE harness = $1 AND session_id = $2 AND tenant = $3
		  AND kind IN ('session.start', 'session.heartbeat')
		ORDER BY received_at DESC
		LIMIT 1
	`, harness, sessionID, tenant).Scan(&lastHB)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No start/heartbeat at all — can't be running.
			return "stale", nil
		}
		return "", fmt.Errorf("query supervised heartbeat for session %s: %w", sessionID, err)
	}

	if now.Sub(lastHB) < SupervisedTimeout {
		return "running", nil // Positive proof: heartbeat within timeout.
	}
	return "stale", nil // No recent heartbeat — no proof => stale.
}

// deriveBoundedStatus computes status for bounded sessions.
// WP-N (host reporter) not yet merged → degrade gracefully to bounded_max_age only.
// No reporter coverage → stale once age > 6h, NEVER running without proof.
//
// Returns "pending" for sessions under BoundedMaxAge (awaiting host reporter proof),
// and "stale" for sessions exceeding BoundedMaxAge (backstop triggered).
// This makes the age threshold observable in tests — before the fix both branches
// returned "stale" making the backstop check tautological.
func deriveBoundedStatus(ctx context.Context, pool *pgxpool.Pool, harness, sessionID, tenant string, now time.Time) (string, error) {
	var startReceivedAt time.Time
	err := pool.QueryRow(ctx, `
		SELECT received_at
		FROM work_events
		WHERE harness = $1 AND session_id = $2 AND tenant = $3 AND kind = 'session.start'
		ORDER BY received_at ASC
		LIMIT 1
	`, harness, sessionID, tenant).Scan(&startReceivedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "stale", nil
		}
		return "", fmt.Errorf("query bounded start for session %s: %w", sessionID, err)
	}

	// Backstop: bounded_max_age (6h) -> stale.
	if now.Sub(startReceivedAt) > BoundedMaxAge {
		slog.Default().Debug("bounded session exceeded max age backstop",
			"session_id", sessionID, "age", now.Sub(startReceivedAt))
		return "stale", nil
	}

	// If no host reporter coverage (WP-N not merged), bounded sessions
	// without proof are "pending" — they haven't expired yet but lack
	// positive proof of liveness. They become "running" once WP-N confirms
	// the process is alive.
	return "pending", nil
}

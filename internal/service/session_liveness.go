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
	LivenessMode    string    `json:"liveness_mode"` // resolved from session.start, never NULL
	Tenant          string    `json:"tenant"`
	Status          string    `json:"status"` // running|stale|done|failed|cancelled|unknown
	LastEventAt     time.Time `json:"last_event_at"`
	LastEventKind   string    `json:"last_event_kind"`
	LastEventStatus string    `json:"last_event_status,omitempty"` // empty string if NULL
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
// LivenessMode and LastEventStatus use *string because they can be SQL NULL:
// liveness_mode is set on session.start only → NULL on session.end/heartbeat;
// status is conditional → can be NULL on heartbeat or session.end.
type sessionRow struct {
	Harness         string
	SessionID       string
	Host            string
	PID             *int32
	LivenessMode    *string
	Tenant          string
	LastEventKind   string
	LastEventStatus *string
	ReceivedAt      time.Time
}

// querySessions fetches the latest event for each distinct (harness, session_id)
// pair within a tenant. Uses COALESCE on liveness_mode and status so SQL NULL
// scans safely into *string — callers can distinguish NULL from empty string
// if needed, but the derivation logic always resolves liveness_mode from the
// session.start row (see deriveSessionStatus step 2).
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
//	    # no reporter coverage → stale (never running without proof)
//	    backstop: bounded_max_age (6h) -> stale
//
// NOTE: liveness_mode is resolved from the session.start row (tenant-scoped),
// NOT from the latest-event row. session.end/heartbeat events have NULL
// liveness_mode in real data — using the latest-event row would misclassify.
func deriveSessionStatus(ctx context.Context, pool *pgxpool.Pool, row sessionRow, now time.Time) (SessionStatus, error) {
	// Resolve liveness_mode from the session.start row (authoritative).
	// This avoids NULL liveness_mode from session.end/heartbeat events.
	var resolvedMode *string
	err := pool.QueryRow(ctx, `
		SELECT liveness_mode
		FROM work_events
		WHERE harness = $1 AND session_id = $2 AND tenant = $3 AND kind = 'session.start'
		ORDER BY received_at ASC
		LIMIT 1
	`, row.Harness, row.SessionID, row.Tenant).Scan(&resolvedMode)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return SessionStatus{}, fmt.Errorf("query session.start liveness_mode for session %s: %w", row.SessionID, err)
	}

	// Dereference *string to plain string; default to "" if truly NULL (no session.start at all).
	var mode string
	if resolvedMode != nil {
		mode = *resolvedMode
	}

	// Resolve last_event_status from *string to plain string.
	var lastStatus string
	if row.LastEventStatus != nil {
		lastStatus = *row.LastEventStatus
	}

	status := SessionStatus{
		Harness:         row.Harness,
		SessionID:       row.SessionID,
		Host:            row.Host,
		PID:             row.PID,
		LivenessMode:    mode,
		Tenant:          row.Tenant,
		LastEventAt:     row.ReceivedAt,
		LastEventKind:   row.LastEventKind,
		LastEventStatus: lastStatus,
	}

	// Step 1: Check for terminal event (session.end).
	// If terminal_seen -> state = (that status); ABSORBING (later non-terminal events inert).
	var terminalStatus string
	err = pool.QueryRow(ctx, `
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

	// Step 2: No terminal event. Derive from liveness_mode (resolved from session.start).
	var derived string
	var deriveErr error
	switch mode {
	case "supervised":
		derived, deriveErr = deriveSupervisedStatus(ctx, pool, row.Harness, row.SessionID, row.Tenant, now)
	case "bounded":
		derived, deriveErr = deriveBoundedStatus(ctx, pool, row.Harness, row.SessionID, row.Tenant, now)
	default:
		// Unknown/NULL liveness_mode — log anomaly, treat as bounded backstop.
		slog.Default().Warn("unknown liveness_mode, treating as bounded",
			"session_id", row.SessionID, "mode", mode)
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
// Consumes the host_liveness feed (WP-N) to derive bounded-session status.
// A bounded session is "running" only when the host reporter confirms
// alive=true with a recent seen_at (within BoundedMaxAge TTL).
// Without positive proof or with an expired proof → stale.
//
// Derivation rule (contract §4):
//   alive=true, seen_at within TTL  → running (positive proof)
//   alive=false                     → stale (process killed/crashed)
//   no row / ErrNoRows              → stale (no proof, never running without proof)
func deriveBoundedStatus(ctx context.Context, pool *pgxpool.Pool, harness, sessionID, tenant string, now time.Time) (string, error) {
	// Query session.start for age backstop.
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

	// Consult host_liveness feed. Equivalent to GetBoundedSessionHostLiveness
	// in host_liveness.sql, but via raw SQL to keep the (pool) signature.
	var alive bool
	var seenAt *time.Time
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(hl.alive, false)::boolean,
		       hl.seen_at
		FROM (
			SELECT we.host, we.pid
			FROM work_events we
			WHERE we.harness = $1 AND we.session_id = $2 AND we.tenant = $3
			  AND we.kind = 'session.start'
			ORDER BY we.received_at DESC LIMIT 1
		) sess
		LEFT JOIN host_liveness hl
		    ON hl.host = sess.host AND hl.pid = sess.pid AND hl.tenant = $3
	`, harness, sessionID, tenant).Scan(&alive, &seenAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "stale", nil
		}
		return "", fmt.Errorf("query host_liveness for bounded session %s: %w", sessionID, err)
	}

	// alive=false → stale (process killed/crashed).
	if !alive {
		return "stale", nil
	}

	// alive=true but seen_at expired → reporter may have died.
	// BoundedMaxAge doubles as liveness-proof freshness TTL.
	if seenAt != nil && now.Sub(*seenAt) > BoundedMaxAge {
		slog.Default().Debug("bounded session host_liveness proof expired",
			"session_id", sessionID, "seen_at", seenAt, "age", now.Sub(*seenAt))
		return "stale", nil
	}

	// Absolute session-age backstop: even with a fresh alive=true proof, a
	// bounded session older than BoundedMaxAge is suspect (contract: the 6h
	// ceiling is absolute, not just a proof-freshness TTL).
	if now.Sub(startReceivedAt) > BoundedMaxAge {
		slog.Default().Debug("bounded session exceeded absolute max-age backstop",
			"session_id", sessionID, "age", now.Sub(startReceivedAt))
		return "stale", nil
	}

	// alive=true with recent proof, within absolute ceiling → running.
	return "running", nil
}

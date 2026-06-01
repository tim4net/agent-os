package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tim4net/agent-os/internal/service"
)

// EmitterHealthRow is the raw row returned by the emitter health SQL query.
type EmitterHealthRow struct {
	Harness              string     `json:"harness"`
	SessionID            string     `json:"session_id"`
	Host                 *string    `json:"host"`
	LivenessMode         *string    `json:"liveness_mode"`
	PID                  int        `json:"pid"`
	Status               string     `json:"status"`
	LastEventReceivedAt  *time.Time `json:"last_event_received_at"`
	LastHeartbeat        *time.Time `json:"last_heartbeat"`
	FirstSeen            *time.Time `json:"first_seen"`
}

// EmitterRoutes returns a Chi router for emitter health endpoints (WP-M).
// Mounted at /api/emitters by the integrator (router.go).
func (a *API) EmitterRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.ListEmitterHealth)
	return r
}

// emitterHealthQuery is the SQL for computing emitter liveness.
// Hand-written (sqlc compile-checked in queries/emitters.sql).
// Executed via raw pgx pool.Query (no generated *.sql.go committed — Lead runs codegen).
//
// Liveness derivation uses per-session aggregation (not the latest row):
//   session_mode = MAX(liveness_mode) FILTER (WHERE kind IN ('session.start','session.heartbeat'))
// This ensures a note/artifact.created with NULL liveness_mode on the latest row
// does not cause a live supervised emitter to be reported stale.
const emitterHealthQuery = `
WITH session_agg AS (
    SELECT DISTINCT ON (harness, session_id)
        harness,
        session_id,
        MAX(CASE WHEN kind = 'session.end' AND status IN ('done','failed','cancelled')
                 THEN status END) OVER (PARTITION BY harness, session_id)
            AS terminal_status,
        MAX(received_at) FILTER (
            WHERE kind IN ('session.start', 'session.heartbeat')
        ) OVER (PARTITION BY harness, session_id)
            AS last_heartbeat,
        MAX(received_at) OVER (PARTITION BY harness, session_id)
            AS last_event_received_at,
        MAX(status) OVER (PARTITION BY harness, session_id)
            AS last_status,
        MAX(tenant) OVER (PARTITION BY harness, session_id)
            AS tenant,
        MIN(received_at) OVER (PARTITION BY harness, session_id)
            AS first_seen,
        MAX(pid) OVER (PARTITION BY harness, session_id)
            AS pid,
        -- Per-session liveness_mode: aggregate from start/heartbeat, never from
        -- arbitrary latest row (note/artifact.created legitimately have NULL mode).
        MAX(liveness_mode) FILTER (
            WHERE kind IN ('session.start', 'session.heartbeat')
        ) OVER (PARTITION BY harness, session_id)
            AS session_mode,
        -- Per-session host: derive from start/heartbeat, not from arbitrary latest row.
        MAX(host) FILTER (
            WHERE kind IN ('session.start', 'session.heartbeat')
        ) OVER (PARTITION BY harness, session_id)
            AS session_host
    FROM work_events
    WHERE tenant = $1::text
    ORDER BY harness, session_id, received_at DESC
),
deduped AS (
    SELECT DISTINCT ON (harness, session_id)
        harness,
        session_id,
        session_host AS host,
        session_mode AS liveness_mode,
        terminal_status,
        last_heartbeat,
        last_event_received_at,
        last_status,
        tenant,
        first_seen,
        pid
    FROM session_agg
    ORDER BY harness, session_id
)
SELECT
    harness,
    session_id,
    host,
    liveness_mode,
    COALESCE(pid::int, 0) AS pid,
    CASE
        WHEN terminal_status IS NOT NULL THEN
            LOWER(terminal_status)
        WHEN liveness_mode = 'supervised' THEN
            CASE
                WHEN last_heartbeat IS NOT NULL
                     AND (NOW() - last_heartbeat) < ($2::interval)
                THEN 'running'
                ELSE 'stale'
            END
        WHEN liveness_mode = 'bounded' THEN
            CASE
                WHEN last_event_received_at IS NOT NULL
                     AND (NOW() - last_event_received_at) < INTERVAL '6 hours'
                THEN 'running'
                ELSE 'stale'
            END
        ELSE 'stale'
    END AS status,
    last_event_received_at,
    last_heartbeat,
    first_seen
FROM deduped
ORDER BY last_event_received_at DESC NULLS LAST
LIMIT $3::int OFFSET $4::int
`

// countEmitterHealthQuery returns the total count of distinct sessions.
const countEmitterHealthQuery = `
SELECT COUNT(DISTINCT (harness, session_id))::bigint AS total
FROM work_events
WHERE tenant = $1::text
`

// ListEmitterHealth handles GET /api/emitters?tenant=...&stale_window=5m&limit=50&offset=0
// Returns the computed liveness state of all emitter sessions.
// Pure read — no writes. Status is a pure function of (events, server clock now).
func (a *API) ListEmitterHealth(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant query parameter is required")
		return
	}
	staleWindow := service.DefaultSupervisedStaleWindow

	if sw := r.URL.Query().Get("stale_window"); sw != "" {
		d, err := time.ParseDuration(sw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid stale_window duration")
			return
		}
		if d < 1*time.Second {
			writeError(w, http.StatusBadRequest, "stale_window must be at least 1 second")
			return
		}
		if d > 24*time.Hour {
			writeError(w, http.StatusBadRequest, "stale_window must be at most 24 hours")
			return
		}
		staleWindow = d
	}

	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 64); err == nil && n > 0 {
			if n > 10_000 {
				n = 10_000
			}
			limit = int(n)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.ParseInt(o, 10, 64); err == nil && n >= 0 {
			if n > 1_000_000_000 {
				n = 1_000_000_000
			}
			offset = int(n)
		}
	}

	ctx := r.Context()
	staleInterval := staleWindow.String()

	rows, err := a.pool.Query(ctx, emitterHealthQuery, tenant, staleInterval, limit, offset)
	if err != nil {
		slog.Default().Error("emitters: query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to query emitter health")
		return
	}
	defer rows.Close()

	var emitters []service.EmitterSession
	for rows.Next() {
		var row EmitterHealthRow
		if err := rows.Scan(
			&row.Harness, &row.SessionID, &row.Host, &row.LivenessMode,
			&row.PID, &row.Status, &row.LastEventReceivedAt,
			&row.LastHeartbeat, &row.FirstSeen,
		); err != nil {
			slog.Default().Error("emitters: scan failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to scan emitter row")
			return
		}

		lm := ""
		if row.LivenessMode != nil {
			lm = *row.LivenessMode
		}

		host := ""
		if row.Host != nil {
			host = *row.Host
		}

		emitters = append(emitters, service.EmitterSession{
			Harness:             row.Harness,
			SessionID:           row.SessionID,
			Host:                host,
			LivenessMode:        lm,
			PID:                 row.PID,
			Status:              service.EmitterStatus(row.Status),
			LastEventReceivedAt: row.LastEventReceivedAt,
			LastHeartbeat:       row.LastHeartbeat,
			FirstSeen:           row.FirstSeen,
		})
	}
	if err := rows.Err(); err != nil {
		slog.Default().Error("emitters: rows error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed reading emitter rows")
		return
	}

	// Get total count (COUNT always returns a row, never ErrNoRows)
	var total int64
	if err := a.pool.QueryRow(ctx, countEmitterHealthQuery, tenant).Scan(&total); err != nil {
		slog.Default().Error("emitters: count failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to count emitter sessions")
		return
	}

	if emitters == nil {
		emitters = []service.EmitterSession{}
	}

	writeJSON(w, http.StatusOK, service.EmitterHealthResponse{
		Emitters: emitters,
		Total:    total,
		Limit:    limit,
		Offset:   offset,
	})
}



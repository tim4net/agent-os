package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

// --- Incident types ---

// Incident represents a single failure or anomaly surfacing on the dashboard.
type Incident struct {
	Type        string    `json:"type"`         // "failed_session" | "down_instance" | "stale_session"
	Harness     string    `json:"harness"`      // agent harness (empty for down_instance)
	SessionID   string    `json:"session_id"`   // composite identity with harness (empty for down_instance)
	Host        string    `json:"host"`         // hostname
	Title       string    `json:"title"`        // session title or instance name
	Status      string    `json:"status"`       // "failed" | "stale" | "down"
	Tenant      string    `json:"tenant"`       // tenant scope
	ProjectSlug string    `json:"project_slug"` // project context (empty if uncorrelated)
	ExternalRef string    `json:"external_ref"` // tracker reference
	Branch      string    `json:"branch"`       // branch context
	ReceivedAt  time.Time `json:"received_at"`  // when the triggering event arrived (zero if not applicable)
}

// IncidentsResponse is the API response for GET /api/incidents.
type IncidentsResponse struct {
	Incidents []Incident `json:"incidents"`
	Total     int64      `json:"total"`
	Limit     int        `json:"limit"`
	Offset    int        `json:"offset"`
}

// IncidentRoutes returns a Chi router for incident-surfacing endpoints.
func (a *API) IncidentRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.ListIncidents)
	return r
}

// maxIncidentLimit is the upper bound for the incidents pagination limit.
const maxIncidentLimit = 200

// clampIncidentLimit ensures the pagination limit is within [1, maxIncidentLimit].
func clampIncidentLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > maxIncidentLimit {
		return maxIncidentLimit
	}
	return n
}

// ListIncidents handles GET /api/incidents?tenant=...&limit=50&offset=0
// Returns recent failed sessions, down instances (when WP-I merged), and stale sessions
// (when WP-J merged). Tenant-scoped. Empty state = "all green" (honest).
//
// AC: A failed work-event surfaces here within one poll/SSE cycle; no failure is buried.
// (When WP-I/J merged) a down instance + a stale session also surface.
// Empty state = "all green" (honest, not fabricated).
func (a *API) ListIncidents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := slog.Default().WithGroup("incidents")

	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 64); err == nil && n > 0 {
			if n > 1_000_000_000 {
				n = 1_000_000_000
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
	limit = clampIncidentLimit(limit)

	tenant := r.URL.Query().Get("tenant")

	// Fetch failed sessions from work_events.
	// These are always available (WP-A #2 merged, work_events table exists).
	incidents, total, err := fetchFailedSessions(ctx, a.pool, tenant, limit, offset)
	if err != nil {
		log.Error("failed to fetch incidents", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list incidents")
		return
	}

	// Graceful degradation: if WP-I (app_instances) table exists, fetch down instances.
	// If the table doesn't exist, skip silently — degrade to failed-sessions-only.
	downInstances, err := fetchDownInstances(ctx, a.pool, tenant, limit-len(incidents))
	if err != nil {
		// Table may not exist yet (WP-I not merged) — degrade gracefully.
		log.Debug("down instances not available (WP-I may not be merged)", "error", err)
	} else {
		incidents = append(incidents, downInstances...)
	}

	// Graceful degradation: if WP-J (agent_sessions) table exists, fetch stale sessions.
	// If the table doesn't exist, skip silently.
	staleSessions, err := fetchStaleSessions(ctx, a.pool, tenant, limit-len(incidents))
	if err != nil {
		log.Debug("stale sessions not available (WP-J may not be merged)", "error", err)
	} else {
		incidents = append(incidents, staleSessions...)
	}

	writeJSON(w, http.StatusOK, IncidentsResponse{
		Incidents: incidents,
		Total:     total,
		Limit:     limit,
		Offset:    offset,
	})
}

// fetchFailedSessions queries work_events for sessions that ended with status=failed.
// Uses DISTINCT ON (harness, session_id) to get one row per failed session,
// ordered by received_at DESC (most recent failure first).
func fetchFailedSessions(ctx context.Context, pool *pgxpool.Pool, tenant string, limit, offset int) ([]Incident, int64, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (we.harness, we.session_id)
			we.harness,
			we.session_id,
			we.host,
			COALESCE(we.title::text, '') AS title,
			we.tenant,
			COALESCE(p.slug::text, '') AS project_slug,
			COALESCE(we.external_ref::text, '') AS external_ref,
			COALESCE(we.branch::text, '') AS branch,
			we.received_at
		FROM work_events we
		LEFT JOIN projects p ON we.project_id = p.id
		WHERE we.kind = 'session.end'
		  AND we.status = 'failed'
		  AND ($1::text = '' OR we.tenant = $1::text)
		ORDER BY we.harness, we.session_id, we.received_at DESC
		LIMIT $2 OFFSET $3
	`, tenant, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var incidents []Incident
	for rows.Next() {
		var inc Incident
		var title, projectSlug, externalRef, branch pgtype.Text
		var receivedAt pgtype.Timestamptz
		if err := rows.Scan(
			&inc.Harness, &inc.SessionID, &inc.Host,
			&title, &inc.Tenant, &projectSlug, &externalRef, &branch,
			&receivedAt,
		); err != nil {
			return nil, 0, err
		}
		inc.Type = "failed_session"
		inc.Status = "failed"
		inc.Title = title.String
		inc.ProjectSlug = projectSlug.String
		inc.ExternalRef = externalRef.String
		inc.Branch = branch.String
		if receivedAt.Valid {
			inc.ReceivedAt = receivedAt.Time
		}
		incidents = append(incidents, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Count total distinct failed sessions for pagination.
	var total int64
	err = pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT (harness, session_id))
		FROM work_events
		WHERE kind = 'session.end'
		  AND status = 'failed'
		  AND ($1::text = '' OR tenant = $1::text)
	`, tenant).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	return incidents, total, nil
}

// fetchDownInstances fetches instances that are currently "down" (proven down by a probe).
// Gracefully degrades if the app_instances table doesn't exist yet (WP-I not merged).
func fetchDownInstances(ctx context.Context, pool *pgxpool.Pool, tenant string, remaining int) ([]Incident, error) {
	if remaining <= 0 {
		return nil, nil
	}

	rows, err := pool.Query(ctx, `
		SELECT host, COALESCE(name::text, COALESCE(cwd::text, '')),
		       tenant, COALESCE(branch::text, '')
		FROM app_instances
		WHERE status = 'down'
		  AND ($1::text = '' OR tenant = $1::text)
		ORDER BY last_probed_at DESC NULLS LAST
		LIMIT $2
	`, tenant, remaining)
	if err != nil {
		// If the table doesn't exist, this will be a catalog error — degrade gracefully.
		return nil, err
	}
	defer rows.Close()

	var incidents []Incident
	for rows.Next() {
		var inc Incident
		var name pgtype.Text
		var branch pgtype.Text
		if err := rows.Scan(&inc.Host, &name, &inc.Tenant, &branch); err != nil {
			return nil, err
		}
		inc.Type = "down_instance"
		inc.Status = "down"
		inc.Title = name.String
		inc.Branch = branch.String
		inc.ReceivedAt = time.Time{} // down instances don't have a single event timestamp
		incidents = append(incidents, inc)
	}
	return incidents, rows.Err()
}

// fetchStaleSessions fetches sessions whose liveness has gone stale (no heartbeat within
// the liveness window). Gracefully degrades if the agent_sessions table doesn't exist yet.
func fetchStaleSessions(ctx context.Context, pool *pgxpool.Pool, tenant string, remaining int) ([]Incident, error) {
	if remaining <= 0 {
		return nil, nil
	}

	rows, err := pool.Query(ctx, `
		SELECT harness, session_id, host, tenant
		FROM agent_sessions
		WHERE liveness = 'stale'
		  AND ($1::text = '' OR tenant = $1::text)
		ORDER BY last_heartbeat_at DESC NULLS LAST
		LIMIT $2
	`, tenant, remaining)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var incidents []Incident
	for rows.Next() {
		var inc Incident
		if err := rows.Scan(&inc.Harness, &inc.SessionID, &inc.Host, &inc.Tenant); err != nil {
			return nil, err
		}
		inc.Type = "stale_session"
		inc.Status = "stale"
		inc.ReceivedAt = time.Time{}
		incidents = append(incidents, inc)
	}
	return incidents, rows.Err()
}

// ensure imports are satisfied
var _ = (*db.Queries)(nil)
var _ = pgtype.Text{}

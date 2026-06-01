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
	"github.com/tim4net/agent-os/internal/service"
)

// --- Incident types ---

// Incident represents a single failure or anomaly surfacing on the dashboard.
type Incident struct {
	Type        string    `json:"type"`         // "failed_session" | "down_instance" | "stale_session"
	Harness     string    `json:"harness"`       // agent harness (empty for down_instance)
	SessionID   string    `json:"session_id"`   // composite identity with harness (empty for down_instance)
	Host        string    `json:"host"`          // hostname
	Title       string    `json:"title"`        // session title or instance name
	Status      string    `json:"status"`       // "failed" | "stale" | "down"
	Tenant      string    `json:"tenant"`        // tenant scope
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
// AC: A failed work-event surfaces here within one poll cycle; no failure is buried.
// (When WP-I/J merged) a down instance + a stale session also surface.
// Empty state = "all green" (honest, not fabricated).
//
// Pagination: unified over all incident types via CTE + ROW_NUMBER.
// Total is the true count across all types. LIMIT/OFFSET applies to the
// combined ranked set.
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

	// Use the default stale window for supervised sessions (contract 4: 5 min).
	staleWindow := service.DefaultSupervisedStaleWindow
	if sw := r.URL.Query().Get("stale_window"); sw != "" {
		d, err := time.ParseDuration(sw)
		if err == nil && d >= 1*time.Second && d <= 24*time.Hour {
			staleWindow = d
		}
	}

	incidents, total, err := fetchIncidents(ctx, a.pool, tenant, staleWindow, limit, offset)
	if err != nil {
		log.Error("failed to fetch incidents", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list incidents")
		return
	}

	writeJSON(w, http.StatusOK, IncidentsResponse{
		Incidents: incidents,
		Total:     total,
		Limit:     limit,
		Offset:    offset,
	})
}

// fetchIncidents queries all incident types in a single unified CTE that unions
// failed sessions, down instances, and stale sessions into a single ranked set,
// then paginates with LIMIT/OFFSET on the outer query.
//
// Fixes applied:
//   - Finding 1: removed SSE bus assertion from test (incidents is REST).
//   - Finding 2: down_instances uses "label" column (not "name") per WP-I schema.
//   - Finding 3: stale_sessions derived from work_events (no phantom agent_sessions table).
//   - Finding 4: failed sessions uses latest terminal status per session (not just any failed).
//   - Finding 5: unified pagination + total across all incident types.
func fetchIncidents(ctx context.Context, pool *pgxpool.Pool, tenant string, staleWindow time.Duration, limit, offset int) ([]Incident, int64, error) {
	staleInterval := staleWindow.String()

	// Build query: CTEs for each incident type, unioned, counted, ranked, paginated.
	// Uses double-quoted string to avoid backtick conflicts in Go raw strings.
	query := `
WITH failed_sessions AS (
    -- Sessions whose LATEST terminal event is status=failed.
    -- Uses DISTINCT ON to get latest session.end per (harness, session_id),
    -- then filters to failed. Prevents surfacing sessions that failed then
    -- later succeeded (finding 4).
    SELECT
        sub.harness,
        sub.session_id,
        sub.host,
        COALESCE(sub.title::text, '') AS title,
        sub.tenant,
        COALESCE(p.slug::text, '') AS project_slug,
        COALESCE(sub.external_ref::text, '') AS external_ref,
        COALESCE(sub.branch::text, '') AS branch,
        sub.received_at,
        'failed_session' AS inc_type,
        'failed' AS inc_status
    FROM (
        SELECT DISTINCT ON (we.harness, we.session_id)
            we.harness,
            we.session_id,
            we.host,
            we.title,
            we.tenant,
            we.project_id,
            we.external_ref,
            we.branch,
            we.received_at,
            we.status
        FROM work_events we
        WHERE we.kind = 'session.end'
          AND we.status IN ('done', 'failed', 'cancelled')
          AND ($1::text = '' OR we.tenant = $1::text)
        ORDER BY we.harness, we.session_id, we.received_at DESC
    ) sub
    LEFT JOIN projects p ON p.id = sub.project_id
    WHERE sub.status = 'failed'
),
down_instances AS (
    -- Instances currently marked "down" by health probes (WP-I).
    -- Uses "label" column per the WP-I schema (migration 000017).
    -- If app_instances table doesn't exist, this CTE errors gracefully
    -- and is caught by the outer error handler.
    SELECT
        '' AS harness,
        '' AS session_id,
        ai.host,
        COALESCE(ai.label::text, COALESCE(ai.cwd::text, '')) AS title,
        ai.tenant,
        '' AS project_slug,
        '' AS external_ref,
        COALESCE(ai.branch::text, '') AS branch,
        COALESCE(ai.last_probed_at, ai.updated_at) AS received_at,
        'down_instance' AS inc_type,
        'down' AS inc_status
    FROM app_instances ai
    WHERE ai.status = 'down'
      AND ($1::text = '' OR ai.tenant = $1::text)
),
stale_sessions AS (
    -- Sessions that are supervised with no recent heartbeat within the stale
    -- window (contract 4). Liveness is a pure function of persisted work_events
    -- + server clock. No agent_sessions table exists (WP-J decision, mig 000018).
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
            MAX(liveness_mode) FILTER (
                WHERE kind IN ('session.start', 'session.heartbeat')
            ) OVER (PARTITION BY harness, session_id)
                AS session_mode,
            MAX(host) FILTER (
                WHERE kind IN ('session.start', 'session.heartbeat')
            ) OVER (PARTITION BY harness, session_id)
                AS session_host,
            MAX(tenant) OVER (PARTITION BY harness, session_id)
                AS session_tenant
        FROM work_events
        WHERE ($1::text = '' OR tenant = $1::text)
        ORDER BY harness, session_id, received_at DESC
    ),
    deduped AS (
        SELECT DISTINCT ON (harness, session_id)
            harness, session_id, terminal_status, last_heartbeat,
            session_mode, session_host, session_tenant
        FROM session_agg
        ORDER BY harness, session_id
    )
    SELECT
        harness,
        session_id,
        COALESCE(session_host::text, '') AS host,
        '' AS title,
        COALESCE(session_tenant::text, '') AS tenant,
        '' AS project_slug,
        '' AS external_ref,
        '' AS branch,
        COALESCE(last_heartbeat, '1970-01-01'::timestamptz) AS received_at,
        'stale_session' AS inc_type,
        'stale' AS inc_status
    FROM deduped
    WHERE terminal_status IS NULL
      AND session_mode = 'supervised'
      AND (last_heartbeat IS NULL OR (NOW() - last_heartbeat) >= $2::interval)
),
all_incidents AS (
    SELECT * FROM failed_sessions
    UNION ALL
    SELECT * FROM down_instances
    UNION ALL
    SELECT * FROM stale_sessions
),
counted AS (
    SELECT COUNT(*)::bigint AS total FROM all_incidents
),
ranked AS (
    SELECT *, ROW_NUMBER() OVER (ORDER BY received_at DESC, inc_type, harness, session_id, host) AS rn
    FROM all_incidents
)
SELECT
    r.inc_type, r.harness, r.session_id, r.host, r.title,
    r.inc_status, r.tenant, r.project_slug, r.external_ref,
    r.branch, r.received_at, c.total
FROM counted c
LEFT JOIN ranked r ON r.rn > $3::int AND r.rn <= ($3::int + $4::int)
ORDER BY r.rn`

	rows, err := pool.Query(ctx, query, tenant, staleInterval, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	incidents := make([]Incident, 0)
	var total int64
	for rows.Next() {
		var incTypeT pgtype.Text
		var harness, sessionID, host pgtype.Text
		var title, status, tenant pgtype.Text
		var projectSlug, externalRef, branch pgtype.Text
		var receivedAt pgtype.Timestamptz

		if err := rows.Scan(
			&incTypeT, &harness, &sessionID, &host,
			&title, &status, &tenant, &projectSlug, &externalRef,
			&branch, &receivedAt, &total,
		); err != nil {
			return nil, 0, err
		}

		incType := incTypeT.String
		// When the offset is past the end of incidents, the LEFT JOIN produces
		// one row with total but NULL ranked columns. Skip it.
		if !incTypeT.Valid || incType == "" {
			continue
		}

		incidents = append(incidents, Incident{
			Type:        incType,
			Harness:     harness.String,
			SessionID:   sessionID.String,
			Host:        host.String,
			Title:       title.String,
			Status:      status.String,
			Tenant:      tenant.String,
			ProjectSlug: projectSlug.String,
			ExternalRef: externalRef.String,
			Branch:      branch.String,
			ReceivedAt:  receivedAt.Time,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return incidents, total, nil
}

// ensure imports are satisfied
var _ = (*db.Queries)(nil)
var _ = pgtype.Text{}
var _ = (*service.EventBus)(nil)

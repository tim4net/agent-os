package api

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/tim4net/agent-os/internal/db"
)

// epsilon for float64 cost comparisons.
const spendEpsilon = 1e-6

// feq returns true if a and b are within spendEpsilon of each other.
func feq(a, b float64) bool { return math.Abs(a-b) < spendEpsilon }

// SpendRow is a single row in the spend aggregation response.
type SpendRow struct {
	DimensionKey string  `json:"dimension_key"`
	TotalCostUsd float64 `json:"total_cost_usd"`
	EventCount   int64   `json:"event_count"`
	TotalTurns   int64   `json:"total_turns"`
}

// SpendResponse is the API response for GET /api/spend.
type SpendResponse struct {
	Rows   []SpendRow `json:"rows"`
	Total  int64      `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}

// SpendRoutes returns a Chi router for spend endpoints.
func (a *API) SpendRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.GetSpend)
	return r
}

// Valid group_by values for the spend endpoint.
var validGroupBy = map[string]bool{
	"agent":   true,
	"project": true,
	"tenant":  true,
	"day":     true,
}

// GetSpend handles GET /api/spend?group_by=agent|project|tenant|day&tenant=...&external_ref=...&limit=50&offset=0
// Aggregates cost_usd + num_turns from work-events per the requested dimension.
// Pure read over existing work_events — no migration needed (WP-K).
// Per the work-event contract, cost_usd is cumulative per session; the query
// dedupes to the latest event per (harness, session_id) before rolling up.
func (a *API) GetSpend(w http.ResponseWriter, r *http.Request) {
	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "agent" // default
	}
	if !validGroupBy[groupBy] {
		writeError(w, http.StatusBadRequest, "invalid group_by: must be agent|project|tenant|day")
		return
	}

	tenant := r.URL.Query().Get("tenant")
	externalRef := r.URL.Query().Get("external_ref")

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

	rows, err := a.queries.AggregateSpend(r.Context(), db.AggregateSpendParams{
		GroupBy:     groupBy,
		Off:         int32(offset),
		Lim:         int32(limit),
		Tenant:      tenant,
		ExternalRef: externalRef,
	})
	if err != nil {
		slog.Default().Error("spend: aggregate failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to aggregate spend")
		return
	}

	// Convert pgtype.Numeric to float64 for JSON.
	result := make([]SpendRow, 0, len(rows))
	var totalGroups int64
	for i, row := range rows {
		cost, _ := row.TotalCostUsd.Float64Value()
		result = append(result, SpendRow{
			DimensionKey: row.DimensionKey,
			TotalCostUsd: cost.Float64,
			EventCount:   row.EventCount,
			TotalTurns:   row.TotalTurns,
		})
		// Every row carries the same total_groups (window function); take from first.
		if i == 0 {
			totalGroups = row.TotalGroups
		}
	}

	writeJSON(w, http.StatusOK, SpendResponse{
		Rows:   result,
		Total:  totalGroups,
		Limit:  limit,
		Offset: offset,
	})
}

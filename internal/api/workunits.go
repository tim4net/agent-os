package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/tim4net/agent-os/internal/service"
)

// WorkUnitsResponse is the API response for the correlation endpoint.
type WorkUnitsResponse struct {
	WorkUnits []service.WorkUnit `json:"work_units"`
	Total     int64              `json:"total"`
	Limit     int                `json:"limit"`
	Offset    int                `json:"offset"`
}

// WorkUnitRoutes returns a Chi router for work-unit (correlation) endpoints.
func (a *API) WorkUnitRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.ListWorkUnits)
	return r
}

// ListWorkUnits handles GET /api/work-units?limit=50&offset=0 — correlated + uncorrelated
// groupings of work-events (ADR-001 D6/F3). Uncorrelated events are surfaced, never dropped.
func (a *API) ListWorkUnits(w http.ResponseWriter, r *http.Request) {
	// Parse as int64 then bound BEFORE narrowing, so a huge value can't wrap (DoS guard).
	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 64); err == nil && n > 0 {
			if n > 1_000_000 { // engine hard-caps to 200; this just prevents absurd inputs
				n = 1_000_000
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

	// Tenant scope (ADR-002): empty = all tenants; otherwise filter server-side.
	// The UI defaults to a single tenant so dayjob/personal never co-mingle in one glance.
	tenant := r.URL.Query().Get("tenant")

	engine := service.NewCorrelationEngine(a.queries)
	units, err := engine.ListWorkUnits(r.Context(), tenant, limit, offset)
	if err != nil {
		log.Printf("work-units: list failed: %v", err)
		http.Error(w, "failed to list work units", http.StatusInternalServerError)
		return
	}
	total, err := engine.Count(r.Context(), tenant)
	if err != nil {
		log.Printf("work-units: count failed: %v", err)
		http.Error(w, "failed to count work units", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	// reflect the actual cap the engine applied
	if limit > 200 {
		limit = 200
	}
	json.NewEncoder(w).Encode(WorkUnitsResponse{
		WorkUnits: units,
		Total:     total,
		Limit:     limit,
		Offset:    offset,
	})
}

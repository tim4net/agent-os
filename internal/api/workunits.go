package api

import (
	"encoding/json"
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
	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	engine := service.NewCorrelationEngine(a.queries)
	units, err := engine.ListWorkUnits(r.Context(), int32(limit), int32(offset))
	if err != nil {
		http.Error(w, "failed to list work units: "+err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := engine.Count(r.Context())
	if err != nil {
		http.Error(w, "failed to count work units: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(WorkUnitsResponse{
		WorkUnits: units,
		Total:     total,
		Limit:     limit,
		Offset:    offset,
	})
}

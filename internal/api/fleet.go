package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/tim4net/agent-os/internal/service"
)

// FleetRoutes returns a Chi router for fleet/session liveness endpoints.
func (a *API) FleetRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.GetFleet)
	return r
}

// GetFleet handles GET /api/fleet?tenant=<tenant>
// Returns all sessions for a tenant with derived liveness status.
// Tenant is REQUIRED (ADR-002) — empty tenant returns 400.
// Liveness is a PURE FUNCTION of persisted events + server clock (contract §4).
func (a *API) GetFleet(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant query parameter is required")
		return
	}

	svc := service.NewSessionLivenessService(a.pool)
	fleet, err := svc.GetFleet(r.Context(), tenant)
	if err != nil {
		slog.Default().Error("fleet: failed to get fleet", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get fleet")
		return
	}

	writeJSON(w, http.StatusOK, fleet)
}

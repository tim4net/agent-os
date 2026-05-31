package api

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// TrackerRoutes returns a Chi router for tracker item endpoints.
// All list endpoints are read-only (contract §8, ADR-001 D4/D5).
// The sync endpoint triggers a write (upsert) through the TrackerSyncer interface.
func (a *API) TrackerRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.ListTrackerItems)
	r.Get("/sync/{projectID}", a.SyncTrackerItems)
	return r
}

// clampLimit ensures the pagination limit is within [1, MaxTrackerItemLimit].
func clampLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > service.MaxTrackerItemLimit {
		return service.MaxTrackerItemLimit
	}
	return n
}

// ListTrackerItems handles GET /api/trackers/items?project_id=...&tenant=...&limit=50&offset=0
// Returns paginated tracker items, tenant-scoped (ADR-002).
func (a *API) ListTrackerItems(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 64); err == nil && n > 0 {
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

	// Clamp limit BEFORE any query (DoS guard — Finding #5).
	limit = clampLimit(limit)

	projectIDStr := r.URL.Query().Get("project_id")
	tenant := r.URL.Query().Get("tenant")

	if projectIDStr != "" {
		// List by project.
		var projectID pgtype.UUID
		if err := projectID.Scan(projectIDStr); err != nil {
			http.Error(w, "invalid project_id", http.StatusBadRequest)
			return
		}
		if tenant == "" {
			http.Error(w, "tenant query parameter is required", http.StatusBadRequest)
			return
		}

		// Get true total count for pagination (Finding: project-path Total was wrong).
		total, err := a.queries.CountTrackerItemsByProject(r.Context(), db.CountTrackerItemsByProjectParams{
			ProjectID: projectID,
			Tenant:    tenant,
		})
		if err != nil {
			log.Printf("trackers: count by project failed: %v", err)
			http.Error(w, "failed to count tracker items", http.StatusInternalServerError)
			return
		}

		src := service.NewShortcutSource(a.queries, slog.Default().WithGroup("shortcut"))
		items, err := src.List(r.Context(), projectID, tenant, limit, offset)
		if err != nil {
			log.Printf("trackers: list by project failed: %v", err)
			http.Error(w, "failed to list tracker items", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, service.TrackerItemListResponse{
			Items:  items,
			Total:  total,
			Limit:  limit,
			Offset: offset,
		})
		return
	}

	// List by tenant — query DB directly for tenant-wide view.
	if tenant == "" {
		http.Error(w, "tenant query parameter is required when project_id is not specified", http.StatusBadRequest)
		return
	}

	total, err := a.queries.CountTrackerItemsByTenant(r.Context(), tenant)
	if err != nil {
		log.Printf("trackers: count failed: %v", err)
		http.Error(w, "failed to count tracker items", http.StatusInternalServerError)
		return
	}

	rows, err := a.queries.ListTrackerItemsByTenant(r.Context(), db.ListTrackerItemsByTenantParams{
		Tenant: tenant,
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		log.Printf("trackers: list by tenant failed: %v", err)
		http.Error(w, "failed to list tracker items", http.StatusInternalServerError)
		return
	}

	items := make([]service.TrackerItemEntry, 0, len(rows))
	for _, row := range rows {
		items = append(items, service.TrackerItemFromDB(row))
	}

	writeJSON(w, http.StatusOK, service.TrackerItemListResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// SyncTrackerItems handles GET /api/trackers/sync/{projectID}?tenant=...
// Dispatches to the appropriate tracker source based on the project's
// tracker column (shortcut, github_issues). Returns SyncResult with
// synced/failed counts.
func (a *API) SyncTrackerItems(w http.ResponseWriter, r *http.Request) {
	projectIDStr := chi.URLParam(r, "projectID")
	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		http.Error(w, "tenant query parameter is required", http.StatusBadRequest)
		return
	}

	var projectID pgtype.UUID
	if err := projectID.Scan(projectIDStr); err != nil {
		http.Error(w, "invalid project_id", http.StatusBadRequest)
		return
	}

	// Look up the project to determine which tracker source to dispatch to.
	proj, err := a.queries.GetProject(r.Context(), projectID)
	if err != nil {
		log.Printf("trackers: get project failed: %v", err)
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}

	var src service.TrackerSyncer
	switch proj.Tracker {
	case "shortcut":
		src = service.NewShortcutSource(a.queries, slog.Default().WithGroup("shortcut"))
	case "github_issues":
		src = service.NewGitHubSource(a.queries, slog.Default().WithGroup("github"))
	default:
		http.Error(w, fmt.Sprintf("unsupported tracker: %s", proj.Tracker), http.StatusBadRequest)
		return
	}

	result, err := src.Sync(r.Context(), projectID, tenant)
	if err != nil {
		log.Printf("trackers: sync failed: %v", err)
		http.Error(w, fmt.Sprintf("failed to sync tracker items: %s", err.Error()), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// writeJSON encodes v as JSON and writes to w. Logs on encode error (Finding #8).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("trackers: json encode error: %v", err)
	}
}

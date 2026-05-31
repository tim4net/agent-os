package api

import (
	"encoding/json"
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
// All endpoints are read-only (contract §8, ADR-001 D4/D5).
func (a *API) TrackerRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.ListTrackerItems)
	r.Get("/sync/{projectID}", a.SyncTrackerItems)
	return r
}

// ListTrackerItems handles GET /api/trackers/items?project_id=...&tenant=...&limit=50&offset=0
// Returns paginated tracker items, tenant-scoped (ADR-002).
func (a *API) ListTrackerItems(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 64); err == nil && n > 0 {
			if n > 1_000_000 {
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

	projectIDStr := r.URL.Query().Get("project_id")
	tenant := r.URL.Query().Get("tenant")

	if projectIDStr != "" {
		// List by project.
		var projectID pgtype.UUID
		if err := projectID.Scan(projectIDStr); err != nil {
			http.Error(w, "invalid project_id", http.StatusBadRequest)
			return
		}
		src := service.NewShortcutSource(a.queries, slog.Default().WithGroup("shortcut"))
		items, err := src.List(r.Context(), projectID, tenant, limit, offset)
		if err != nil {
			log.Printf("trackers: list by project failed: %v", err)
			http.Error(w, "failed to list tracker items", http.StatusInternalServerError)
			return
		}
		if limit > 200 {
			limit = 200
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(service.TrackerItemListResponse{
			Items:  items,
			Total:  int64(len(items)),
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
	if limit > 200 {
		limit = 200
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(service.TrackerItemListResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// SyncTrackerItems handles GET /api/trackers/items/sync/{projectID}?tenant=...
// Triggers a one-shot sync of Shortcut stories for the given project.
// Returns the count of items synced.
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

	src := service.NewShortcutSource(a.queries, slog.Default().WithGroup("shortcut"))
	count, err := src.Sync(r.Context(), projectID, tenant)
	if err != nil {
		log.Printf("trackers: sync failed: %v", err)
		http.Error(w, "failed to sync tracker items", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"synced": count,
	})
}

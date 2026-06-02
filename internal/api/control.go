package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/tim4net/agent-os/internal/db"
)

// ---------------------------------------------------------------------------
// WP-O2: Control-plane HTTP API — /api/control
// ---------------------------------------------------------------------------

// ControlStateResponse is the response body for GET /api/control/state.
type ControlStateResponse struct {
	Mode           string                    `json:"mode"`
	CadenceSeconds int32                     `json:"cadence_seconds"`
	QueueCounts    map[string]int64          `json:"queue_counts"`
	UpdatedAt      string                    `json:"updated_at"`
}

// SetModeRequest is the POST body for POST /api/control/mode.
type SetModeRequest struct {
	Mode           string `json:"mode"`
	CadenceSeconds *int32 `json:"cadence_seconds,omitempty"`
}

// EnqueueUnitRequest is the POST body for POST /api/control/units.
type EnqueueUnitRequest struct {
	WpRef   string          `json:"wp_ref"`
	Payload json.RawMessage `json:"payload"`
}

// WorkUnitResponse is a single work-unit in an API response.
type WorkUnitResponse struct {
	ID          int64  `json:"id"`
	WpRef       string `json:"wp_ref"`
	Status      string `json:"status"`
	Payload     any    `json:"payload"`
	ClaimedAt   *string `json:"claimed_at,omitempty"`
	CompletedAt *string `json:"completed_at,omitempty"`
	Error       string  `json:"error,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// UnitListResponse is the paginated response for GET /api/control/units.
type UnitListResponse struct {
	Units  []WorkUnitResponse `json:"units"`
	Count  int                `json:"count"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// validModes is the set of accepted control_mode values.
var validModes = map[string]bool{
	"continuous": true,
	"tick":       true,
	"stopped":    true,
}

// ControlRoutes returns a Chi router for the control-plane endpoints.
// Integrator: add to Router() in router.go — r.Mount("/control", a.ControlRoutes())
func (a *API) ControlRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/state", a.GetControlState)
	r.Post("/mode", a.SetControlMode)
	r.Route("/units", func(r chi.Router) {
		r.Get("/", a.ListUnits)
		r.Post("/", a.EnqueueUnit)
		r.Post("/{id}/requeue", a.RequeueUnit)
	})
	return r
}

// GetControlState handles GET /api/control/state.
// Returns current mode, cadence_seconds, and queue counts by status.
func (a *API) GetControlState(w http.ResponseWriter, r *http.Request) {
	state, err := a.queries.GetControlState(r.Context())
	if err != nil {
		slog.Default().Error("control: get state failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get control state")
		return
	}

	counts, err := a.queries.CountWorkUnitsByStatus(r.Context())
	if err != nil {
		slog.Default().Error("control: count work units failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to count work units")
		return
	}

	queueCounts := make(map[string]int64)
	for _, c := range counts {
		queueCounts[string(c.Status)] = c.Count
	}

	writeJSON(w, http.StatusOK, ControlStateResponse{
		Mode:           string(state.Mode),
		CadenceSeconds: state.CadenceSeconds,
		QueueCounts:    queueCounts,
		UpdatedAt:      state.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// SetControlMode handles POST /api/control/mode.
// Sets the mode (continuous|tick|stopped). cadence_seconds is optional (defaults to 60).
func (a *API) SetControlMode(w http.ResponseWriter, r *http.Request) {
	var req SetModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if !validModes[req.Mode] {
		writeError(w, http.StatusBadRequest, "invalid mode: must be one of continuous, tick, stopped")
		return
	}

	// Default cadence_seconds to 60 if not provided.
	cadence := int32(60)
	if req.CadenceSeconds != nil && *req.CadenceSeconds > 0 {
		cadence = *req.CadenceSeconds
	}

	// Update the mode. Note: SetControlMode currently only updates mode, not cadence.
	// If cadence needs updating, we do a direct SQL update via pool.
	state, err := a.queries.SetControlMode(r.Context(), db.ControlMode(req.Mode))
	if err != nil {
		slog.Default().Error("control: set mode failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to set control mode")
		return
	}

	// Update cadence if different from default / requested.
	// SetControlMode only updates mode, so we update cadence separately if needed.
	if cadence != state.CadenceSeconds && a.pool != nil {
		_, err := a.pool.Exec(r.Context(),
			`UPDATE control_state SET cadence_seconds = $1, updated_at = NOW()`, cadence)
		if err != nil {
			slog.Default().Error("control: set cadence failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to set cadence")
			return
		}
		// Re-read state to get updated values.
		state, err = a.queries.GetControlState(r.Context())
		if err != nil {
			slog.Default().Error("control: re-read state failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to read updated state")
			return
		}
	}

	// Get queue counts for the response.
	counts, err := a.queries.CountWorkUnitsByStatus(r.Context())
	if err != nil {
		slog.Default().Error("control: count work units failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to count work units")
		return
	}

	queueCounts := make(map[string]int64)
	for _, c := range counts {
		queueCounts[string(c.Status)] = c.Count
	}

	writeJSON(w, http.StatusOK, ControlStateResponse{
		Mode:           string(state.Mode),
		CadenceSeconds: state.CadenceSeconds,
		QueueCounts:    queueCounts,
		UpdatedAt:      state.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// ListUnits handles GET /api/control/units?status=&limit=&offset=
// Lists work units, filterable by status, newest first.
func (a *API) ListUnits(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")

	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.ParseInt(l, 10, 64); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			limit = int(n)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.ParseInt(o, 10, 64); err == nil && n >= 0 {
			offset = int(n)
		}
	}

	var units []db.WorkUnit
	var err error

	if statusFilter != "" {
		units, err = a.queries.ListWorkUnitsByStatus(r.Context(), db.ListWorkUnitsByStatusParams{
			Column1: statusFilter,
			Limit:   int32(limit),
			Offset:  int32(offset),
		})
	} else {
		// No status filter: list all, newest first via pool query.
		rows, qErr := a.pool.Query(r.Context(),
			`SELECT id, wp_ref, status, payload, claimed_at, completed_at, error, created_at
			 FROM work_units ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
			limit, offset)
		if qErr != nil {
			slog.Default().Error("control: list units failed", "error", qErr)
			writeError(w, http.StatusInternalServerError, "failed to list work units")
			return
		}
		defer rows.Close()
		for rows.Next() {
			var u db.WorkUnit
			if scanErr := rows.Scan(&u.ID, &u.WpRef, &u.Status, &u.Payload, &u.ClaimedAt, &u.CompletedAt, &u.Error, &u.CreatedAt); scanErr != nil {
				slog.Default().Error("control: scan unit failed", "error", scanErr)
				writeError(w, http.StatusInternalServerError, "failed to scan work unit")
				return
			}
			units = append(units, u)
		}
		if rows.Err() != nil {
			slog.Default().Error("control: rows iteration error", "error", rows.Err())
			writeError(w, http.StatusInternalServerError, "failed to iterate work units")
			return
		}
	}

	if err != nil {
		slog.Default().Error("control: list units failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list work units")
		return
	}

	resp := UnitListResponse{
		Units:  make([]WorkUnitResponse, 0, len(units)),
		Count:  len(units),
		Limit:  limit,
		Offset: offset,
	}
	for _, u := range units {
		resp.Units = append(resp.Units, workUnitToResponse(u))
	}

	writeJSON(w, http.StatusOK, resp)
}

// EnqueueUnit handles POST /api/control/units.
// Enqueues a new work unit with the given wp_ref and payload.
func (a *API) EnqueueUnit(w http.ResponseWriter, r *http.Request) {
	var req EnqueueUnitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.WpRef == "" {
		writeError(w, http.StatusBadRequest, "wp_ref is required")
		return
	}

	payload := req.Payload
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}

	unit, err := a.queries.EnqueueWorkUnit(r.Context(), db.EnqueueWorkUnitParams{
		WpRef:   req.WpRef,
		Payload: payload,
	})
	if err != nil {
		slog.Default().Error("control: enqueue failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to enqueue work unit")
		return
	}

	writeJSON(w, http.StatusCreated, workUnitToResponse(unit))
}

// RequeueUnit handles POST /api/control/units/{id}/requeue.
// Resets a failed/done unit back to queued. 404 if unknown or not failed/done.
func (a *API) RequeueUnit(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid unit id")
		return
	}

	unit, err := a.queries.RequeueWorkUnit(r.Context(), id)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "unit not found or not in a requeueable state (failed/done)")
			return
		}
		slog.Default().Error("control: requeue failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to requeue work unit")
		return
	}

	writeJSON(w, http.StatusOK, workUnitToResponse(unit))
}

// workUnitToResponse converts a db.WorkUnit to a JSON-friendly response struct.
func workUnitToResponse(u db.WorkUnit) WorkUnitResponse {
	resp := WorkUnitResponse{
		ID:        u.ID,
		WpRef:     u.WpRef,
		Status:    string(u.Status),
		CreatedAt: u.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}

	// Payload: decode JSONB bytes into a generic value for pretty rendering.
	if u.Payload != nil {
		var v any
		if err := json.Unmarshal(u.Payload, &v); err == nil {
			resp.Payload = v
		} else {
			resp.Payload = json.RawMessage(u.Payload)
		}
	}

	if u.ClaimedAt.Valid {
		s := u.ClaimedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
		resp.ClaimedAt = &s
	}
	if u.CompletedAt.Valid {
		s := u.CompletedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
		resp.CompletedAt = &s
	}
	if u.Error.Valid {
		resp.Error = u.Error.String
	}

	return resp
}

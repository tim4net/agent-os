package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tim4net/agent-os/internal/db"
)

// HostLivenessResponse is a single host-liveness record in the API response.
type HostLivenessResponse struct {
	ID        int64  `json:"id"`
	Host      string `json:"host"`
	PID       int32  `json:"pid"`
	SessionID string `json:"session_id"`
	Harness   string `json:"harness"`
	CWD       string `json:"cwd"`
	Tenant    string `json:"tenant"`
	Alive     bool   `json:"alive"`
	SeenAt    string `json:"seen_at"`
}

// HostLivenessListResponse is the paginated response for GET /api/host/liveness.
type HostLivenessListResponse struct {
	Records []HostLivenessResponse `json:"records"`
	Total   int64                  `json:"total"`
	Limit   int                    `json:"limit"`
	Offset  int                    `json:"offset"`
}

// LivenessReportRequest is the POST body for /api/host/liveness.
// This is sent by the host-reporter agent running on each tailnet host.
type LivenessReportRequest struct {
	Host      string `json:"host"`
	PID       int32  `json:"pid"`
	SessionID string `json:"session_id,omitempty"`
	Harness   string `json:"harness,omitempty"`
	CWD       string `json:"cwd,omitempty"`
	Tenant    string `json:"tenant,omitempty"`
	Alive     bool   `json:"alive"`
}

// HostLivenessRoutes returns a Chi router for host-liveness endpoints (WP-N).
// Mounted at /api/host/liveness by the integrator (router.go).
func (a *API) HostLivenessRoutes() http.Handler {
	r := chi.NewRouter()
	r.Post("/", a.PostHostLiveness)
	r.Get("/", a.ListHostLiveness)
	return r
}

// PostHostLiveness handles POST /api/host/liveness.
// Receives a liveness report from a host-reporter agent and upserts it.
// Used by the liveness derivation (contract §4) to determine bounded-session status.
func (a *API) PostHostLiveness(w http.ResponseWriter, r *http.Request) {
	var req LivenessReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Validate required fields
	if req.Host == "" {
		writeError(w, http.StatusBadRequest, "host is required")
		return
	}
	if req.PID <= 0 {
		writeError(w, http.StatusBadRequest, "pid must be a positive integer")
		return
	}

	// Default tenant to "personal" if not specified
	if req.Tenant == "" {
		req.Tenant = "personal"
	}

	record, err := a.queries.UpsertHostLiveness(r.Context(), db.UpsertHostLivenessParams{
		Host:      req.Host,
		Pid:       req.PID,
		SessionID: req.SessionID,
		Harness:   req.Harness,
		Cwd:       req.CWD,
		Tenant:    req.Tenant,
		Alive:     req.Alive,
	})
	if err != nil {
		slog.Default().Error("host_liveness: upsert failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to upsert liveness record")
		return
	}

	writeJSON(w, http.StatusOK, hostLivenessToResponse(record))
}

// ListHostLiveness handles GET /api/host/liveness?tenant=...&limit=50&offset=0
// Lists host-liveness records, scoped to tenant. Tenant is optional — empty returns all.
func (a *API) ListHostLiveness(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")

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

	records, err := a.queries.ListHostLiveness(r.Context(), db.ListHostLivenessParams{
		Tenant: tenant,
		Lim:    int32(limit),
		Off:    int32(offset),
	})
	if err != nil {
		slog.Default().Error("host_liveness: list failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list liveness records")
		return
	}

	total, err := a.queries.CountHostLiveness(r.Context(), tenant)
	if err != nil {
		slog.Default().Error("host_liveness: count failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to count liveness records")
		return
	}

	resp := HostLivenessListResponse{
		Records: make([]HostLivenessResponse, 0, len(records)),
		Total:   total,
		Limit:   limit,
		Offset:  offset,
	}
	for _, rec := range records {
		resp.Records = append(resp.Records, hostLivenessToResponse(rec))
	}

	writeJSON(w, http.StatusOK, resp)
}

// hostLivenessToResponse converts a db.HostLiveness row to a JSON response struct.
// Follows the same pgtype.Timestamptz serialization pattern as instanceToResponse
// (instances.go): NOT NULL timestamptz columns are always valid, so we access
// .Time directly without guarding on .Valid.
func hostLivenessToResponse(rec db.HostLiveness) HostLivenessResponse {
	return HostLivenessResponse{
		ID:        rec.ID,
		Host:      rec.Host,
		PID:       rec.Pid,
		SessionID: rec.SessionID,
		Harness:   rec.Harness,
		CWD:       rec.Cwd,
		Tenant:    rec.Tenant,
		Alive:     rec.Alive,
		SeenAt:    rec.SeenAt.Time.UTC().Format(time.RFC3339),
	}
}

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// InstanceResponse is a single instance in the API response.
type InstanceResponse struct {
	ID            string  `json:"id"`
	Harness       string  `json:"harness"`
	SessionID     string  `json:"session_id"`
	Host          string  `json:"host"`
	PID           *int    `json:"pid,omitempty"`
	Label         string  `json:"label"`
	HealthURL     string  `json:"health_url"`
	Branch        *string `json:"branch,omitempty"`
	SHA           *string `json:"sha,omitempty"`
	CWD           *string `json:"cwd,omitempty"`
	Tenant        string  `json:"tenant"`
	Status        string  `json:"status"`
	LastProbedAt  *string `json:"last_probed_at,omitempty"`
	LastHeartbeat *string `json:"last_heartbeat,omitempty"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// InstancesListResponse is the paginated response for GET /api/instances.
type InstancesListResponse struct {
	Instances []InstanceResponse `json:"instances"`
	Total     int64              `json:"total"`
	Limit     int                `json:"limit"`
	Offset    int                `json:"offset"`
}

// ProbeResponse is the response from POST /api/instances/{id}/probe.
type ProbeResponse struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	ProbedAt   string `json:"probed_at"`
	StatusCode int    `json:"status_code"`
	ResponseMs int64  `json:"response_ms"`
	Error      string `json:"error,omitempty"`
}

// InstanceRoutes returns a Chi router for instance endpoints (WP-I).
// Mounted at /api/instances by the integrator (router.go).
func (a *API) InstanceRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.ListInstances)
	r.Post("/", a.CreateInstance)
	r.Post("/{id}/probe", a.ProbeInstance)
	r.Get("/{id}", a.GetInstance)
	return r
}

// appInstanceToResponse converts a db.AppInstance to an InstanceResponse JSON struct.
func appInstanceToResponse(inst db.AppInstance) InstanceResponse {
	resp := InstanceResponse{
		ID:        inst.ID.String(),
		Harness:   inst.Harness,
		SessionID: inst.SessionID,
		Host:      inst.Host,
		Label:     inst.Label,
		HealthURL: inst.HealthUrl,
		Tenant:    inst.Tenant,
		Status:    inst.Status,
		CreatedAt: inst.CreatedAt.Time.UTC().Format(time.RFC3339),
		UpdatedAt: inst.UpdatedAt.Time.UTC().Format(time.RFC3339),
	}
	if inst.Pid.Valid {
		pid := int(inst.Pid.Int32)
		resp.PID = &pid
	}
	if inst.Branch.Valid {
		resp.Branch = &inst.Branch.String
	}
	if inst.Sha.Valid {
		resp.SHA = &inst.Sha.String
	}
	if inst.Cwd.Valid {
		resp.CWD = &inst.Cwd.String
	}
	if inst.LastProbedAt.Valid {
		t := inst.LastProbedAt.Time.UTC().Format(time.RFC3339)
		resp.LastProbedAt = &t
	}
	if inst.LastHeartbeat.Valid {
		t := inst.LastHeartbeat.Time.UTC().Format(time.RFC3339)
		resp.LastHeartbeat = &t
	}
	return resp
}

// ListInstances handles GET /api/instances?tenant=...&limit=50&offset=0
func (a *API) ListInstances(w http.ResponseWriter, r *http.Request) {
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

	instances, err := a.queries.ListAppInstances(r.Context(), db.ListAppInstancesParams{
		Tenant: tenant,
		Lim:    int32(limit),
		Off:    int32(offset),
	})
	if err != nil {
		slog.Default().Error("instances: list failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list instances")
		return
	}

	total, err := a.queries.CountAppInstances(r.Context(), tenant)
	if err != nil {
		slog.Default().Error("instances: count failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to count instances")
		return
	}

	resp := InstancesListResponse{
		Instances: make([]InstanceResponse, 0, len(instances)),
		Total:     total,
		Limit:     limit,
		Offset:    offset,
	}
	for _, inst := range instances {
		resp.Instances = append(resp.Instances, appInstanceToResponse(inst))
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetInstance handles GET /api/instances/{id}
func (a *API) GetInstance(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance id")
		return
	}

	inst, err := a.queries.GetAppInstance(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}

	writeJSON(w, http.StatusOK, appInstanceToResponse(inst))
}

// CreateInstance handles POST /api/instances (manual add)
// Body: JSON with harness, host, label, health_url, tenant, and optional branch/sha/cwd/pid.
func (a *API) CreateInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Harness   string  `json:"harness"`
		Host      string  `json:"host"`
		Label     string  `json:"label"`
		HealthURL string  `json:"health_url"`
		Tenant    string  `json:"tenant"`
		Branch    *string `json:"branch,omitempty"`
		SHA       *string `json:"sha,omitempty"`
		CWD       *string `json:"cwd,omitempty"`
		PID       *int    `json:"pid,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Validate required fields
	if req.Host == "" {
		writeError(w, http.StatusBadRequest, "host is required")
		return
	}
	if req.HealthURL == "" {
		writeError(w, http.StatusBadRequest, "health_url is required")
		return
	}
	if req.Tenant == "" {
		req.Tenant = "personal"
	}

	// Build optional pgtype fields
	var branch pgtype.Text
	if req.Branch != nil {
		branch.String = *req.Branch
		branch.Valid = true
	}
	var sha pgtype.Text
	if req.SHA != nil {
		sha.String = *req.SHA
		sha.Valid = true
	}
	var cwd pgtype.Text
	if req.CWD != nil {
		cwd.String = *req.CWD
		cwd.Valid = true
	}
	var pid pgtype.Int4
	if req.PID != nil {
		pid.Int32 = int32(*req.PID)
		pid.Valid = true
	}

	inst, err := a.queries.UpsertAppInstanceByHostURL(r.Context(), db.UpsertAppInstanceByHostURLParams{
		Harness:   req.Harness,
		Host:      req.Host,
		Label:     req.Label,
		HealthUrl: req.HealthURL,
		Tenant:    req.Tenant,
		Branch:    branch,
		Sha:       sha,
		Cwd:       cwd,
		Pid:       pid,
		SessionID: "",
	})
	if err != nil {
		slog.Default().Error("instances: create failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create instance")
		return
	}

	writeJSON(w, http.StatusCreated, appInstanceToResponse(inst))
}

// ProbeInstance handles POST /api/instances/{id}/probe
// Performs a real HTTP health probe against the instance's health_url.
// Updates the instance status based on the probe result.
func (a *API) ProbeInstance(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		writeError(w, http.StatusBadRequest, "invalid instance id")
		return
	}

	inst, err := a.queries.GetAppInstance(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}

	if inst.HealthUrl == "" {
		writeError(w, http.StatusBadRequest, "instance has no health_url configured")
		return
	}

	// Perform the real HTTP probe
	prober := service.NewInstanceProber(service.DefaultProberConfig())
	result := prober.Probe(r.Context(), inst.HealthUrl)

	// Update the instance status in the DB from the probe result
	err = a.queries.UpdateInstanceProbeStatus(r.Context(), db.UpdateInstanceProbeStatusParams{
		ID:           id,
		Status:       string(result.Status),
		LastProbedAt: pgtype.Timestamptz{Time: result.ProbedAt, Valid: true},
	})
	if err != nil {
		slog.Default().Error("instances: update probe status failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update probe status")
		return
	}

	resp := ProbeResponse{
		ID:         idStr,
		Status:     string(result.Status),
		ProbedAt:   result.ProbedAt.UTC().Format(time.RFC3339),
		StatusCode: result.StatusCode,
		ResponseMs: result.ResponseTime.Milliseconds(),
	}
	if result.Error != nil {
		resp.Error = result.Error.Error()
	}

	writeJSON(w, http.StatusOK, resp)
}

// Suppress unused import guard
var _ = strings.NewReader

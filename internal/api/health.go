package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	startTime   = time.Now()
	buildVersion = "0.6.0"
	containerID = ""
)

func init() {
	// Try to read container ID
	if data, err := os.ReadFile("/etc/hostname"); err == nil {
		containerID = strings.TrimSpace(string(data))
	}
}

// HealthResponse is the detailed health check response.
type HealthResponse struct {
	Status      string            `json:"status"`
	Service     string            `json:"service"`
	Version     string            `json:"version"`
	Uptime      string            `json:"uptime"`
	UptimeSecs  float64           `json:"uptime_seconds"`
	ContainerID string            `json:"container_id,omitempty"`
	Components  map[string]string `json:"components"`
}

// DetailedHealth handles GET /api/health
func (a *API) DetailedHealth(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(startTime)
	components := make(map[string]string)

	// Check database connection
	dbStatus := "ok"
	_, err := a.queries.ListAgents(r.Context())
	if err != nil {
		dbStatus = "unhealthy"
		slog.Error("health check: database ping failed", "error", err)
	}
	components["database"] = dbStatus

	overall := "ok"
	if dbStatus != "ok" {
		overall = "degraded"
	}

	resp := HealthResponse{
		Status:      overall,
		Service:     "agent-os",
		Version:     buildVersion,
		Uptime:      uptime.Truncate(time.Second).String(),
		UptimeSecs:  uptime.Seconds(),
		ContainerID: containerID,
		Components:  components,
	}

	statusCode := http.StatusOK
	if overall != "ok" {
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(resp)
}

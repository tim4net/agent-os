package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/tim4net/agent-os/internal/service"
)

// WorkEventRoutes returns a router for work-event ingestion endpoints.
func (a *API) WorkEventRoutes() http.Handler {
	r := chi.NewRouter()
	r.Post("/work", a.IngestWorkEvent)
	return r
}

// IngestWorkEvent handles POST /api/events/work — generic work-event ingestion.
func (a *API) IngestWorkEvent(w http.ResponseWriter, r *http.Request) {
	// Check X-AgentOS-Ingest-Key header
	ingestKey := r.Header.Get("X-AgentOS-Ingest-Key")
	if ingestKey == "" {
		writeError(w, http.StatusForbidden, "missing X-AgentOS-Ingest-Key header")
		return
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	// Strict unknown-key detection
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	var unknownKeys []string
	for k := range raw {
		if !service.KnownKeys[k] {
			unknownKeys = append(unknownKeys, k)
		}
	}
	if len(unknownKeys) > 0 {
		sort.Strings(unknownKeys)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown top-level keys: %s", strings.Join(unknownKeys, ", ")))
		return
	}

	// Decode into struct
	var req service.WorkEventRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Idempotency-Key header check
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey != "" && idempotencyKey != req.EventID {
		log := slog.Default()
		log.Warn("Idempotency-Key header does not match body event_id",
			"header", idempotencyKey,
			"body", req.EventID,
		)
	}

	// Create IngestService
	artifactsPath := os.Getenv("AGENTOS_ARTIFACTS_PATH")
	if artifactsPath == "" {
		artifactsPath = "/data/artifacts"
	}
	svc := service.NewIngestService(a.queries, a.bus, slog.Default(), artifactsPath)

	// Ingest
	event, status, err := svc.Ingest(r.Context(), req)
	if err != nil {
		if ve, ok := err.(*service.ValidationError); ok {
			writeError(w, ve.HTTPStatus, ve.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"id":       event.ID.String(),
		"accepted": true,
	})
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	slog.Default().Warn("work event error", "status", status, "error", message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}

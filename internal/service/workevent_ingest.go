package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ValidationError is a structured validation error with an HTTP status code.
type ValidationError struct {
	HTTPStatus int
	Message    string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// ArtifactDescriptor represents an artifact in the work-event request.
type ArtifactDescriptor struct {
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
	Name string `json:"name,omitempty"`
	Mime string `json:"mime,omitempty"`
}

// WorkEventRequest is the request body for ingesting a work event.
type WorkEventRequest struct {
	Schema       string               `json:"schema"`
	EventID      string               `json:"event_id"`
	Host         string               `json:"host"`
	Harness      string               `json:"harness"`
	Kind         string               `json:"kind"`
	SessionID    string               `json:"session_id"`
	Ts           string               `json:"ts"`
	Status       string               `json:"status,omitempty"`
	LivenessMode string               `json:"liveness_mode,omitempty"`
	Pid          *int                 `json:"pid,omitempty"`
	ProjectHint  string               `json:"project_hint,omitempty"`
	ExternalRef  string               `json:"external_ref,omitempty"`
	Branch       string               `json:"branch,omitempty"`
	Sha          string               `json:"sha,omitempty"`
	Cwd          string               `json:"cwd,omitempty"`
	Tenant       string               `json:"tenant,omitempty"`
	Title        string               `json:"title,omitempty"`
	Artifacts    []ArtifactDescriptor `json:"artifacts,omitempty"`
	CostUsd      *float64             `json:"cost_usd,omitempty"`
	Payload      json.RawMessage      `json:"payload,omitempty"`
}

// Known top-level keys for strict unknown-key detection.
var KnownKeys = map[string]bool{
	"schema": true, "event_id": true, "host": true, "harness": true,
	"kind": true, "session_id": true, "ts": true, "status": true,
	"liveness_mode": true, "pid": true, "project_hint": true,
	"external_ref": true, "branch": true, "sha": true, "cwd": true,
	"tenant": true, "title": true, "artifacts": true, "cost_usd": true,
	"payload": true,
}

var validHarnesses = map[string]bool{
	"hermes": true, "claude": true, "antigravity": true, "codex": true, "generic": true,
}

var validKinds = map[string]bool{
	"session.start": true, "session.heartbeat": true, "session.end": true,
	"artifact.created": true, "server.started": true, "server.stopped": true, "note": true,
}

var terminalStatuses = map[string]bool{
	"done": true, "failed": true, "cancelled": true,
}

var runningStatuses = map[string]bool{
	"running": true, "unknown": true,
}

// NOTE: tenant enum validation ("personal", "dayjob", etc.) will be enforced once
// config.go is wired with the key→allowed-tenant map. Until then, the body tenant
// field is ignored (overridden by ResolveTenantFromKey) and the server accepts the
// key-resolved tenant. The contract's "tenant not permitted by ingest key → 403"
// AC is explicitly deferred to the config.go follow-up. See issue #2 comments.

var externalRefPattern = regexp.MustCompile(`^(SC-\d+|#\d+)$`)

const maxPayloadSize = 64 * 1024 // 64KB

// IngestService handles validation, persistence, project resolution, and SSE publishing.
type IngestService struct {
	queries       db.Querier
	bus           *EventBus
	log           *slog.Logger
	artifactsPath string
}

// NewIngestService creates a new IngestService.
func NewIngestService(queries db.Querier, bus *EventBus, log *slog.Logger, artifactsPath string) *IngestService {
	return &IngestService{
		queries:       queries,
		bus:           bus,
		log:           log,
		artifactsPath: artifactsPath,
	}
}

// ResolveTenantFromKey resolves a tenant string from an ingest key.
// TODO(WP-A finding #3): once config.go is wired, this should look up the key
// in a server-side key→tenant mapping and return the allowed tenant set.
// For now, all non-empty keys resolve to "personal" and the body tenant is ignored.
func ResolveTenantFromKey(ingestKey string) (string, error) {
	if ingestKey == "" {
		return "", fmt.Errorf("missing ingest key")
	}
	// TODO: look up key in config.go key→tenant map; return error if key unknown
	return "personal", nil
}

// ValidateWorkEvent validates a WorkEventRequest against the frozen contract.
func ValidateWorkEvent(req WorkEventRequest) error {
	// schema
	if req.Schema != "agentos.work_event/v1" {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("invalid schema: must be \"agentos.work_event/v1\", got %q", req.Schema)}
	}

	// event_id required, valid UUID
	if req.EventID == "" {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "event_id is required"}
	}
	if !isValidUUID(req.EventID) {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "event_id must be a valid UUID"}
	}

	// host required
	if req.Host == "" {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "host is required"}
	}

	// harness required, enum
	if req.Harness == "" {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "harness is required"}
	}
	if !validHarnesses[req.Harness] {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("invalid harness: %q", req.Harness)}
	}

	// kind required, enum
	if req.Kind == "" {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "kind is required"}
	}
	if !validKinds[req.Kind] {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("invalid kind: %q", req.Kind)}
	}

	// session_id required
	if req.SessionID == "" {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "session_id is required"}
	}

	// ts required, valid RFC3339, within ±10 min
	if req.Ts == "" {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "ts is required"}
	}
	ts, err := time.Parse(time.RFC3339, req.Ts)
	if err != nil {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("ts must be valid RFC3339: %v", err)}
	}
	now := time.Now().UTC()
	diff := now.Sub(ts.UTC())
	if diff < 0 {
		diff = -diff
	}
	if diff > 10*time.Minute {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("ts must be within ±10 minutes of server time (offset: %v)", diff)}
	}

	// Conditional status validation
	isSessionKind := req.Kind == "session.start" || req.Kind == "session.heartbeat" || req.Kind == "session.end"
	if isSessionKind {
		if req.Status == "" {
			return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("status is required for kind %q", req.Kind)}
		}
		switch req.Kind {
		case "session.end":
			if !terminalStatuses[req.Status] {
				return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("status for session.end must be one of [done, failed, cancelled], got %q", req.Status)}
			}
		case "session.start", "session.heartbeat":
			if !runningStatuses[req.Status] {
				return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("status for %s must be one of [running, unknown], got %q", req.Kind, req.Status)}
			}
		}
	} else {
		// For non-session kinds, status must be absent or "unknown"
		if req.Status != "" && req.Status != "unknown" {
			return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("status must be absent or \"unknown\" for kind %q, got %q", req.Kind, req.Status)}
		}
	}

	// session.start ⇒ liveness_mode must be present
	if req.Kind == "session.start" {
		if req.LivenessMode != "supervised" && req.LivenessMode != "bounded" {
			return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "liveness_mode is required for session.start (supervised or bounded)"}
		}
	}

	// session.heartbeat ⇒ liveness_mode must be supervised (contract §1)
	// FIX: was only rejecting "bounded"; now rejects anything that isn't "supervised"
	if req.Kind == "session.heartbeat" && req.LivenessMode != "supervised" {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "session.heartbeat requires liveness_mode supervised (bounded emitters cannot heartbeat)"}
	}

	// liveness_mode: supervised ⇒ pid required
	if req.LivenessMode == "supervised" {
		if req.Pid == nil {
			return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "pid is required when liveness_mode is supervised"}
		}
	}

	// cost_usd ≥ 0
	if req.CostUsd != nil && *req.CostUsd < 0 {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: "cost_usd must be >= 0"}
	}

	// payload capped at 64KB
	if len(req.Payload) > maxPayloadSize {
		return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("payload exceeds 64KB limit (%d bytes)", len(req.Payload))}
	}

	// artifacts validation
	for i, art := range req.Artifacts {
		hasPath := art.Path != ""
		hasURL := art.URL != ""
		if !hasPath && !hasURL {
			return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("artifact[%d]: must have exactly one of path or url", i)}
		}
		if hasPath && hasURL {
			return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("artifact[%d]: must have exactly one of path or url, not both", i)}
		}
		if hasPath {
			if err := validateArtifactPath(art.Path); err != nil {
				return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("artifact[%d]: %v", i, err)}
			}
		}
		// FIX (minor #7): URL SSRF guard — reject private/link-local ranges
		if hasURL {
			if err := validateArtifactURL(art.URL); err != nil {
				return &ValidationError{HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("artifact[%d]: %v", i, err)}
			}
		}
	}

	return nil
}

// validateArtifactPath ensures the path is relative, doesn't traverse, and is under the artifacts root.
func validateArtifactPath(p string) error {
	// Must not be empty (already checked)
	if filepath.IsAbs(p) {
		return fmt.Errorf("path must be relative, got absolute: %s", p)
	}
	// Clean and check for traversal
	cleaned := filepath.Clean(p)
	if strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("path must not contain traversal (..): %s", p)
	}
	return nil
}

// validateArtifactURL rejects URLs that resolve to private/link-local ranges (SSRF guard, contract §1/§3).
func validateArtifactURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	// Reject common localhost hostnames
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
		return fmt.Errorf("URL must not resolve to a private/link-local range")
	}
	// Reject loopback, link-local, and private IP ranges (RFC 1918 + RFC 3927)
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
			return fmt.Errorf("URL must not resolve to a private/link-local range")
		}
	}
	return nil
}

// isValidUUID checks if a string is a valid UUID.
func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// resolveProject resolves a project from project_hint or cwd.
func (s *IngestService) resolveProject(ctx context.Context, tenant string, req WorkEventRequest) (pgtype.UUID, error) {
	slug := ""
	if req.ProjectHint != "" {
		slug = req.ProjectHint
	} else if req.Cwd != "" {
		slug = filepath.Base(req.Cwd)
	}
	if slug == "" {
		return pgtype.UUID{}, nil
	}

	// FIX (minor #8): distinguish "no hint" from "EnsureProjectBySlug failed"
	project, err := s.queries.EnsureProjectBySlug(ctx, db.EnsureProjectBySlugParams{
		Slug:   slug,
		Name:   slug,
		Tenant: tenant,
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("failed to resolve/create project %q for tenant %q: %w", slug, tenant, err)
	}
	return project.ID, nil
}

// Ingest validates, resolves the project, persists (upsert), and publishes.
func (s *IngestService) Ingest(ctx context.Context, req WorkEventRequest) (db.WorkEvent, int, error) {
	// Validate
	if err := ValidateWorkEvent(req); err != nil {
		if ve, ok := err.(*ValidationError); ok {
			s.log.Warn("work event validation failed", "error", ve.Message)
			return db.WorkEvent{}, ve.HTTPStatus, ve
		}
		return db.WorkEvent{}, http.StatusBadRequest, err
	}

	// Warn on non-matching external_ref pattern (best-effort)
	if req.ExternalRef != "" && !externalRefPattern.MatchString(req.ExternalRef) {
		s.log.Warn("external_ref does not match expected pattern", "external_ref", req.ExternalRef)
	}

	// Resolve project — FIX (minor #8): surface errors instead of swallowing them
	tenant := req.Tenant
	if tenant == "" {
		tenant = "personal"
	}
	var projectID pgtype.UUID
	projectID, err := s.resolveProject(ctx, tenant, req)
	if err != nil {
		s.log.Error("project resolution failed", "error", err)
		return db.WorkEvent{}, http.StatusInternalServerError, fmt.Errorf("project resolution: %w", err)
	}

	// Parse event_id to UUID
	var eventUUID pgtype.UUID
	if err := eventUUID.Scan(req.EventID); err != nil {
		return db.WorkEvent{}, http.StatusBadRequest, &ValidationError{
			HTTPStatus: http.StatusBadRequest,
			Message:    fmt.Sprintf("invalid event_id UUID: %v", err),
		}
	}

	// Parse ts
	ts, _ := time.Parse(time.RFC3339, req.Ts)
	var pgTs pgtype.Timestamptz
	pgTs.Scan(ts)

	// Build status
	var pgStatus pgtype.Text
	if req.Status != "" {
		pgStatus = pgtype.Text{String: req.Status, Valid: true}
	}

	// Build liveness_mode
	var pgLivenessMode pgtype.Text
	if req.LivenessMode != "" {
		pgLivenessMode = pgtype.Text{String: req.LivenessMode, Valid: true}
	}

	// Build pid
	var pgPid pgtype.Int4
	if req.Pid != nil {
		pgPid = pgtype.Int4{Int32: int32(*req.Pid), Valid: true}
	}

	// Build external_ref
	var pgExternalRef pgtype.Text
	if req.ExternalRef != "" {
		pgExternalRef = pgtype.Text{String: req.ExternalRef, Valid: true}
	}

	// Build branch
	var pgBranch pgtype.Text
	if req.Branch != "" {
		pgBranch = pgtype.Text{String: req.Branch, Valid: true}
	}

	// Build sha
	var pgSha pgtype.Text
	if req.Sha != "" {
		pgSha = pgtype.Text{String: req.Sha, Valid: true}
	}

	// Build cwd
	var pgCwd pgtype.Text
	if req.Cwd != "" {
		pgCwd = pgtype.Text{String: req.Cwd, Valid: true}
	}

	// Build title
	var pgTitle pgtype.Text
	if req.Title != "" {
		pgTitle = pgtype.Text{String: req.Title, Valid: true}
	}

	// Build cost_usd
	var pgCostUsd pgtype.Numeric
	if req.CostUsd != nil {
		pgCostUsd.Scan(fmt.Sprintf("%f", *req.CostUsd))
	}

	// Build payload
	payload := []byte("{}")
	if len(req.Payload) > 0 {
		payload = req.Payload
	}

	// FIX (finding #1): Atomic upsert — single INSERT ON CONFLICT DO NOTHING.
	// On conflict, PostgreSQL returns no rows (pgx.ErrNoRows) → we SELECT the existing row.
	params := db.InsertWorkEventParams{
		EventID:       eventUUID,
		SchemaVersion: req.Schema,
		Harness:       req.Harness,
		SessionID:     req.SessionID,
		Host:          req.Host,
		Pid:           pgPid,
		Kind:          req.Kind,
		Status:        pgStatus,
		LivenessMode:  pgLivenessMode,
		ProjectID:     projectID,
		Tenant:        tenant,
		ExternalRef:   pgExternalRef,
		Branch:        pgBranch,
		Sha:           pgSha,
		Cwd:           pgCwd,
		Title:         pgTitle,
		CostUsd:       pgCostUsd,
		Payload:       payload,
		Ts:            pgTs,
	}

	row, err := s.queries.InsertWorkEvent(ctx, params)
	if err == pgx.ErrNoRows {
		// Conflict — event_id already exists. Fetch the original row.
		existing, err := s.queries.GetWorkEventByEventID(ctx, eventUUID)
		if err != nil {
			s.log.Error("failed to fetch existing event on conflict", "event_id", req.EventID, "error", err)
			return db.WorkEvent{}, http.StatusInternalServerError, fmt.Errorf("fetch existing event: %w", err)
		}
		s.log.Info("idempotent duplicate accepted", "event_id", req.EventID, "existing_id", existing.ID.String())
		return existing, http.StatusAccepted, nil
	}
	if err != nil {
		s.log.Error("failed to insert work event", "error", err)
		return db.WorkEvent{}, http.StatusInternalServerError, fmt.Errorf("db insert failed: %w", err)
	}

	// Publish SSE event (201 only — new row)
	if s.bus != nil {
		s.bus.PublishTyped("work_event", map[string]any{
			"id":         row.ID.String(),
			"event_id":   row.EventID.String(),
			"harness":    row.Harness,
			"session_id": row.SessionID,
			"host":       row.Host,
			"kind":       row.Kind,
			"status":     row.Status,
			"tenant":     row.Tenant,
		})
	}

	return row, http.StatusCreated, nil
}

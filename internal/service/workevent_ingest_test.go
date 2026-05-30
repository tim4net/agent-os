package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// validRequest returns a minimal valid WorkEventRequest for session.start.
func validRequest() WorkEventRequest {
	pid := 12345
	return WorkEventRequest{
		Schema:       "agentos.work_event/v1",
		EventID:      "b1e2f3a4-5678-9abc-def0-123456789abc",
		Host:         "zbook",
		Harness:      "hermes",
		Kind:         "session.start",
		SessionID:    "75e2167f-1234-5678-9abc-def012345678",
		Ts:           time.Now().Format(time.RFC3339),
		Status:       "running",
		LivenessMode: "supervised",
		Pid:          &pid,
		Tenant:       "personal",
	}
}

func TestValidateWorkEvent(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(req *WorkEventRequest)
		wantErr string // substring of expected error message, empty means expect success
	}{
		{
			name:    "valid event passes validation",
			mutate:  func(req *WorkEventRequest) {},
			wantErr: "",
		},
		{
			name:    "missing event_id",
			mutate:  func(req *WorkEventRequest) { req.EventID = "" },
			wantErr: "event_id is required",
		},
		{
			name:    "invalid event_id (not UUID)",
			mutate:  func(req *WorkEventRequest) { req.EventID = "not-a-uuid" },
			wantErr: "event_id must be a valid UUID",
		},
		{
			name:    "missing host",
			mutate:  func(req *WorkEventRequest) { req.Host = "" },
			wantErr: "host is required",
		},
		{
			name:    "missing harness",
			mutate:  func(req *WorkEventRequest) { req.Harness = "" },
			wantErr: "harness is required",
		},
		{
			name:    "invalid harness enum",
			mutate:  func(req *WorkEventRequest) { req.Harness = "invalid_harness" },
			wantErr: "invalid harness",
		},
		{
			name:    "missing kind",
			mutate:  func(req *WorkEventRequest) { req.Kind = "" },
			wantErr: "kind is required",
		},
		{
			name:    "invalid kind enum",
			mutate:  func(req *WorkEventRequest) { req.Kind = "invalid.kind" },
			wantErr: "invalid kind",
		},
		{
			name:    "missing session_id",
			mutate:  func(req *WorkEventRequest) { req.SessionID = "" },
			wantErr: "session_id is required",
		},
		{
			name:    "missing ts",
			mutate:  func(req *WorkEventRequest) { req.Ts = "" },
			wantErr: "ts is required",
		},
		{
			name:    "invalid ts format",
			mutate:  func(req *WorkEventRequest) { req.Ts = "not-a-timestamp" },
			wantErr: "ts must be valid RFC3339",
		},
		{
			name:    "ts too far in the future",
			mutate:  func(req *WorkEventRequest) { req.Ts = time.Now().Add(20 * time.Minute).Format(time.RFC3339) },
			wantErr: "ts must be within",
		},
		{
			name:    "ts too far in the past",
			mutate:  func(req *WorkEventRequest) { req.Ts = time.Now().Add(-20 * time.Minute).Format(time.RFC3339) },
			wantErr: "ts must be within",
		},
		{
			name:    "invalid schema version",
			mutate:  func(req *WorkEventRequest) { req.Schema = "agentos.work_event/v2" },
			wantErr: "invalid schema",
		},
		{
			name:    "session.end with status:running",
			mutate:  func(req *WorkEventRequest) { req.Kind = "session.end"; req.Status = "running" },
			wantErr: "status for session.end must be one of",
		},
		{
			name:    "session.end with status:done is valid",
			mutate:  func(req *WorkEventRequest) { req.Kind = "session.end"; req.Status = "done" },
			wantErr: "",
		},
		{
			name:    "session.end with status:failed is valid",
			mutate:  func(req *WorkEventRequest) { req.Kind = "session.end"; req.Status = "failed" },
			wantErr: "",
		},
		{
			name:    "session.end with status:cancelled is valid",
			mutate:  func(req *WorkEventRequest) { req.Kind = "session.end"; req.Status = "cancelled" },
			wantErr: "",
		},
		{
			name:    "session.end without status",
			mutate:  func(req *WorkEventRequest) { req.Kind = "session.end"; req.Status = "" },
			wantErr: "status is required for kind",
		},
		{
			name:    "artifact.created with status:running",
			mutate:  func(req *WorkEventRequest) { req.Kind = "artifact.created"; req.Status = "running"; req.LivenessMode = ""; req.Pid = nil },
			wantErr: "status must be absent or \"unknown\"",
		},
		{
			name:    "artifact.created with status:unknown is valid",
			mutate:  func(req *WorkEventRequest) { req.Kind = "artifact.created"; req.Status = "unknown"; req.LivenessMode = ""; req.Pid = nil },
			wantErr: "",
		},
		{
			name:    "artifact.created with no status is valid",
			mutate:  func(req *WorkEventRequest) { req.Kind = "artifact.created"; req.Status = ""; req.LivenessMode = ""; req.Pid = nil },
			wantErr: "",
		},
		{
			name:    "session.start without liveness_mode",
			mutate:  func(req *WorkEventRequest) { req.LivenessMode = "" },
			wantErr: "liveness_mode is required for session.start",
		},
		{
			name:    "session.heartbeat with bounded liveness_mode",
			mutate:  func(req *WorkEventRequest) { req.Kind = "session.heartbeat"; req.LivenessMode = "bounded" },
			wantErr: "session.heartbeat requires liveness_mode supervised",
		},
		// FIX (finding #2): heartbeat with empty/missing liveness_mode → 400
		{
			name:    "session.heartbeat with empty liveness_mode",
			mutate:  func(req *WorkEventRequest) { req.Kind = "session.heartbeat"; req.LivenessMode = "" },
			wantErr: "session.heartbeat requires liveness_mode supervised",
		},
		{
			name:    "session.heartbeat with unknown liveness_mode",
			mutate:  func(req *WorkEventRequest) { req.Kind = "session.heartbeat"; req.LivenessMode = "invalid" },
			wantErr: "session.heartbeat requires liveness_mode supervised",
		},
		{
			name:    "supervised without pid",
			mutate:  func(req *WorkEventRequest) { req.Pid = nil },
			wantErr: "pid is required when liveness_mode is supervised",
		},
		{
			name:    "cost_usd negative",
			mutate:  func(req *WorkEventRequest) { v := -1.0; req.CostUsd = &v },
			wantErr: "cost_usd must be >= 0",
		},
		{
			name:    "cost_usd zero is valid",
			mutate:  func(req *WorkEventRequest) { v := 0.0; req.CostUsd = &v },
			wantErr: "",
		},
		{
			name:    "payload exceeds 64KB",
			mutate:  func(req *WorkEventRequest) { req.Payload = make(json.RawMessage, 65*1024) },
			wantErr: "payload exceeds 64KB",
		},
		{
			name:    "payload at exactly 64KB is valid",
			mutate:  func(req *WorkEventRequest) { req.Payload = make(json.RawMessage, 64*1024) },
			wantErr: "",
		},
		{
			name:    "payload under 64KB is valid",
			mutate:  func(req *WorkEventRequest) { req.Payload = json.RawMessage(`{"key":"value"}`) },
			wantErr: "",
		},
		{
			name:    "session.start with bounded liveness_mode is valid",
			mutate:  func(req *WorkEventRequest) { req.LivenessMode = "bounded"; req.Pid = nil },
			wantErr: "",
		},
		{
			name:    "session.start with unknown liveness_mode",
			mutate:  func(req *WorkEventRequest) { req.LivenessMode = "unknown" },
			wantErr: "liveness_mode is required for session.start",
		},
		{
			name:    "note kind with no status is valid",
			mutate:  func(req *WorkEventRequest) { req.Kind = "note"; req.Status = ""; req.LivenessMode = ""; req.Pid = nil },
			wantErr: "",
		},
		{
			name:    "server.started kind with no status is valid",
			mutate:  func(req *WorkEventRequest) { req.Kind = "server.started"; req.Status = ""; req.LivenessMode = ""; req.Pid = nil },
			wantErr: "",
		},
		{
			name:    "server.stopped kind with no status is valid",
			mutate:  func(req *WorkEventRequest) { req.Kind = "server.stopped"; req.Status = ""; req.LivenessMode = ""; req.Pid = nil },
			wantErr: "",
		},
		// FIX (finding #3): well-formed but un-correlatable event is still valid
		{
			name:    "well-formed event without external_ref or branch is valid",
			mutate:  func(req *WorkEventRequest) { req.ExternalRef = ""; req.Branch = ""; req.Sha = "" },
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			tt.mutate(&req)

			err := ValidateWorkEvent("", req)

			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else {
					if !strings.Contains(err.Error(), tt.wantErr) {
						t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
					}
					// Verify it's a ValidationError with 400 status
					if ve, ok := err.(*ValidationError); ok {
						if ve.HTTPStatus != http.StatusBadRequest {
							t.Errorf("expected HTTP status 400, got %d", ve.HTTPStatus)
						}
					} else {
						t.Errorf("expected *ValidationError, got %T", err)
					}
				}
			}
		})
	}
}

func TestValidateArtifactPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"relative path", "some/path.png", false},
		{"absolute path", "/etc/passwd", true},
		{"traversal", "../etc/passwd", true},
		{"nested traversal", "foo/../../etc/passwd", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateArtifactPath("", tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateArtifactPath(%q) = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

// TestValidateArtifactPathWithRoot proves contract §3 adherence:
// absolute paths under the configured artifact root are accepted.
func TestValidateArtifactPathWithRoot(t *testing.T) {
	root := "/data/artifacts"
	tests := []struct {
		name    string
		root    string
		path    string
		wantErr bool
	}{
		// Root-aware: absolute paths under root are accepted
		{"absolute path under root", root, "/data/artifacts/x.png", false},
		{"nested absolute under root", root, "/data/artifacts/sub/dir/file.png", false},
		{"contract example", root, "/data/artifacts/x.png", false},
		// Rejected: absolute path outside root
		{"absolute path outside root", root, "/etc/passwd", true},
		{"absolute path sibling", root, "/data/other/file.png", true},
		{"absolute root traversal", root, "/data/artifacts/../etc/passwd", true},
		// Rejected: traversal via symlink-like path (canonicalized)
		{"traversal past root", root, "/data/artifacts/sub/../../etc/passwd", true},
		// Relative paths still work with root set
		{"relative path", root, "some/file.png", false},
		{"relative traversal", root, "../etc/passwd", true},
		// No root: backward compat (absolute rejected)
		{"no root, absolute rejected", "", "/data/artifacts/x.png", true},
		{"no root, relative accepted", "", "some/file.png", false},
		{"no root, traversal rejected", "", "../etc/passwd", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateArtifactPath(tt.root, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateArtifactPath(root=%q, %q) = %v, wantErr %v", tt.root, tt.path, err, tt.wantErr)
			}
		})
	}
}

// TestValidateArtifactPathInRootViaIngest proves: Ingest() with artifactsPath accepts
// contract-valid absolute paths under the root.
func TestValidateArtifactPathInRootViaIngest(t *testing.T) {
	fq := newFakeQuerier()
	bus := NewEventBus()
	svc := NewIngestService(fq, bus, slog.Default(), "/data/artifacts")

	eventID := uuid.NewString()
	req := validFakeRequest(eventID)
	req.Kind = "artifact.created"
	req.Status = ""
	req.LivenessMode = ""
	req.Pid = nil
	// Contract §3 example: absolute path under the configured root
	req.Artifacts = []ArtifactDescriptor{{Type: "image", Path: "/data/artifacts/x.png", Name: "before.png"}}

	_, status, err := svc.Ingest(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error for in-root absolute path, got: %v", err)
	}
	if status != 201 {
		t.Fatalf("expected 201, got %d", status)
	}
}

// FIX (minor #7): URL SSRF guard tests
func TestValidateArtifactURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string // empty = expect success
	}{
		{"https URL is valid", "https://example.com/file.png", ""},
		{"http URL is valid", "http://example.com/file.png", ""},
		{"localhost is rejected", "http://localhost/file.png", "private/link-local"},
		{"127.0.0.1 is rejected", "http://127.0.0.1/file.png", "private/link-local"},
		{"10.0.0.1 is rejected", "http://10.0.0.1/file.png", "private/link-local"},
		{"169.254.1.1 is rejected", "http://169.254.1.1/metadata", "private/link-local"},
		{"192.168.1.1 is rejected", "http://192.168.1.1/file.png", "private/link-local"},
		{".local suffix rejected", "http://myhost.local/file.png", "private/link-local"},
		{".internal suffix rejected", "http://myhost.internal/file.png", "private/link-local"},
		{"ftp scheme rejected", "ftp://example.com/file.png", "scheme must be http or https"},
		{"invalid URL", "://bad", "invalid URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateArtifactURL(tt.url)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestIsValidUUID(t *testing.T) {
	tests := []struct {
		uuid string
		want bool
	}{
		{"b1e2f3a4-5678-9abc-def0-123456789abc", true},
		{"B1E2F3A4-5678-9ABC-DEF0-123456789ABC", true},
		{"not-a-uuid", false},
		{"", false},
		{"b1e2f3a45678-9abc-def0-123456789abc", false}, // missing dash
	}
	for _, tt := range tests {
		t.Run(tt.uuid, func(t *testing.T) {
			if got := isValidUUID(tt.uuid); got != tt.want {
				t.Errorf("isValidUUID(%q) = %v, want %v", tt.uuid, got, tt.want)
			}
		})
	}
}

func TestValidHarnesses(t *testing.T) {
	for _, h := range []string{"hermes", "claude", "antigravity", "codex", "generic"} {
		if !validHarnesses[h] {
			t.Errorf("expected %q to be a valid harness", h)
		}
	}
}

func TestValidKinds(t *testing.T) {
	for _, k := range []string{"session.start", "session.heartbeat", "session.end", "artifact.created", "server.started", "server.stopped", "note"} {
		if !validKinds[k] {
			t.Errorf("expected %q to be a valid kind", k)
		}
	}
}

func TestArtifactsValidation(t *testing.T) {
	tests := []struct {
		name    string
		arts    []ArtifactDescriptor
		wantErr string
	}{
		{
			name:    "no artifacts is valid",
			arts:    nil,
			wantErr: "",
		},
		{
			name:    "artifact with path is valid",
			arts:    []ArtifactDescriptor{{Type: "image", Path: "some/file.png"}},
			wantErr: "",
		},
		{
			name:    "artifact with url is valid",
			arts:    []ArtifactDescriptor{{Type: "image", URL: "https://example.com/file.png"}},
			wantErr: "",
		},
		{
			name:    "artifact with neither path nor url",
			arts:    []ArtifactDescriptor{{Type: "image"}},
			wantErr: "must have exactly one of path or url",
		},
		{
			name:    "artifact with both path and url",
			arts:    []ArtifactDescriptor{{Type: "image", Path: "file.png", URL: "https://example.com/file.png"}},
			wantErr: "must have exactly one of path or url, not both",
		},
		{
			name:    "artifact with traversal path",
			arts:    []ArtifactDescriptor{{Type: "image", Path: "../etc/passwd"}},
			wantErr: "path must not contain traversal",
		},
		// FIX (minor #7): artifact URL SSRF guard
		{
			name:    "artifact with localhost URL rejected",
			arts:    []ArtifactDescriptor{{Type: "image", URL: "http://localhost/file.png"}},
			wantErr: "private/link-local",
		},
		{
			name:    "artifact with private IP URL rejected",
			arts:    []ArtifactDescriptor{{Type: "image", URL: "http://192.168.1.1/file.png"}},
			wantErr: "private/link-local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			req.Artifacts = tt.arts
			err := ValidateWorkEvent("", req)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestExternalRefPattern(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"SC-123", true},
		{"#42", true},
		{"SC-91130", true},
		{"JIRA-123", false},
		{"invalid", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("ref=%q", tt.ref), func(t *testing.T) {
			got := externalRefPattern.MatchString(tt.ref)
			if got != tt.want {
				t.Errorf("externalRefPattern.MatchString(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestResolveTenantFromKey(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantErr    bool
		wantTenant string
	}{
		{"empty key returns error", "", true, ""},
		{"non-empty key returns personal (placeholder until config.go wiring)", "some-key", false, "personal"},
		{"another key returns personal (placeholder until config.go wiring)", "another-key", false, "personal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenant, err := ResolveTenantFromKey(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if tenant != tt.wantTenant {
					t.Errorf("expected tenant %q, got %q", tt.wantTenant, tenant)
				}
			}
		})
	}
}

// TestValidTenantSet lists the known tenant values from the contract §4.
// This is a documentation/compile-time guard, NOT a runtime validator
// (tenant enforcement is deferred to config.go wiring — see issue #2).
func TestValidTenantSet(t *testing.T) {
	known := []string{"personal", "dayjob"}
	for _, tn := range known {
		t.Run(tn, func(t *testing.T) {
			// This test documents the known values. The actual enforcement
			// gate will live in ResolveTenantFromKey once config.go provides
			// the key→allowed-tenant mapping.
			if tn == "" {
				t.Error("tenant must be non-empty")
			}
		})
	}
}

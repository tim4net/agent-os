package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			tt.mutate(&req)

			err := ValidateWorkEvent(req)

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
			err := validateArtifactPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateArtifactPath(%q) = %v, wantErr %v", tt.path, err, tt.wantErr)
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			req.Artifacts = tt.arts

			err := ValidateWorkEvent(req)

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

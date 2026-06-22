package config

import (
	"encoding/json"
	"log/slog"
	"os"
)

// AgentSpec describes one fleet agent declaratively. The fleet (seed agents +
// discovery candidates) is assembled from a manifest — a JSON list loaded from
// config — rather than a hardcoded Go list (issue #136). Adding an agent is a
// config change (AGENTS_JSON / AGENTS_MANIFEST_PATH), not a Go change.
//
// JSON field names are stable as they are exposed by GET /api/agents/discover.
type AgentSpec struct {
	Hostname    string `json:"hostname"`
	DisplayName string `json:"display_name"`
	Harness     string `json:"harness"`
	BaseURL     string `json:"base_url"`
}

// DefaultAgentManifest is the built-in manifest used when neither
// AGENTS_MANIFEST_PATH nor AGENTS_JSON is set. It mirrors the historic seed
// fleet so existing deploys behave identically; override via config to add or
// remove agents without touching Go code.
const DefaultAgentManifest = `[
  {"hostname":"roux","display_name":"Roux","harness":"hermes","base_url":"http://roux:8080"},
  {"hostname":"crawbot","display_name":"Crawbot","harness":"openclaw","base_url":"http://crawbot:8080"},
  {"hostname":"litellm","display_name":"LiteLLM on xps","harness":"litellm","base_url":"http://xps:4000"},
  {"hostname":"agy","display_name":"Antigravity (agy)","harness":"agy","base_url":"local://agy"}
]`

// DefaultAgentSpecs parses DefaultAgentManifest. It is the single canonical
// fallback for both DB seeding and agent discovery.
func DefaultAgentSpecs() []AgentSpec {
	specs, err := ParseAgentManifest(DefaultAgentManifest)
	if err != nil {
		// DefaultAgentManifest is a compile-time constant; this is unreachable.
		panic("invalid DefaultAgentManifest: " + err.Error())
	}
	return specs
}

// ParseAgentManifest parses a JSON manifest. An empty/whitespace input yields
// an empty (non-nil) slice — allowing operators to disable the fleet entirely
// with AGENTS_JSON='[]'. Malformed JSON returns an error.
func ParseAgentManifest(raw string) ([]AgentSpec, error) {
	var specs []AgentSpec
	if err := json.Unmarshal([]byte(raw), &specs); err != nil {
		return nil, err
	}
	if specs == nil {
		specs = []AgentSpec{}
	}
	return specs, nil
}

// LoadAgentManifest resolves the fleet manifest from the environment.
// Resolution order:
//  1. AGENTS_MANIFEST_PATH — path to a JSON file on disk
//  2. AGENTS_JSON          — inline JSON string
//  3. DefaultAgentManifest (built-in)
//
// A missing/unreadable file or malformed JSON is logged and falls back to the
// default so a bad config never prevents the server from booting.
func LoadAgentManifest() []AgentSpec {
	var raw string
	source := "default"

	if path := os.Getenv("AGENTS_MANIFEST_PATH"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			raw = string(b)
			source = "AGENTS_MANIFEST_PATH=" + path
		} else {
			slog.Error("failed to read agent manifest file; using default", "path", path, "error", err)
		}
	}
	if raw == "" {
		if inline := os.Getenv("AGENTS_JSON"); inline != "" {
			raw = inline
			source = "AGENTS_JSON"
		}
	}
	if raw == "" {
		return DefaultAgentSpecs()
	}

	specs, err := ParseAgentManifest(raw)
	if err != nil {
		slog.Error("failed to parse agent manifest; using default", "source", source, "error", err)
		return DefaultAgentSpecs()
	}
	slog.Info("loaded agent manifest", "source", source, "agents", len(specs))
	return specs
}

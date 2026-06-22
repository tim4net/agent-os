package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAgentManifest_Valid(t *testing.T) {
	specs, err := ParseAgentManifest(`[
		{"hostname":"a","display_name":"Alpha","harness":"hermes","base_url":"http://a:8080"},
		{"hostname":"b","display_name":"Beta","harness":"agy","base_url":"local://b"}
	]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	if specs[0].Hostname != "a" || specs[0].Harness != "hermes" {
		t.Fatalf("unexpected first spec: %+v", specs[0])
	}
}

func TestParseAgentManifest_EmptyArrayYieldsEmptySlice(t *testing.T) {
	// AGENTS_JSON='[]' must disable the fleet (empty, non-nil) — not fall back.
	specs, err := ParseAgentManifest(`[]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if specs == nil {
		t.Fatal("expected non-nil empty slice for []")
	}
	if len(specs) != 0 {
		t.Fatalf("expected 0 specs, got %d", len(specs))
	}
}

func TestParseAgentManifest_Malformed(t *testing.T) {
	if _, err := ParseAgentManifest(`{not json`); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestDefaultAgentSpecs_CanonicalFour(t *testing.T) {
	specs := DefaultAgentSpecs()
	if len(specs) != 4 {
		t.Fatalf("expected 4 default specs, got %d (%+v)", len(specs), specs)
	}
	want := map[string]bool{"roux": false, "crawbot": false, "litellm": false, "agy": false}
	for _, s := range specs {
		if _, ok := want[s.Hostname]; !ok {
			t.Errorf("unexpected default hostname %q", s.Hostname)
		}
		want[s.Hostname] = true
		if s.Harness == "" || s.DisplayName == "" || s.BaseURL == "" {
			t.Errorf("default spec %q has empty field: %+v", s.Hostname, s)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("default manifest missing %q", name)
		}
	}
}

func TestLoadAgentManifest_Default(t *testing.T) {
	t.Setenv("AGENTS_MANIFEST_PATH", "")
	t.Setenv("AGENTS_JSON", "")
	specs := LoadAgentManifest()
	if len(specs) != 4 {
		t.Fatalf("default manifest should have 4 agents, got %d", len(specs))
	}
}

func TestLoadAgentManifest_InlineJSON(t *testing.T) {
	t.Setenv("AGENTS_MANIFEST_PATH", "")
	t.Setenv("AGENTS_JSON", `[{"hostname":"z","display_name":"Zed","harness":"generic","base_url":"http://z"}]`)
	specs := LoadAgentManifest()
	if len(specs) != 1 || specs[0].Hostname != "z" {
		t.Fatalf("expected single custom agent z, got %+v", specs)
	}
}

func TestLoadAgentManifest_EmptyArrayDisablesFleet(t *testing.T) {
	t.Setenv("AGENTS_MANIFEST_PATH", "")
	t.Setenv("AGENTS_JSON", `[]`)
	specs := LoadAgentManifest()
	if len(specs) != 0 {
		t.Fatalf("AGENTS_JSON=[] should yield zero agents, got %d", len(specs))
	}
}

func TestLoadAgentManifest_FilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	content := `[{"hostname":"filey","display_name":"File","harness":"hermes","base_url":"http://filey"}]`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTS_MANIFEST_PATH", path)
	t.Setenv("AGENTS_JSON", `[{"hostname":"ignored","display_name":"","harness":"","base_url":""}]`)
	specs := LoadAgentManifest()
	if len(specs) != 1 || specs[0].Hostname != "filey" {
		t.Fatalf("file path should take precedence over AGENTS_JSON, got %+v", specs)
	}
}

func TestLoadAgentManifest_UnreadableFileFallsBack(t *testing.T) {
	t.Setenv("AGENTS_MANIFEST_PATH", filepath.Join(t.TempDir(), "does-not-exist.json"))
	t.Setenv("AGENTS_JSON", "")
	specs := LoadAgentManifest()
	// Unreadable file → fall back to default (4 agents), not a boot failure.
	if len(specs) != 4 {
		t.Fatalf("unreadable file should fall back to default 4, got %d", len(specs))
	}
}

func TestLoadAgentManifest_MalformedInlineFallsBack(t *testing.T) {
	t.Setenv("AGENTS_MANIFEST_PATH", "")
	t.Setenv("AGENTS_JSON", `{broken`)
	specs := LoadAgentManifest()
	if len(specs) != 4 {
		t.Fatalf("malformed inline JSON should fall back to default 4, got %d", len(specs))
	}
}

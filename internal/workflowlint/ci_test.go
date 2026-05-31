package workflowlint

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflow represents the subset of GitHub Actions YAML we care to validate.
type workflow struct {
	Name string                   `yaml:"name"`
	On   map[string]interface{}   `yaml:"on"`
	Jobs map[string]job           `yaml:"jobs"`
}

type job struct {
	Name    string              `yaml:"name"`
	RunsOn  interface{}         `yaml:"runs-on"` // can be string or []string
	Env     map[string]string   `yaml:"env"`
	Steps   []map[string]interface{} `yaml:"steps"`
}

// TestCIWorkflowStructure validates the ci.yml shape matches all acceptance
// criteria for WP-CI.  This test runs without network or Docker — it only
// parses the YAML and checks structural properties.
func TestCIWorkflowStructure(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("failed to read ci.yml: %v", err)
	}

	var wf workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("failed to parse ci.yml: %v", err)
	}

	// ── AC1: runs-on must be self-hosted, linux (not ubuntu-latest) ───────
	if len(wf.Jobs) == 0 {
		t.Fatal("workflow has no jobs")
	}
	for jobName, job := range wf.Jobs {
		runsOn := flattenStringSlice(job.RunsOn)
		if len(runsOn) == 0 {
			t.Fatalf("job %q: runs-on is empty", jobName)
		}
		hasSelfHosted := false
		hasLinux := false
		for _, label := range runsOn {
			if label == "self-hosted" {
				hasSelfHosted = true
			}
			if label == "linux" {
				hasLinux = true
			}
			if label == "ubuntu-latest" || label == "ubuntu-22.04" || label == "ubuntu-24.04" {
				t.Fatalf("job %q: uses GitHub-hosted runner %q — must be self-hosted", jobName, label)
			}
		}
		if !hasSelfHosted {
			t.Fatalf("job %q: missing 'self-hosted' label in runs-on", jobName)
		}
		if !hasLinux {
			t.Fatalf("job %q: missing 'linux' label in runs-on", jobName)
		}
		t.Logf("job %q: runs-on = %v ✓", jobName, runsOn)
	}

	// ── AC2: triggers on pull_request AND push to main ────────────────────
	onPullRequest := false
	onPushMain := false
	for key := range wf.On {
		if key == "pull_request" {
			onPullRequest = true
		}
		if key == "push" {
			if pushVal, ok := wf.On["push"]; ok {
				if m, ok := pushVal.(map[string]interface{}); ok {
					if branches, ok := m["branches"]; ok {
						if arr, ok := branches.([]interface{}); ok {
							for _, b := range arr {
								if b.(string) == "main" {
									onPushMain = true
								}
							}
						}
					}
				}
			}
		}
	}
	if !onPullRequest {
		t.Error("missing trigger: pull_request")
	}
	if !onPushMain {
		t.Error("missing trigger: push to main")
	}

	// ── AC3: required steps present ──────────────────────────────────────
	for _, job := range wf.Jobs {
		stepChecks := map[string]bool{
			"go build ./...":  false,
			"go vet ./...":    false,
			"sqlc generate":  false,
		}
		for _, step := range job.Steps {
			run, _ := step["run"].(string)
			if run != "" {
				for key := range stepChecks {
					// Match if the run block contains the key substring.
					if containsSubstring(run, key) {
						stepChecks[key] = true
					}
				}
			}
		}
		for key, found := range stepChecks {
			if !found {
				t.Errorf("missing step containing %q", key)
			}
		}
	}

	// ── AC4: Postgres step present + AOS_TEST_DSN + migration + skip detection ─
	for _, job := range wf.Jobs {
		hasPodman := false
		hasMigrate := false
		hasAOSTestDSN := false
		hasSkipDetect := false
		for _, step := range job.Steps {
			run, _ := step["run"].(string)
			if run != "" {
				if containsSubstring(run, "podman run") {
					hasPodman = true
				}
				if containsSubstring(run, "migrations") || containsSubstring(run, "*.up.sql") {
					hasMigrate = true
				}
				if containsSubstring(run, "AOS_TEST_DSN") {
					hasAOSTestDSN = true
				}
				if containsSubstring(run, "AOS_TEST_DSN not set") {
					hasSkipDetect = true
				}
			}
		}
		if !hasPodman {
			t.Error("missing podman step to start Postgres")
		}
		if !hasMigrate {
			t.Error("missing migration step (internal/migrations/*.up.sql)")
		}
		if !hasAOSTestDSN {
			t.Error("missing AOS_TEST_DSN export in test step")
		}
		if !hasSkipDetect {
			t.Error("missing skip-detection gate (AOS_TEST_DSN not set check)")
		}
	}

	// ── AC5: no continue-on-error (default false = job fails on failure) ──
	for jobName, job := range wf.Jobs {
		for i, step := range job.Steps {
			if v, ok := step["continue-on-error"]; ok {
				if b, ok := v.(bool); ok && b {
					t.Errorf("job %q step %d: has continue-on-error: true — would mask failures", jobName, i)
				}
			}
		}
	}

	// ── AC6: sqlc generate must appear before go build (no pre-committed .sql.go needed) ──
	for _, job := range wf.Jobs {
		sqlcIdx := -1
		buildIdx := -1
		for i, step := range job.Steps {
			run, _ := step["run"].(string)
			if run != "" {
				if containsSubstring(run, "sqlc generate") {
					sqlcIdx = i
				}
				if containsSubstring(run, "go build ./...") {
					buildIdx = i
				}
			}
		}
		if sqlcIdx == -1 {
			t.Error("sqlc generate step not found")
		}
		if buildIdx == -1 {
			t.Error("go build step not found")
		}
		if sqlcIdx != -1 && buildIdx != -1 && sqlcIdx > buildIdx {
			t.Error("sqlc generate must run BEFORE go build (otherwise .sql.go would need committing)")
		}
	}

	t.Log("all structural checks passed")
}

// flattenStringSlice handles runs-on being either a string or []string.
func flattenStringSlice(v interface{}) []string {
	switch val := v.(type) {
	case string:
		return []string{val}
	case []interface{}:
		var out []string
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return val
	}
	return nil
}

func containsSubstring(s, sub string) bool {
	// Simple case-insensitive check.
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsFold(s, sub))
}

func containsFold(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if foldEq(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func foldEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if toLower(a[i]) != toLower(b[i]) {
			return false
		}
	}
	return true
}

func toLower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

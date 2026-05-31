package workflowlint

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflow represents the subset of GitHub Actions YAML we care to validate.
type workflow struct {
	Name string                 `yaml:"name"`
	On   map[string]interface{} `yaml:"on"`
	Jobs map[string]job         `yaml:"jobs"`
}

type job struct {
	Name   string                   `yaml:"name"`
	RunsOn interface{}              `yaml:"runs-on"` // can be string or []string
	Env    map[string]string        `yaml:"env"`
	Steps  []map[string]interface{} `yaml:"steps"`
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
			"go build ./...": false,
			"go vet ./...":   false,
			"sqlc generate": false,
		}
		for _, step := range job.Steps {
			run, _ := step["run"].(string)
			if run != "" {
				for key := range stepChecks {
					if strings.Contains(strings.ToLower(run), strings.ToLower(key)) {
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

	// ── AC3b: sqlc version pinned to v1.31.1 ──────────────────────────────
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			run, _ := step["run"].(string)
			if run != "" && strings.Contains(strings.ToLower(run), "sqlc version") {
				if !strings.Contains(run, "v1.31.1") {
					t.Error("sqlc version step must pin to v1.31.1 (exact match)")
				}
			}
		}
	}

	// ── AC4: Postgres step + BOTH DSN env vars + migration + GENERIC skip detection ─
	//
	// The real contract: the workflow must export BOTH AOS_TEST_DSN and
	// AOS_TEST_DATABASE_URL because the repo's integration suites read two
	// different env-var names.  The skip-gate must be generic (catch any
	// "not set — skipping" pattern), not a single literal.
	for _, job := range wf.Jobs {
		hasPodman := false
		hasMigrate := false
		hasAOSTestDSN := false
		hasAOSTestDBURL := false
		hasGenericSkipDetect := false

		for _, step := range job.Steps {
			run, _ := step["run"].(string)
			if run == "" {
				continue
			}
			runLower := strings.ToLower(run)
			if strings.Contains(runLower, "podman run") {
				hasPodman = true
			}
			if strings.Contains(runLower, "migrations") || strings.Contains(run, "*.up.sql") {
				hasMigrate = true
			}
			if strings.Contains(run, "export AOS_TEST_DSN=") {
				hasAOSTestDSN = true
				// BLOCKING #2 fix: DSN must interpolate ${POSTGRES_PASSWORD},
				// never a literal or masked value like ***.
				if !strings.Contains(run, "${POSTGRES_PASSWORD}") {
					t.Error("AOS_TEST_DSN must interpolate ${POSTGRES_PASSWORD}, not a literal/masked value")
				}
				if strings.Contains(run, ":***@") {
					t.Error("AOS_TEST_DSN contains a masked '***' password (copy-pasted from a log / masker-corrupted) — integration tests cannot authenticate")
				}
			}
			if strings.Contains(run, "export AOS_TEST_DATABASE_URL=") {
				hasAOSTestDBURL = true
			}
			// The skip-gate must be generic — matches any "not set … skipping"
			// pattern, not the single literal "AOS_TEST_DSN not set".
			if strings.Contains(runLower, "not set") && strings.Contains(runLower, "skipping") {
				hasGenericSkipDetect = true
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
		if !hasAOSTestDBURL {
			t.Error("missing AOS_TEST_DATABASE_URL export in test step — " +
				"integration suites read this var, not just AOS_TEST_DSN")
		}
		if !hasGenericSkipDetect {
			t.Error("missing generic skip-detection gate (must catch any " +
				"\"not set — skipping\" pattern, not a single literal)")
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
				if strings.Contains(strings.ToLower(run), "sqlc generate") {
					sqlcIdx = i
				}
				if strings.Contains(strings.ToLower(run), "go build ./...") {
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

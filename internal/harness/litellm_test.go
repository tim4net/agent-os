package harness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiteLLMVersionInfoParsesVersion(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"x.y.z"}`))
	}))
	defer srv.Close()

	l := &LiteLLMHarness{baseURL: srv.URL, httpClient: srv.Client()}
	info, err := l.VersionInfo(context.Background())
	if err != nil {
		t.Fatalf("VersionInfo error = %v", err)
	}
	if gotPath != "/version" {
		t.Fatalf("path = %q, want /version", gotPath)
	}
	if info.Current != "x.y.z" || info.Source != "http" {
		t.Fatalf("VersionInfo = %+v, want current x.y.z source http", info)
	}
	if info.CheckedAt.IsZero() {
		t.Fatal("CheckedAt should be set")
	}
}

// litellmVersionMux builds a test server that responds per-path so the version
// fallback chain (/version -> /health/readiness -> /openapi.json) can be
// exercised one source at a time. A non-empty body for a path makes that source
// succeed; an empty body (or absence) makes the probe fall through.
func litellmVersionMux(t *testing.T, versionBody, readinessBody, openapiBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	register := func(path, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if body == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		})
	}
	register("/version", versionBody)
	register("/health/readiness", readinessBody)
	register("/openapi.json", openapiBody)
	return httptest.NewServer(mux)
}

// When /version 404s but /health/readiness reports litellm_version, the probe
// must fall through to the readiness source.
func TestLiteLLMVersionInfoFallsBackToReadiness(t *testing.T) {
	srv := litellmVersionMux(t, "", `{"status":"healthy","litellm_version":"1.85.1"}`, "")
	defer srv.Close()

	l := &LiteLLMHarness{baseURL: srv.URL, httpClient: srv.Client()}
	info, err := l.VersionInfo(context.Background())
	if err != nil {
		t.Fatalf("VersionInfo error = %v", err)
	}
	if info.Current != "1.85.1" || info.Source != "health" {
		t.Fatalf("VersionInfo = %+v, want current 1.85.1 source health", info)
	}
}

// When neither /version nor /health/readiness yield a version, the probe must
// fall through to /openapi.json info.version. Mirrors the live xps:4000 build,
// where /version 404s, readiness omits the version, and only openapi has it.
func TestLiteLLMVersionInfoFallsBackToOpenAPI(t *testing.T) {
	srv := litellmVersionMux(t,
		"",
		`{"status":"healthy","db":"Not connected"}`,
		`{"openapi":"3.1.0","info":{"title":"LiteLLM API","version":"1.85.1"}}`,
	)
	defer srv.Close()

	l := &LiteLLMHarness{baseURL: srv.URL, httpClient: srv.Client()}
	info, err := l.VersionInfo(context.Background())
	if err != nil {
		t.Fatalf("VersionInfo error = %v", err)
	}
	if info.Current != "1.85.1" || info.Source != "openapi" {
		t.Fatalf("VersionInfo = %+v, want current 1.85.1 source openapi", info)
	}
}

// /version wins even when later sources also have a version — provenance must be
// the highest-priority source that succeeds.
func TestLiteLLMVersionInfoPrefersVersionEndpoint(t *testing.T) {
	srv := litellmVersionMux(t,
		`{"version":"9.9.9"}`,
		`{"litellm_version":"1.85.1"}`,
		`{"info":{"version":"0.0.0"}}`,
	)
	defer srv.Close()

	l := &LiteLLMHarness{baseURL: srv.URL, httpClient: srv.Client()}
	info, err := l.VersionInfo(context.Background())
	if err != nil {
		t.Fatalf("VersionInfo error = %v", err)
	}
	if info.Current != "9.9.9" || info.Source != "http" {
		t.Fatalf("VersionInfo = %+v, want current 9.9.9 source http", info)
	}
}

// All three sources 404 -> unknown.
func TestLiteLLMVersionInfo404ReturnsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	l := &LiteLLMHarness{baseURL: srv.URL, httpClient: srv.Client()}
	info, err := l.VersionInfo(context.Background())
	if err != nil {
		t.Fatalf("VersionInfo error = %v", err)
	}
	if info.Current != "" || info.Source != "unknown" {
		t.Fatalf("VersionInfo = %+v, want unknown", info)
	}
}

// Every source returns 200 but none carries a usable version field -> unknown.
// Guards against treating an empty/absent field as a version.
func TestLiteLLMVersionInfoEmptyFieldsReturnUnknown(t *testing.T) {
	srv := litellmVersionMux(t,
		`{"version":""}`,
		`{"status":"healthy","db":"Not connected"}`,
		`{"openapi":"3.1.0","info":{"title":"LiteLLM API"}}`,
	)
	defer srv.Close()

	l := &LiteLLMHarness{baseURL: srv.URL, httpClient: srv.Client()}
	info, err := l.VersionInfo(context.Background())
	if err != nil {
		t.Fatalf("VersionInfo error = %v", err)
	}
	if info.Current != "" || info.Source != "unknown" {
		t.Fatalf("VersionInfo = %+v, want unknown", info)
	}
}

func TestLiteLLMVersionInfoConnectionRefusedReturnsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	l := &LiteLLMHarness{baseURL: url, httpClient: srv.Client()}
	info, err := l.VersionInfo(context.Background())
	if err != nil {
		t.Fatalf("VersionInfo error = %v", err)
	}
	if info.Current != "" || info.Source != "unknown" {
		t.Fatalf("VersionInfo = %+v, want unknown", info)
	}
}

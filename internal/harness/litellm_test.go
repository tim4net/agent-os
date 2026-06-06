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

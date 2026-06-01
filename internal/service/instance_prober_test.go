package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInstanceProber_HealthyServer_ReturnsUp(t *testing.T) {
	// A real running server shows 'up' with a fresh last_probed_at.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	prober := NewInstanceProber(ProberConfig{
		ProbeTimeout: 2 * time.Second,
	})
	result := prober.Probe(context.Background(), srv.URL)

	if result.Status != InstanceStatusUp {
		t.Fatalf("expected status 'up', got %q", result.Status)
	}
	if result.StatusCode != 200 {
		t.Fatalf("expected status code 200, got %d", result.StatusCode)
	}
	if result.Error != nil {
		t.Fatalf("expected no error, got %v", result.Error)
	}
	if result.ProbedAt.IsZero() {
		t.Fatal("expected non-zero ProbedAt")
	}
	if result.ResponseTime <= 0 {
		t.Fatal("expected positive ResponseTime")
	}
}

func TestInstanceProber_StoppedServer_ReturnsDown(t *testing.T) {
	// A stopped server flips to 'down' on next probe (proves it's a PROBE, not a DB flag — the anti-fake-status rule).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Close() // stop the server

	prober := NewInstanceProber(ProberConfig{
		ProbeTimeout: 1 * time.Second,
	})
	result := prober.Probe(context.Background(), srv.URL)

	if result.Status != InstanceStatusDown {
		t.Fatalf("expected status 'down', got %q", result.Status)
	}
	if result.Error == nil {
		t.Fatal("expected connection error for stopped server")
	}
}

func TestInstanceProber_Non2xx_ReturnsDown(t *testing.T) {
	// A server returning 500 should be 'down'.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	prober := NewInstanceProber(ProberConfig{})
	result := prober.Probe(context.Background(), srv.URL)

	if result.Status != InstanceStatusDown {
		t.Fatalf("expected status 'down' for 500 response, got %q", result.Status)
	}
	if result.StatusCode != 500 {
		t.Fatalf("expected status code 500, got %d", result.StatusCode)
	}
}

func TestInstanceProber_InvalidURL_ReturnsDown(t *testing.T) {
	prober := NewInstanceProber(ProberConfig{})
	result := prober.Probe(context.Background(), "not-a-valid-url://blah")

	if result.Status != InstanceStatusDown {
		t.Fatalf("expected status 'down' for invalid URL, got %q", result.Status)
	}
	if result.Error == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestInstanceProber_CancelledContext(t *testing.T) {
	// Slow server with cancelled context should return down.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // longer than context timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	prober := NewInstanceProber(ProberConfig{})
	result := prober.Probe(ctx, srv.URL)

	if result.Status != InstanceStatusDown {
		t.Fatalf("expected status 'down' for timed-out probe, got %q", result.Status)
	}
}

func TestInstanceProber_NextProbeInterval_WithJitter(t *testing.T) {
	prober := NewInstanceProber(ProberConfig{
		DefaultInterval: 30 * time.Second,
		JitterRange:     5 * time.Second,
	})

	// Run multiple times and verify all intervals are within [25s, 35s]
	for i := 0; i < 100; i++ {
		interval := prober.NextProbeInterval()
		if interval < 25*time.Second || interval > 35*time.Second {
			t.Fatalf("interval %d: %v out of expected range [25s, 35s]", i, interval)
		}
	}
}

func TestInstanceProber_NextProbeInterval_MinimumClamp(t *testing.T) {
	// Jitter can push below 0; should be clamped to 1 second minimum.
	prober := NewInstanceProber(ProberConfig{
		DefaultInterval: 500 * time.Millisecond,
		JitterRange:     2 * time.Second, // large jitter relative to interval
	})

	for i := 0; i < 100; i++ {
		interval := prober.NextProbeInterval()
		if interval < time.Second {
			t.Fatalf("interval %d: %v below minimum 1s", i, interval)
		}
	}
}

func TestValidInstanceStatus(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"up", true},
		{"down", true},
		{"unknown", true},
		{"UP", false},
		{"running", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ValidInstanceStatus(tt.input)
			if got != tt.want {
				t.Fatalf("ValidInstanceStatus(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestInstanceStatus_String(t *testing.T) {
	if InstanceStatusUp.String() != "up" {
		t.Fatalf("expected 'up', got %q", InstanceStatusUp.String())
	}
	if InstanceStatusDown.String() != "down" {
		t.Fatalf("expected 'down', got %q", InstanceStatusDown.String())
	}
}

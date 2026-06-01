package service

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// InstanceStatus represents the health status of an app instance.
type InstanceStatus string

const (
	InstanceStatusUp     InstanceStatus = "up"
	InstanceStatusDown   InstanceStatus = "down"
	InstanceStatusUnkown InstanceStatus = "unknown"
)

// InstanceProber performs real HTTP health probes against app instances.
// It respects the anti-fake-status rule (contract §4): status is derived from
// actual probe results, never from DB flags. A never-reached URL stays "unknown",
// never "up" — "running requires positive proof" (ADR-001 F10 / ADR-003 D3).
type InstanceProber struct {
	client       *http.Client
	log          *slog.Logger
	mu           sync.Mutex
	defaultInterval time.Duration
	jitterRange    time.Duration
}

// ProberConfig holds configuration for the instance prober.
type ProberConfig struct {
	// ProbeTimeout is the per-probe HTTP timeout (default: 5s).
	ProbeTimeout time.Duration
	// DefaultInterval is the base probe interval (default: 30s).
	DefaultInterval time.Duration
	// JitterRange is the random jitter applied to the interval (default: ±5s).
	JitterRange time.Duration
}

// DefaultProberConfig returns sensible defaults for the prober.
func DefaultProberConfig() ProberConfig {
	return ProberConfig{
		ProbeTimeout:    5 * time.Second,
		DefaultInterval: 30 * time.Second,
		JitterRange:     5 * time.Second,
	}
}

// NewInstanceProber creates a new InstanceProber with the given config.
func NewInstanceProber(cfg ProberConfig) *InstanceProber {
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = DefaultProberConfig().ProbeTimeout
	}
	if cfg.DefaultInterval == 0 {
		cfg.DefaultInterval = DefaultProberConfig().DefaultInterval
	}
	if cfg.JitterRange == 0 {
		cfg.JitterRange = DefaultProberConfig().JitterRange
	}
	return &InstanceProber{
		client: &http.Client{
			Timeout: cfg.ProbeTimeout,
		},
		log:            slog.Default(),
		defaultInterval: cfg.DefaultInterval,
		jitterRange:     cfg.JitterRange,
	}
}

// ProbeResult contains the outcome of a single health probe.
type ProbeResult struct {
	Status       InstanceStatus
	ProbedAt     time.Time
	StatusCode   int // HTTP status code from probe, 0 if connection failed
	ResponseTime time.Duration
	Error        error
}

// Probe performs a real HTTP GET against the given healthURL.
// Returns a ProbeResult with the determined status.
// - HTTP 2xx → "up"
// - Connection refused / timeout / DNS failure → "down"
// - Non-2xx response → "down"
// The returned ProbedAt is server clock (not client clock).
//
// Rules:
// - Never returns "up" without positive proof (contract §4).
// - A never-reached URL returns "down" (connection failure), NOT "unknown".
//   "unknown" is the initial DB state before any probe; after a probe, it's "up" or "down".
func (p *InstanceProber) Probe(ctx context.Context, healthURL string) ProbeResult {
	now := time.Now().UTC()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		p.log.Warn("instance probe: invalid URL", "url", healthURL, "error", err)
		return ProbeResult{
			Status:   InstanceStatusDown,
			ProbedAt: now,
			Error:    fmt.Errorf("invalid URL: %w", err),
		}
	}

	start := time.Now()
	resp, err := p.client.Do(req)
	elapsed := time.Since(start)

	if err != nil {
		// Connection failure → down. This is the real probe result.
		p.log.Debug("instance probe: connection failed", "url", healthURL, "error", err)
		return ProbeResult{
			Status:       InstanceStatusDown,
			ProbedAt:     now,
			ResponseTime: elapsed,
			Error:        err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		p.log.Debug("instance probe: healthy", "url", healthURL, "status_code", resp.StatusCode, "elapsed", elapsed)
		return ProbeResult{
			Status:       InstanceStatusUp,
			ProbedAt:     now,
			StatusCode:   resp.StatusCode,
			ResponseTime: elapsed,
		}
	}

	// Non-2xx → down
	p.log.Debug("instance probe: unhealthy response", "url", healthURL, "status_code", resp.StatusCode)
	return ProbeResult{
		Status:       InstanceStatusDown,
		ProbedAt:     now,
		StatusCode:   resp.StatusCode,
		ResponseTime: elapsed,
		Error:        fmt.Errorf("non-2xx status: %d", resp.StatusCode),
	}
}

// NextProbeInterval returns the duration until the next probe, with jitter applied.
// Jitter is uniformly distributed in [interval - jitter, interval + jitter],
// clamped to a minimum of 1 second.
func (p *InstanceProber) NextProbeInterval() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	jitter := time.Duration(0)
	if p.jitterRange > 0 {
		// Uniform random in [-jitterRange, +jitterRange]
		jitter = time.Duration(rand.Int63n(int64(2*p.jitterRange))) - p.jitterRange
	}

	interval := p.defaultInterval + jitter
	if interval < time.Second {
		interval = time.Second
	}
	return interval
}

// String returns a human-readable representation of the InstanceStatus.
func (s InstanceStatus) String() string {
	return string(s)
}

// ValidInstanceStatus returns true if the status string is a known instance status.
func ValidInstanceStatus(s string) bool {
	switch InstanceStatus(s) {
	case InstanceStatusUp, InstanceStatusDown, InstanceStatusUnkown:
		return true
	}
	return false
}

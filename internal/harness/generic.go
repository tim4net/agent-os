package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// GenericHarness is a simple HTTP health-check harness that only supports
// a basic GET /health endpoint.
type GenericHarness struct {
	baseURL    string
	httpClient *http.Client
}

func NewGenericHarness() Harness {
	return &GenericHarness{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (g *GenericHarness) Name() string { return "generic" }

func (g *GenericHarness) Init(config map[string]any) error {
	baseURL, ok := config["base_url"].(string)
	if !ok || baseURL == "" {
		return fmt.Errorf("generic harness: base_url is required")
	}
	g.baseURL = baseURL
	return nil
}

func (g *GenericHarness) Health(ctx context.Context) (*HealthStatus, error) {
	url := g.baseURL + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("generic health: create request: %w", err)
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return &HealthStatus{Status: "offline"}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &HealthStatus{Status: "degraded"}, nil
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &HealthStatus{Status: "online"}, nil
	}

	status := &HealthStatus{Status: "online"}
	if s, ok := result["status"].(string); ok {
		status.Status = s
	}
	if v, ok := result["version"].(string); ok {
		status.Version = v
	}
	return status, nil
}

func (g *GenericHarness) Chat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (<-chan ChatChunk, error) {
	return nil, ErrNotSupported
}

func (g *GenericHarness) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return nil, ErrNotSupported
}

func (g *GenericHarness) Commands() []Command { return nil }

func (g *GenericHarness) Close() error {
	g.httpClient.CloseIdleConnections()
	return nil
}

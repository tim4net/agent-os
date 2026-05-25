package harness

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// OpenClawHarness implements the Harness interface for OpenClaw/Crawbot agents.
type OpenClawHarness struct {
	baseURL    string
	httpClient *http.Client
}

func NewOpenClawHarness() Harness {
	return &OpenClawHarness{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (o *OpenClawHarness) Name() string { return "openclaw" }

func (o *OpenClawHarness) Init(config map[string]any) error {
	baseURL, ok := config["base_url"].(string)
	if !ok || baseURL == "" {
		return fmt.Errorf("openclaw harness: base_url is required")
	}
	o.baseURL = baseURL
	return nil
}

func (o *OpenClawHarness) Health(ctx context.Context) (*HealthStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/", nil)
	if err != nil {
		return nil, fmt.Errorf("openclaw health: create request: %w", err)
	}

	resp, err := o.httpClient.Do(req)
	if err != nil {
		// Connection refused means offline
		if _, ok := err.(net.Error); ok {
			return &HealthStatus{Status: "offline"}, nil
		}
		return &HealthStatus{Status: "offline"}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return &HealthStatus{Status: "online"}, nil
	}

	return &HealthStatus{Status: "degraded"}, nil
}

func (o *OpenClawHarness) Chat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (<-chan ChatChunk, error) {
	return nil, ErrNotSupported
}

func (o *OpenClawHarness) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return nil, ErrNotSupported
}

func (o *OpenClawHarness) Close() error {
	o.httpClient.CloseIdleConnections()
	return nil
}

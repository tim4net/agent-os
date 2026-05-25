package harness

import (
	"context"
	"errors"
)

// ErrNotSupported is returned when a harness does not implement an operation.
var ErrNotSupported = errors.New("operation not supported by this harness")

// HealthStatus represents the health check result from an agent.
type HealthStatus struct {
	Status  string   `json:"status"`
	Version string   `json:"version,omitempty"`
	Uptime  string   `json:"uptime,omitempty"`
	Models  []string `json:"models,omitempty"`
}

// ChatMessage represents a single message in a chat conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatOptions configures a chat request.
type ChatOptions struct {
	Model       string `json:"model"`
	SystemPrompt string `json:"system_prompt,omitempty"`
}

// ChatChunk represents a streaming chunk from a chat response.
type ChatChunk struct {
	Content string `json:"content"`
	Done    bool   `json:"done"`
	Error   error  `json:"-"`
}

// ModelInfo describes an available model.
type ModelInfo struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// Harness is the interface that all agent harnesses must implement.
type Harness interface {
	Name() string
	Health(ctx context.Context) (*HealthStatus, error)
	Chat(ctx context.Context, messages []ChatMessage, opts ChatOptions) (<-chan ChatChunk, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
	Init(config map[string]any) error
	Close() error
}

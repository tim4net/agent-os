package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
)

// AgentWatcher periodically checks agent health and publishes status changes.
type AgentWatcher struct {
	queries       *db.Queries
	registry      *harness.Registry
	bus           *EventBus
	litellmURL    string
	hermesAPIKey  string
	interval      time.Duration
	done          chan struct{}
}

// NewAgentWatcher creates a new AgentWatcher.
func NewAgentWatcher(queries *db.Queries, registry *harness.Registry, bus *EventBus, litellmURL string, hermesAPIKey string) *AgentWatcher {
	return &AgentWatcher{
		queries:      queries,
		registry:     registry,
		bus:          bus,
		litellmURL:   litellmURL,
		hermesAPIKey: hermesAPIKey,
		interval:     30 * time.Second,
		done:         make(chan struct{}),
	}
}

// Start begins the background health-check loop.
func (aw *AgentWatcher) Start(ctx context.Context) {
	go aw.run(ctx)
	slog.Info("agent watcher started")
}

// Stop signals the watcher to stop.
func (aw *AgentWatcher) Stop() {
	close(aw.done)
}

func (aw *AgentWatcher) run(ctx context.Context) {
	// Run once immediately
	aw.checkAll(ctx)

	ticker := time.NewTicker(aw.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("agent watcher stopped (context cancelled)")
			return
		case <-aw.done:
			slog.Info("agent watcher stopped")
			return
		case <-ticker.C:
			aw.checkAll(ctx)
		}
	}
}

func (aw *AgentWatcher) checkAll(ctx context.Context) {
	agents, err := aw.queries.ListAgents(ctx)
	if err != nil {
		slog.Error("agent watcher: list agents", "error", err)
		return
	}

	for _, agent := range agents {
		aw.checkAgent(ctx, agent)
	}
}

func (aw *AgentWatcher) checkAgent(ctx context.Context, agent db.Agent) {
	h, err := aw.registry.Get(agent.Harness)
	if err != nil {
		slog.Error("agent watcher: unknown harness", "harness", agent.Harness, "agent", agent.Name)
		return
	}

	// Initialize the harness with the agent's config
	config := map[string]any{
		"base_url": agent.BaseUrl,
	}
	if agent.Harness == "hermes" {
		if aw.litellmURL != "" {
			config["litellm_url"] = aw.litellmURL
		}
		if aw.hermesAPIKey != "" {
			config["api_key"] = aw.hermesAPIKey
		}
	}
	if err := h.Init(config); err != nil {
		slog.Error("agent watcher: init harness", "agent", agent.Name, "error", err)
		return
	}
	defer h.Close()

	// Perform health check with timeout
	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	health, err := h.Health(healthCtx)
	if err != nil {
		slog.Error("agent watcher: health check failed", "agent", agent.Name, "error", err)
		health = &harness.HealthStatus{Status: "offline"}
	}

	// Determine new status
	newStatus := health.Status
	if newStatus == "" {
		newStatus = "unknown"
	}

	// Update if status changed
	oldStatus := agent.Status
	if oldStatus != newStatus {
		_, err := aw.queries.UpdateAgentStatus(ctx, db.UpdateAgentStatusParams{
			ID:     agent.ID,
			Status: newStatus,
		})
		if err != nil {
			slog.Error("agent watcher: update status", "agent", agent.Name, "error", err)
			return
		}

		slog.Info("agent watcher: status changed", "agent", agent.Name, "old", oldStatus, "new", newStatus)

		// Publish event
		aw.bus.PublishTyped(EventAgentStatusChanged, map[string]any{
			"agent_id":   agent.ID.String(),
			"agent_name": agent.Name,
			"old_status": oldStatus,
			"new_status": newStatus,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		})
	} else {
		// Just update last_seen
		var pgStatus pgtype.Text
		pgStatus.String = newStatus
		pgStatus.Valid = true

		aw.queries.UpdateAgentStatus(ctx, db.UpdateAgentStatusParams{
			ID:     agent.ID,
			Status: newStatus,
		})
	}
}

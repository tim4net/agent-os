package service

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
)

// AgentWatcher periodically checks agent health and publishes status changes.
type AgentWatcher struct {
	queries    *db.Queries
	registry   *harness.Registry
	bus        *EventBus
	litellmURL string
	interval   time.Duration
	done       chan struct{}
}

// NewAgentWatcher creates a new AgentWatcher.
func NewAgentWatcher(queries *db.Queries, registry *harness.Registry, bus *EventBus, litellmURL string) *AgentWatcher {
	return &AgentWatcher{
		queries:    queries,
		registry:   registry,
		bus:        bus,
		litellmURL: litellmURL,
		interval:   30 * time.Second,
		done:       make(chan struct{}),
	}
}

// Start begins the background health-check loop.
func (aw *AgentWatcher) Start(ctx context.Context) {
	go aw.run(ctx)
	log.Println("agent watcher started")
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
			log.Println("agent watcher stopped (context cancelled)")
			return
		case <-aw.done:
			log.Println("agent watcher stopped")
			return
		case <-ticker.C:
			aw.checkAll(ctx)
		}
	}
}

func (aw *AgentWatcher) checkAll(ctx context.Context) {
	agents, err := aw.queries.ListAgents(ctx)
	if err != nil {
		log.Printf("agent watcher: list agents: %v", err)
		return
	}

	for _, agent := range agents {
		aw.checkAgent(ctx, agent)
	}
}

func (aw *AgentWatcher) checkAgent(ctx context.Context, agent db.Agent) {
	h, err := aw.registry.Get(agent.Harness)
	if err != nil {
		log.Printf("agent watcher: unknown harness %q for agent %q", agent.Harness, agent.Name)
		return
	}

	// Initialize the harness with the agent's config
	config := map[string]any{
		"base_url": agent.BaseUrl,
	}
	if agent.Harness == "hermes" && aw.litellmURL != "" {
		config["litellm_url"] = aw.litellmURL
	}
	if err := h.Init(config); err != nil {
		log.Printf("agent watcher: init harness for %q: %v", agent.Name, err)
		return
	}
	defer h.Close()

	// Perform health check with timeout
	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	health, err := h.Health(healthCtx)
	if err != nil {
		log.Printf("agent watcher: health check for %q failed: %v", agent.Name, err)
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
			log.Printf("agent watcher: update status for %q: %v", agent.Name, err)
			return
		}

		log.Printf("agent watcher: %q status changed: %s -> %s", agent.Name, oldStatus, newStatus)

		// Publish event
		aw.bus.PublishTyped(EventAgentStatusChanged, map[string]any{
			"agent_id":    agent.ID.String(),
			"agent_name":  agent.Name,
			"old_status":  oldStatus,
			"new_status":  newStatus,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
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

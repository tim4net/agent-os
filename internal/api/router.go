package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
	"github.com/tim4net/agent-os/internal/service"
)

// API holds dependencies for the API handlers.
type API struct {
	queries      *db.Queries
	registry     *harness.Registry
	bus          *service.EventBus
	feed         *service.ActivityFeed
	litellmURL   string
	obsidianPath string
	artifacts    *ArtifactAPI
	memory       *MemoryAPI
	studio       *StudioAPI
}

// NewAPI creates a new API instance with the given dependencies.
func NewAPI(queries *db.Queries, registry *harness.Registry, bus *service.EventBus, feed *service.ActivityFeed, litellmURL string, artifactsPath string, obsidianPath string, xaiAPIKey string) *API {
	var provider StudioProvider
	if xaiAPIKey != "" {
		provider = NewXAIProvider(xaiAPIKey)
	} else {
		provider = NewXAIProvider("") // will return errors on generate
	}

	return &API{
		queries:      queries,
		registry:     registry,
		bus:          bus,
		feed:         feed,
		litellmURL:   litellmURL,
		obsidianPath: obsidianPath,
		artifacts:    NewArtifactAPI(queries, artifactsPath),
		memory:       NewMemoryAPI(queries, obsidianPath, litellmURL),
		studio:       NewStudioAPI(queries, artifactsPath, provider),
	}
}

// buildHarnessConfig creates a harness config map for the given agent.
func (a *API) buildHarnessConfig(agent db.Agent) map[string]any {
	config := map[string]any{
		"base_url": agent.BaseUrl,
	}
	// Pass litellm_url for hermes harness so it can list models
	if agent.Harness == "hermes" && a.litellmURL != "" {
		config["litellm_url"] = a.litellmURL
	}
	return config
}

// Router returns a Chi router with all API routes mounted under /api.
func (a *API) Router() http.Handler {
	r := chi.NewRouter()

	// Health endpoint
	r.Get("/health", a.DetailedHealth)

	// Activity feed
	r.Get("/activity", a.GetActivity)

	// Agent routes
	r.Route("/agents", func(r chi.Router) {
		r.Get("/", a.ListAgents)
		r.Post("/", a.CreateAgent)
		r.Get("/discover", a.DiscoverAgents)
		r.Post("/auto-register", a.AutoRegisterAgents)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", a.GetAgent)
			r.Get("/models", a.GetAgentModels)
			r.Post("/chat", a.ChatWithAgent)
		})
	})

	// Conversation routes
	r.Route("/conversations", func(r chi.Router) {
		r.Get("/", a.ListConversations)
		r.Post("/", a.CreateConversation)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/messages", a.GetConversationMessages)
			r.Post("/export", a.ExportConversation)
		})
	})

	// Artifact routes
	r.Mount("/artifacts", a.artifacts.ArtifactRoutes())

	// Artifact notes (nested route — we mount separately to avoid conflict with artifacts.Mount)
	r.Get("/artifacts/{id}/notes", a.GetArtifactNotes)

	// Memory routes
	r.Mount("/memory", a.memory.MemoryRoutes())

	// Studio routes
	r.Mount("/studio", a.studio.StudioRoutes())

	// Task (Kanban) routes
	r.Mount("/tasks", a.TaskRoutes())

	// Task notes
	r.Get("/tasks/{id}/notes", a.GetTaskNotes)

	// Goals routes
	r.Mount("/goals", a.GoalRoutes())

	// Pipeline routes
	r.Mount("/pipeline", a.PipelineRoutes())

	// Events SSE endpoint
	r.Get("/events", a.StreamEvents)

	return r
}

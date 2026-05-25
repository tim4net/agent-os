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
	queries    *db.Queries
	registry   *harness.Registry
	bus        *service.EventBus
	litellmURL string
	artifacts  *ArtifactAPI
}

// NewAPI creates a new API instance with the given dependencies.
func NewAPI(queries *db.Queries, registry *harness.Registry, bus *service.EventBus, litellmURL string, artifactsPath string) *API {
	return &API{
		queries:    queries,
		registry:   registry,
		bus:        bus,
		litellmURL: litellmURL,
		artifacts:  NewArtifactAPI(queries, artifactsPath),
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

	// Agent routes
	r.Route("/agents", func(r chi.Router) {
		r.Get("/", a.ListAgents)
		r.Post("/", a.CreateAgent)
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
		})
	})

	// Artifact routes
	r.Mount("/artifacts", a.artifacts.ArtifactRoutes())

	// Events SSE endpoint
	r.Get("/events", a.StreamEvents)

	return r
}

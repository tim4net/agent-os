package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
	"github.com/tim4net/agent-os/internal/service"
)

// API holds dependencies for the API handlers.
type API struct {
	queries          *db.Queries
	pool             *pgxpool.Pool
	registry         *harness.Registry
	bus              *service.EventBus
	feed             *service.ActivityFeed
	litellmURL       string
	obsidianPath     string
	hermesSkillsPath string
	hermesAPIKey     string
	zaiAPIKey        string
	openrouterAPIKey string
	llmModel         string
	artifacts        *ArtifactAPI
	memory           *MemoryAPI
	studio           *StudioAPI
}

// NewAPI creates a new API instance with the given dependencies.
func NewAPI(queries *db.Queries, pool *pgxpool.Pool, registry *harness.Registry, bus *service.EventBus, feed *service.ActivityFeed, litellmURL string, artifactsPath string, obsidianPath string, hermesSkillsPath string, apiKeys map[string]string, hermesAPIKey string, zaiAPIKey string, openrouterAPIKey string, llmModel string) *API {
	return &API{
		queries:          queries,
		pool:             pool,
		registry:         registry,
		bus:              bus,
		feed:             feed,
		litellmURL:       litellmURL,
		obsidianPath:     obsidianPath,
		hermesSkillsPath: hermesSkillsPath,
		hermesAPIKey:     hermesAPIKey,
		zaiAPIKey:        zaiAPIKey,
		openrouterAPIKey: openrouterAPIKey,
		llmModel:         llmModel,
		artifacts:        NewArtifactAPI(queries, artifactsPath),
		memory:           NewMemoryAPI(queries, obsidianPath, litellmURL, llmModel),
		studio:           NewStudioAPI(queries, artifactsPath, apiKeys),
	}
}

// buildHarnessConfig creates a harness config map for the given agent.
func (a *API) buildHarnessConfig(agent db.Agent) map[string]any {
	config := map[string]any{
		"base_url": agent.BaseUrl,
	}
	// Pass harness-specific config for hermes
	if agent.Harness == "hermes" {
		if a.hermesAPIKey != "" {
			config["api_key"] = a.hermesAPIKey
		}
		if a.litellmURL != "" {
			config["litellm_url"] = a.litellmURL
		}
	}
	// Pass auth_token for openclaw from metadata
	if agent.Harness == "openclaw" {
		var meta map[string]any
		if err := json.Unmarshal(agent.Metadata, &meta); err == nil {
			if token, ok := meta["auth_token"].(string); ok && token != "" {
				config["auth_token"] = token
			}
		}
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
			r.Patch("/", a.UpdateAgentConfig)
			r.Get("/config", a.GetAgentConfig)
			r.Get("/models", a.GetAgentModels)
			r.Get("/commands", a.GetAgentCommands)
			r.Post("/chat", a.ChatWithAgent)
		})
	})

	// Slash command endpoint
	r.Post("/slash-command", a.HandleSlashCommand)

	// Conversation routes
	r.Route("/conversations", func(r chi.Router) {
		r.Get("/", a.ListConversations)
		r.Post("/", a.CreateConversation)
		r.Post("/summarize", a.ConversationSummary)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/messages", a.GetConversationMessages)
			r.Post("/export", a.ExportConversation)
		})
	})

	// Artifact routes
	r.Mount("/artifacts", a.artifacts.ArtifactRoutes())

	// Artifact export and notes
	r.Post("/artifacts/{id}/export", a.ExportArtifact)
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

	// Workflow routes
	r.Mount("/workflows", a.WorkflowRoutes())

	// Skills routes
	r.Mount("/skills", a.SkillRoutes())

	// Delegation routes (webhook from Hermes)
	r.Mount("/delegations", a.DelegationRoutes())

	// Work units (correlation) routes
	r.Mount("/work-units", a.WorkUnitRoutes())

	// Timeline routes
	r.Mount("/timeline", a.TimelineRoutes())

	// Voice routes
	r.Post("/voice/transcribe", a.Transcribe)
	r.Post("/voice/synthesize", a.Synthesize)

	// Vision routes
	r.Post("/vision/analyze", a.AnalyzeVision)

	// Events SSE endpoint
	r.Get("/events", a.StreamEvents)

	return r
}

package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
	"github.com/tim4net/agent-os/internal/secret"
	"github.com/tim4net/agent-os/internal/service"
)

// API holds dependencies for the API handlers.
type API struct {
	queries          *db.Queries
	pool             *pgxpool.Pool
	registry         *harness.Registry
	bus              *service.EventBus
	feed             *service.ActivityFeed
	cipher           *secret.Cipher
	litellmURL       string
	obsidianPath     string
	hermesSkillsPath string
	hermesAPIKey     string
	anthropicAPIKey  string
	openaiAPIKey     string
	geminiAPIKey     string
	xaiAPIKey        string
	falKey           string
	zaiAPIKey        string
	openrouterAPIKey string
	llmModel         string
	artifacts        *ArtifactAPI
	memory           *MemoryAPI
	studio           *StudioAPI
}

// NewAPI creates a new API instance with the given dependencies.
func NewAPI(queries *db.Queries, pool *pgxpool.Pool, registry *harness.Registry, bus *service.EventBus, feed *service.ActivityFeed, cipher *secret.Cipher, litellmURL string, artifactsPath string, obsidianPath string, hermesSkillsPath string, apiKeys map[string]string, keys ProviderKeys, llmModel string) *API {
	return &API{
		queries:          queries,
		pool:             pool,
		registry:         registry,
		bus:              bus,
		feed:             feed,
		cipher:           cipher,
		litellmURL:       litellmURL,
		obsidianPath:     obsidianPath,
		hermesSkillsPath: hermesSkillsPath,
		hermesAPIKey:     keys.Hermes,
		anthropicAPIKey:  keys.Anthropic,
		openaiAPIKey:     keys.OpenAI,
		geminiAPIKey:     keys.Gemini,
		xaiAPIKey:        keys.XAI,
		falKey:           keys.FAL,
		zaiAPIKey:        keys.ZAI,
		openrouterAPIKey: keys.OpenRouter,
		llmModel:         llmModel,
		artifacts:        NewArtifactAPI(queries, artifactsPath),
		memory:           NewMemoryAPI(queries, obsidianPath, litellmURL, llmModel),
		studio:           NewStudioAPI(queries, artifactsPath, apiKeys),
	}
}

// ProviderKeys bundles the env-fallback provider credentials so the API
// constructor signature stays manageable as providers are added.
type ProviderKeys struct {
	Hermes     string
	Anthropic  string
	OpenAI     string
	Gemini     string
	XAI        string
	FAL        string
	ZAI        string
	OpenRouter string
}

// buildHarnessConfig creates a harness config map for the given agent.
// Secrets (hermes api_key) resolve through the settings store first, then the
// env fallback, so a key set in the Settings UI takes effect without a redeploy.
func (a *API) buildHarnessConfig(ctx context.Context, agent db.Agent) map[string]any {
	config := map[string]any{
		"base_url": agent.BaseUrl,
	}
	// Pass harness-specific config for hermes
	if agent.Harness == "hermes" {
		if key := a.resolveSecret(ctx, "hermes_api_key"); key != "" {
			config["api_key"] = key
		}
		if a.litellmURL != "" {
			config["litellm_url"] = a.litellmURL
		}
	}
	// Pass auth_token for openclaw from metadata (decrypted; supports legacy
	// plaintext rows transparently).
	if agent.Harness == "openclaw" {
		if token := a.decodeAuthToken(agent.Metadata); token != "" {
			config["auth_token"] = token
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

	// Settings (orchestrator control plane): provider keys + general config.
	r.Route("/settings", func(r chi.Router) {
		r.Get("/", a.ListSettings)
		r.Put("/{key}", a.UpdateSetting)
		r.Delete("/{key}", a.DeleteSetting)
	})

	// Harness catalog: the registered harness types an agent can use.
	r.Get("/harnesses", a.ListHarnesses)

	// Agent routes
	r.Route("/agents", func(r chi.Router) {
		r.Get("/", a.ListAgents)
		r.Post("/", a.CreateAgent)
		r.Get("/discover", a.DiscoverAgents)
		r.Post("/auto-register", a.AutoRegisterAgents)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", a.GetAgent)
			r.Patch("/", a.UpdateAgentConfig)
			r.Delete("/", a.DeleteAgent)
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

	// Work-event ingestion (WP-A, contract v1.1 §1 → POST /api/events/work).
	// Registered as a direct route rather than r.Mount("/events", …) because the
	// SSE bus already owns GET /events (below); a direct POST /events/work keeps
	// both intents without a chi mount collision.
	r.Post("/events/work", a.IngestWorkEvent)

	// Work units (correlation) routes
	r.Mount("/work-units", a.WorkUnitRoutes())

	// Control-plane routes (WP-O2, #39 — mode/cadence/queue over the orchestrator engine)
	r.Mount("/control", a.ControlRoutes())

	// Ledger routes (WP-O3, #40 — run-log + findings as DB records, read API)
	r.Mount("/ledger", a.LedgerRoutes())

	// Tracker routes (WP-E, read-only Shortcut reader, contract §8, ADR-001 D4/D5).
	r.Mount("/trackers", a.TrackerRoutes())

	// Incident routes (WP-L, #26 — failure surfacing, "what's broken now").
	r.Mount("/incidents", a.IncidentRoutes())

	// Timeline routes
	r.Mount("/timeline", a.TimelineRoutes())

	// Voice routes
	r.Post("/voice/transcribe", a.Transcribe)
	r.Post("/voice/synthesize", a.Synthesize)

	// Vision routes
	r.Post("/vision/analyze", a.AnalyzeVision)

	// Events SSE endpoint
	r.Get("/events", a.StreamEvents)

	// Spend observability (WP-K)
	r.Mount("/spend", a.SpendRoutes())

	// Instance registry + health prober (WP-I, ADR-003)
	r.Mount("/instances", a.InstanceRoutes())

	// Fleet/session liveness monitor (WP-J, F10)
	r.Mount("/fleet", a.FleetRoutes())

	// Emitter/fleet liveness — per-session health from work_events (WP-M, #27)
	r.Mount("/emitters", a.EmitterRoutes())

	// Worktree listing + host-process liveness feed (WP-N, #28)
	r.Mount("/worktrees", a.WorktreeRoutes())
	// Integrator note: PR-body proposed r.Mount("/host", …) but the handler
	// registers Post/Get at "/", and both the issue AC and the host-reporter's
	// DEFAULT_ENDPOINT target /api/host/liveness — so mount at /host/liveness.
	r.Mount("/host/liveness", a.HostLivenessRoutes())

	return r
}

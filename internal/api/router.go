package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tim4net/agent-os/internal/config"
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
	envelope         *secret.EnvelopeCipher
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
	agentManifest    []config.AgentSpec
	artifacts        *ArtifactAPI
	memory           *MemoryAPI
	studio           *StudioAPI
	mailLimiter      *mailLimiter
}

// NewAPI creates a new API instance with the given dependencies.
func NewAPI(queries *db.Queries, pool *pgxpool.Pool, registry *harness.Registry, bus *service.EventBus, feed *service.ActivityFeed, cipher *secret.Cipher, envelope *secret.EnvelopeCipher, litellmURL string, artifactsPath string, obsidianPath string, hermesSkillsPath string, apiKeys map[string]string, keys ProviderKeys, llmModel string) *API {
	return &API{
		queries:          queries,
		pool:             pool,
		registry:         registry,
		bus:              bus,
		feed:             feed,
		cipher:           cipher,
		envelope:         envelope,
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
		agentManifest:    config.LoadAgentManifest(),
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

// buildHarnessConfig creates a harness config map for the given agent by
// resolving its GRANTED vault resources (default-deny). Only resources explicitly
// granted to the agent are injected; revoking a grant removes the capability at
// the next build. Secrets are decrypted here and never cached.
func (a *API) buildHarnessConfig(ctx context.Context, agent db.Agent) map[string]any {
	config := map[string]any{
		"base_url": agent.BaseUrl,
	}
	if a.litellmURL != "" && agent.Harness == "hermes" {
		config["litellm_url"] = a.litellmURL
	}

	// Load granted resources (default-deny: nothing granted → nothing injected).
	granted, err := a.queries.ListResourcesForAgent(ctx, agent.ID)
	if err != nil {
		// On lookup failure, fall back to legacy per-agent openclaw token so a
		// transient DB error doesn't silently strip an agent's only credential.
		if agent.Harness == "openclaw" {
			if token := a.decodeAuthToken(agent.Metadata); token != "" {
				config["auth_token"] = token
			}
		}
		return config
	}

	var mcpServers []map[string]any
	for _, res := range granted {
		switch res.Kind {
		case "credential":
			secret := a.resolveResourceSecret(ctx, res)
			if secret == "" {
				continue
			}
			// Inject the first granted credential as the harness key. The harness
			// (hermes/openclaw) consumes whichever key field it expects.
			switch agent.Harness {
			case "hermes":
				if _, set := config["api_key"]; !set {
					config["api_key"] = secret
				}
			case "openclaw":
				if _, set := config["auth_token"]; !set {
					config["auth_token"] = secret
				}
			default:
				if _, set := config["api_key"]; !set {
					config["api_key"] = secret
				}
			}
		case "mcp_server":
			srv := map[string]any{"slug": res.Slug, "label": res.Label}
			var cfg map[string]any
			if len(res.Config) > 0 {
				_ = json.Unmarshal(res.Config, &cfg)
			}
			for k, v := range cfg {
				srv[k] = v
			}
			if secret := a.resolveResourceSecret(ctx, res); secret != "" {
				srv["auth_token"] = secret
			}
			mcpServers = append(mcpServers, srv)
		case "integration":
			// Integrations expose non-secret config + an optional token, namespaced
			// by slug so multiple integrations can coexist.
			intg := map[string]any{}
			var cfg map[string]any
			if len(res.Config) > 0 {
				_ = json.Unmarshal(res.Config, &cfg)
			}
			for k, v := range cfg {
				intg[k] = v
			}
			if secret := a.resolveResourceSecret(ctx, res); secret != "" {
				intg["token"] = secret
			}
			if config["integrations"] == nil {
				config["integrations"] = map[string]any{}
			}
			config["integrations"].(map[string]any)[res.Slug] = intg
		}
	}
	if len(mcpServers) > 0 {
		config["mcp_servers"] = mcpServers
	}

	// Legacy fallback: an openclaw agent with a per-agent metadata token but no
	// granted credential still works (back-compat for pre-vault agents).
	if agent.Harness == "openclaw" {
		if _, set := config["auth_token"]; !set {
			if token := a.decodeAuthToken(agent.Metadata); token != "" {
				config["auth_token"] = token
			}
		}
	}
	return config
}

// Router returns a Chi router with all API routes mounted under /api.
func (a *API) Router() http.Handler {
	r := chi.NewRouter()

	// Owner identity: all routes require a resolved owner (trusted
	// X-Webauth-User header or AOS_DEV_LOGIN fallback) so downstream
	// handlers can scope every query by owner_id. The /health endpoint
	// is exempted so health checks work even when the DB is degraded.
	// This is the Phase 1 identity spine mount deferred from PR #90.
	//
	// Nil-guard: test-only minimal APIs (e.g. TestListHarnesses_Endpoint)
	// construct &API{registry: reg} without a queries/pool. In production
	// queries is always non-nil (set by NewAPI). Skipping the mount when
	// queries is nil avoids a nil-pointer panic in GetUserByLogin; those
	// tests target static endpoints (/harnesses) that don't use owner_id.
	if a.queries != nil {
		idMw := IdentityMiddleware(a.queries, IdentityConfigFromEnv())
		r.Use(func(next http.Handler) http.Handler {
			handler := idMw(next)
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" || r.URL.Path == "/health/" {
					next.ServeHTTP(w, r)
					return
				}
				handler.ServeHTTP(w, r)
			})
		})
	}

	// Health endpoint (public — exempted above)
	r.Get("/health", a.DetailedHealth)

	// Activity feed
	r.Get("/activity", a.GetActivity)

	// Resource vault (orchestrator control plane): credentials, integrations,
	// MCP servers. Secrets encrypted at rest; responses masked.
	r.Route("/resources", func(r chi.Router) {
		r.Get("/", a.ListResources)
		r.Post("/", a.CreateResource)
		r.Put("/{id}", a.UpdateResource)
		r.Delete("/{id}", a.DeleteResource)
	})

	// Grants: the permission matrix edges. GET /grants = all edges for the matrix.
	r.Get("/grants", a.ListAllGrants)

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
			r.Get("/version", a.GetAgentVersion)
			r.Post("/chat", a.ChatWithAgent)
			r.Post("/delegate", a.DelegateToAgent)
			// Per-agent capability grants (the Access drawer).
			r.Get("/grants", a.ListAgentGrants)
			r.Put("/grants/{resourceId}", a.GrantAgentResource)
			r.Delete("/grants/{resourceId}", a.RevokeAgentResource)
			// Agent-to-agent mailbox (WP-101, issue #112).
			r.Mount("/mail", a.MailRoutes())
		})
	})

	// Slash command endpoint
	r.Post("/slash-command", a.HandleSlashCommand)

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
	r.Mount("/workflow-templates", a.WorkflowTemplateRoutes())

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

	// Workspace (project) routes — create/list/select a workspace and view its
	// scoped surface of agents + memory + artifacts (issue #134).
	r.Mount("/projects", a.ProjectRoutes())

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

	// Live host surface (issue #132): a read-only, on-demand snapshot of
	// host-side capabilities (hostname, PID, git worktrees) computed by the
	// API process itself. Distinct from /host/liveness (the DB-backed
	// heartbeat table): this exposes git/worktree info the containerised API
	// otherwise lacks.
	r.Mount("/host/live", a.HostLiveRoutes())

	return r
}

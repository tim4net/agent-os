package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/tim4net/agent-os/internal/api"
	"github.com/tim4net/agent-os/internal/config"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
	"github.com/tim4net/agent-os/internal/secret"
	"github.com/tim4net/agent-os/internal/service"
)

func main() {
	cfg := config.Load()

	// Connect to database
	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Apply pending database migrations at boot (in-app migration runner,
	// WP-MIG #11). Self-contained via go:embed; applies only pending
	// up-migrations in transactions, records schema_migrations, takes a
	// Postgres advisory lock so concurrent replicas don't race, and never
	// auto-runs destructive down-migrations.
	if _, err := db.MigrateUpWithLogger(ctx, pool, slog.Default()); err != nil {
		slog.Error("failed to apply database migrations", "error", err)
		os.Exit(1)
	}
	slog.Info("database migrations up to date")

	queries := db.New(pool)

	// Ensure known agents exist in the database (INSERT-only, never overwrite).
	// This guarantees agents are present on first boot but respects any DB-side
	// edits to display_name, role, system_prompt, persona, etc.
	ensureKnownAgents(ctx, queries)

	// Ensure infrastructure agents are hidden (prevent visible flag regression)
	// NOTE: LiteLLM agent visible as "LiteLLM on xps" for direct model access.
	// Only hide true infrastructure-only agents here if needed.

	// Create event bus
	bus := service.NewEventBus()

	// Create activity feed (ring buffer of last 200 events)
	feed := service.NewActivityFeed(bus, 200)

	// Register harnesses
	harness.Register("generic", harness.NewGenericHarness)
	harness.Register("hermes", harness.NewHermesHarness)
	harness.Register("openclaw", harness.NewOpenClawHarness)
	harness.Register("litellm", harness.NewLiteLLMHarness)
	harness.Register("agy", harness.NewAgyHarness)

	// Start agent watcher
	watcher := service.NewAgentWatcher(queries, harness.DefaultRegistry, bus, cfg.LiteLLMURL, cfg.HermesAPIKey)
	watcher.Start(ctx)
	defer watcher.Stop()

	// Start artifact scanner
	scanner := service.NewArtifactScanner(queries, bus, cfg.ArtifactsPath)
	scanner.Start(ctx)
	defer scanner.Stop()

	// Ensure the memory vault directory exists. A missing OBSIDIAN_PATH is a
	// normal first-run / fresh-deploy state; creating it here keeps the memory
	// indexer and the Knowledge > Files tree endpoint from erroring on absence.
	if err := os.MkdirAll(cfg.ObsidianPath, 0o755); err != nil {
		slog.Warn("failed to ensure obsidian vault directory", "path", cfg.ObsidianPath, "error", err)
	}

	// Start memory indexer
	indexer := service.NewMemoryIndexer(queries, bus, cfg.ObsidianPath)
	indexer.Start(ctx)
	defer indexer.Stop()

	// Omi wearable ingest (issue #135). Opt-in / deferred behind
	// higher-priority work: the background poller is only started when an
	// OMI_API_TOKEN is configured, so the default deployment is unaffected.
	if cfg.OmiAPIToken != "" {
		omiSrc := service.NewOmiClient(cfg.OmiBaseURL, cfg.OmiAPIToken, nil)
		omiIng := service.NewOmiIngester(omiSrc, queries)
		omiIng.Start(ctx)
		defer omiIng.Stop()
		slog.Info("omi-ingester: enabled", "base_url", cfg.OmiBaseURL)
	}

	// Build router
	r := chi.NewRouter()
	r.Use(api.CORS)
	r.Use(api.RequestLogger)
	r.Use(middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip timeout for SSE streams and endpoints that call the LLM
			// (goals breakdown, pipeline generate, workflows can take 60s+)
			switch {
			case strings.HasPrefix(r.URL.Path, "/api/events"):
				next.ServeHTTP(w, r)
				return
			case strings.HasSuffix(r.URL.Path, "/chat"):
				next.ServeHTTP(w, r)
				return
			case strings.HasSuffix(r.URL.Path, "/breakdown"):
				next.ServeHTTP(w, r)
				return
			case strings.HasSuffix(r.URL.Path, "/generate"):
				next.ServeHTTP(w, r)
				return
			case strings.HasSuffix(r.URL.Path, "/advance"):
				next.ServeHTTP(w, r)
				return
			case strings.HasSuffix(r.URL.Path, "/synthesize"):
				next.ServeHTTP(w, r)
				return
			case strings.HasSuffix(r.URL.Path, "/summarize"):
				next.ServeHTTP(w, r)
				return
			case strings.HasSuffix(r.URL.Path, "/delegate"):
				next.ServeHTTP(w, r)
				return
			}
			middleware.Timeout(60*time.Second)(next).ServeHTTP(w, r)
		})
	})

	// Mount API routes
	apiKeys := map[string]string{
		"xai":        cfg.XAIAPIKey,
		"openrouter": cfg.OpenRouterAPIKey,
		"gemini":     cfg.GeminiAPIKey,
		"fal":        cfg.FALKey,
	}

	// Resolve the master key for encrypted secrets at rest. Preference:
	// AOS_MASTER_KEY env, else a generated key persisted on the artifacts
	// volume so secrets survive redeploys. A nil cipher = secrets disabled
	// (the API refuses to store secrets rather than persisting plaintext).
	masterKey, err := secret.ResolveMasterKey(cfg.ArtifactsPath)
	if err != nil {
		slog.Error("failed to resolve secret master key", "error", err)
		os.Exit(1)
	}
	var cipher *secret.Cipher
	if masterKey != nil {
		cipher, err = secret.NewCipher(masterKey)
		if err != nil {
			slog.Error("failed to init secret cipher", "error", err)
			os.Exit(1)
		}
		slog.Info("secret encryption enabled (AES-256-GCM)")
	} else {
		slog.Warn("secret encryption DISABLED — set AOS_MASTER_KEY to enable encrypted API-key storage")
	}

	envelope := secret.NewEnvelopeCipher(cipher, queries)
	if cipher != nil {
		if err := secret.RunBackfill(ctx, envelope, queries, slog.Default()); err != nil {
			slog.Error("failed to run secret backfill", "error", err)
			os.Exit(1)
		}
	}

	providerKeys := api.ProviderKeys{
		Hermes:     cfg.HermesAPIKey,
		Anthropic:  cfg.AnthropicAPIKey,
		OpenAI:     cfg.OpenAIAPIKey,
		Gemini:     cfg.GeminiAPIKey,
		XAI:        cfg.XAIAPIKey,
		FAL:        cfg.FALKey,
		ZAI:        cfg.ZAIAPIKey,
		OpenRouter: cfg.OpenRouterAPIKey,
	}
	a := api.NewAPI(queries, pool, harness.DefaultRegistry, bus, feed, cipher, envelope, cfg.LiteLLMURL, cfg.ArtifactsPath, cfg.ObsidianPath, cfg.HermesSkillsPath, apiKeys, providerKeys, cfg.LLMModel)
	r.Mount("/api", a.Router())

	// Start background title worker (hourly re-summarization of active conversations)
	titleWorker := api.NewTitleWorker(a)
	titleWorker.Start(ctx)
	defer titleWorker.Stop()

	// Start server with graceful shutdown
	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // No timeout — nginx handles proxy_read_timeout; SSE streams can run indefinitely
		IdleTimeout:  120 * time.Second,
	}

	// Listen for shutdown signals
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		slog.Info("agent-os starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	slog.Info("shutting down gracefully...")

	// Stop background services
	watcher.Stop()
	scanner.Stop()
	indexer.Stop()
	titleWorker.Stop()

	// Drain connections with 10s timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	// Close database pool
	pool.Close()

	slog.Info("agent-os stopped")
}

// ensureKnownAgents inserts agents that don't yet exist in the database.
// Uses ON CONFLICT (name) DO NOTHING so existing rows (including display_name,
// role, system_prompt, persona) are never overwritten. The fleet list is the
// config-driven manifest (issue #136) — see config.LoadAgentManifest — not a
// hardcoded Go list.
func ensureKnownAgents(ctx context.Context, queries *db.Queries) {
	for _, s := range config.LoadAgentManifest() {
		_, err := queries.EnsureAgent(ctx, db.EnsureAgentParams{
			Name:        s.Hostname,
			DisplayName: s.DisplayName,
			Harness:     s.Harness,
			BaseUrl:     s.BaseURL,
			Metadata:    []byte("{}"),
		})
		if err != nil {
			// pgx.ErrNoRows means the agent already existed — that's the expected happy path.
			slog.Debug("agent seeding skipped (already exists)", "agent", s.Hostname)
		} else {
			slog.Info("agent seeding created new agent", "agent", s.Hostname, "harness", s.Harness)
		}
	}
}

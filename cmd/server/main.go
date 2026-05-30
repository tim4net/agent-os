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

	// Start agent watcher
	watcher := service.NewAgentWatcher(queries, harness.DefaultRegistry, bus, cfg.LiteLLMURL, cfg.HermesAPIKey)
	watcher.Start(ctx)
	defer watcher.Stop()

	// Start artifact scanner
	scanner := service.NewArtifactScanner(queries, bus, cfg.ArtifactsPath)
	scanner.Start(ctx)
	defer scanner.Stop()

	// Start memory indexer
	indexer := service.NewMemoryIndexer(queries, bus, cfg.ObsidianPath)
	indexer.Start(ctx)
	defer indexer.Stop()

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
	a := api.NewAPI(queries, pool, harness.DefaultRegistry, bus, feed, cfg.LiteLLMURL, cfg.ArtifactsPath, cfg.ObsidianPath, cfg.HermesSkillsPath, apiKeys, cfg.HermesAPIKey, cfg.ZAIAPIKey, cfg.OpenRouterAPIKey, cfg.LLMModel)
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

// knownAgent defines a known agent that should exist in the database.
type knownAgent struct {
	Name        string
	DisplayName string
	Harness     string
	BaseURL     string
}

// knownAgentsList is the single source of truth for agents that must exist.
// Only used for initial seeding — existing rows are never modified.
var knownAgentsList = []knownAgent{
	{Name: "roux", DisplayName: "Roux", Harness: "hermes", BaseURL: "http://roux:8080"},
	{Name: "crawbot", DisplayName: "Crawbot", Harness: "openclaw", BaseURL: "http://crawbot:8080"},
	{Name: "litellm", DisplayName: "LiteLLM on xps", Harness: "litellm", BaseURL: "http://xps:4000"},
}

// ensureKnownAgents inserts agents that don't yet exist in the database.
// Uses ON CONFLICT (name) DO NOTHING so existing rows (including display_name,
// role, system_prompt, persona) are never overwritten.
func ensureKnownAgents(ctx context.Context, queries *db.Queries) {
	for _, ka := range knownAgentsList {
		_, err := queries.EnsureAgent(ctx, db.EnsureAgentParams{
			Name:        ka.Name,
			DisplayName: ka.DisplayName,
			Harness:     ka.Harness,
			BaseUrl:     ka.BaseURL,
			Metadata:    []byte("{}"),
		})
		if err != nil {
			// pgx.ErrNoRows means the agent already existed — that's the expected happy path.
			slog.Debug("agent seeding skipped (already exists)", "agent", ka.Name)
		} else {
			slog.Info("agent seeding created new agent", "agent", ka.Name, "harness", ka.Harness)
		}
	}
}

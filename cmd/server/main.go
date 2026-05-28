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

	// Ensure infrastructure agents are hidden (prevent visible flag regression)
	// NOTE: LiteLLM agent is now user-visible as "Models" for direct model access.
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
	a := api.NewAPI(queries, harness.DefaultRegistry, bus, feed, cfg.LiteLLMURL, cfg.ArtifactsPath, cfg.ObsidianPath, cfg.HermesSkillsPath, apiKeys, cfg.HermesAPIKey, cfg.ZAIAPIKey)
	r.Mount("/api", a.Router())

	// Start server with graceful shutdown
	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second, // Long timeout for SSE streams
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

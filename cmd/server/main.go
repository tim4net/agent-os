package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
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
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	queries := db.New(pool)

	// Create event bus
	bus := service.NewEventBus()

	// Register harnesses
	harness.Register("generic", harness.NewGenericHarness)
	harness.Register("hermes", harness.NewHermesHarness)
	harness.Register("openclaw", harness.NewOpenClawHarness)
	harness.Register("litellm", harness.NewLiteLLMHarness)

	// Start agent watcher
	watcher := service.NewAgentWatcher(queries, harness.DefaultRegistry, bus, cfg.LiteLLMURL)
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
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// Health endpoint
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "agent-os",
		})
	})

	// Mount API routes
	a := api.NewAPI(queries, harness.DefaultRegistry, bus, cfg.LiteLLMURL, cfg.ArtifactsPath, cfg.ObsidianPath, cfg.XAIAPIKey)
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
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("agent-os starting on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-done
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	log.Println("agent-os stopped")
}

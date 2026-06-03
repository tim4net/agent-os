// Package main provides the orchestrator binary — the in-app continuous driver
// that claims work units and invokes the gate pipeline.
//
// WP-O5 (#42): This is the cutover binary that runs the live loop. During
// shadow mode, it runs in parallel with the external cron without merging
// (AC2). The single merge point is preserved via the existing Lead merge
// decision.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

func main() {
	dsn := flag.String("dsn", "", "database connection string (or set DATABASE_URL)")
	shadow := flag.Bool("shadow", true, "run in shadow mode (dispatch but don't merge) — AC2")
	sentinelPath := flag.String("halt-sentinel", "", "path to halt sentinel file (checked each iteration) — AC4")
	flag.Parse()

	// Setup structured logger.
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	// Resolve DSN.
	databaseURL := *dsn
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if databaseURL == "" {
		log.Error("DATABASE_URL or -dsn is required")
		os.Exit(1)
	}

	// Connect to database.
	ctx := context.Background()
	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Apply pending migrations.
	if _, err := db.MigrateUpWithLogger(ctx, pool, log); err != nil {
		log.Error("failed to apply database migrations", "error", err)
		os.Exit(1)
	}
	log.Info("database migrations up to date")

	queries := db.New(pool)
	ledger := service.NewLedgerService(queries)

	// Build driver config.
	cfg := service.DriverConfig{
		ShadowMode:       *shadow,
		HaltSentinelPath: *sentinelPath,
		GateModelFamilies: map[int32]string{
			2: "xai/grok-4",
			3: "openrouter/anthropic/claude-sonnet-4",
		},
	}

	driver := service.NewDriver(queries, ledger, cfg, log)

	// Setup signal handling.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-sigCh
		log.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	mode := "continuous"
	if *shadow {
		mode = "shadow"
	}
	log.Info("orchestrator driver starting",
		"mode", mode,
		"halt_sentinel", cfg.HaltSentinelPath,
		"gate_2_family", cfg.GateModelFamilies[2],
		"gate_3_family", cfg.GateModelFamilies[3],
	)

	// Wait for control_state to not be "stopped" before starting.
	if err := service.WaitForReady(ctx, queries, log); err != nil {
		if ctx.Err() != nil {
			log.Info("orchestrator driver stopped during wait-for-ready")
			return
		}
		log.Error("wait-for-ready failed", "error", err)
		os.Exit(1)
	}

	// Run the driver with a real gate conductor.
	// In production, this would be an HTTP-based conductor calling model APIs.
	// For now, use a no-op conductor that logs and passes everything.
	conductor := &loggingGateConductor{log: log}

	if err := driver.Run(ctx, conductor); err != nil {
		if ctx.Err() != nil {
			log.Info("orchestrator driver stopped")
		} else {
			log.Error("orchestrator driver failed", "error", err)
			os.Exit(1)
		}
	}

	log.Info("orchestrator driver exited cleanly")
}

// loggingGateConductor is a minimal GateConductor that logs gate invocations.
// In production, this is replaced by an HTTP client calling real model APIs.
type loggingGateConductor struct {
	log *slog.Logger
}

func (l *loggingGateConductor) ConductGate(ctx context.Context, unit *db.WorkUnit, gateNum int32, modelFamily string) (*service.GateResult, error) {
	l.log.Info("conducting gate (no-op)",
		"unit_id", unit.ID,
		"wp_ref", unit.WpRef,
		"gate", gateNum,
		"model_family", modelFamily,
	)
	return &service.GateResult{
		Gate:     gateNum,
		Model:    modelFamily,
		Pass:     true,
		Severity: "info",
		Class:    "no-op",
		Summary:  fmt.Sprintf("gate %d passed (no-op conductor)", gateNum),
	}, nil
}

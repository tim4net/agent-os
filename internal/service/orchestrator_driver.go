package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ---------------------------------------------------------------------------
// WP-O5 (#42): Live-dispatch driver — wires RunLoop to real gate pipeline
// ---------------------------------------------------------------------------

// GateResult holds the outcome of a single gate evaluation.
type GateResult struct {
	Gate      int32  // gate number (1, 2, 3)
	Model     string // model family used
	Pass      bool   // whether the gate passed
	Severity  string // info | warning | critical
	Class     string // finding class
	Summary   string
	RootCause string
}

// GateConductor is the interface for conducting individual gates.
// Implementations call independent model families to preserve gate
// independence (AC3).
type GateConductor interface {
	// ConductGate runs gate `gateNum` for the given work unit.
	// The caller MUST ensure gate independence by passing a different
	// modelFamily than used for prior gates on the same unit.
	ConductGate(ctx context.Context, unit *db.WorkUnit, gateNum int32, modelFamily string) (*GateResult, error)
}

// DispatchFn is the signature for per-unit dispatch callbacks used by
// the Driver. It receives the claimed unit and the current shadow-mode
// flag. In shadow mode, the dispatch MUST NOT merge — it only records
// review results (AC2).
type DispatchFn func(ctx context.Context, unit *db.WorkUnit, shadow bool) error

// DriverConfig controls the driver's behavior.
type DriverConfig struct {
	// ShadowMode is true during cutover (AC2). When true, the driver
	// dispatches reviews but does NOT merge — the existing Lead merge
	// decision remains the single merge point.
	ShadowMode bool

	// HaltSentinelPath is the filesystem path checked each iteration.
	// If this file exists and contains "autonomy:halt", the driver
	// stops dispatching within the current iteration (AC4).
	HaltSentinelPath string

	// GateModelFamilies maps gate numbers to their model families.
	// Gate 2 and Gate 3 MUST use different families (AC3).
	// Example: {2: "xai/grok-4", 3: "openrouter/anthropic/claude-sonnet-4"}
	GateModelFamilies map[int32]string
}

// Driver is the live-dispatch orchestrator driver. It wraps RunLoop
// with real gate pipeline execution, shadow-mode safety, and halt-sentinel
// honoring.
type Driver struct {
	queries *db.Queries
	ledger  *LedgerService
	config  DriverConfig
	log     *slog.Logger
}

// NewDriver creates a new Driver.
func NewDriver(queries *db.Queries, ledger *LedgerService, cfg DriverConfig, log *slog.Logger) *Driver {
	return &Driver{
		queries: queries,
		ledger:  ledger,
		config:  cfg,
		log:     log,
	}
}

// Run starts the driver loop. It reads the current control_state mode and
// delegates to RunLoop with a dispatch function that:
//  1. Checks the halt sentinel (AC4)
//  2. Validates gate independence (AC3)
//  3. Conducts gates via the GateConductor
//  4. In shadow mode, skips merge (AC2)
//  5. Marks the unit done or failed
//
// The GateConductor is injected, so the driver itself never calls model
// families directly — gate independence is structurally guaranteed.
func (d *Driver) Run(ctx context.Context, conductor GateConductor) error {
	dispatchFn := d.makeDispatchFn(conductor)
	return RunLoop(ctx, d.queries, dispatchFn, d.log)
}

// makeDispatchFn builds the dispatch function used by RunLoop.
func (d *Driver) makeDispatchFn(conductor GateConductor) func(ctx context.Context, unit *db.WorkUnit) error {
	return func(ctx context.Context, unit *db.WorkUnit) error {
		// AC4: Check halt sentinel.
		if d.shouldHalt() {
			d.log.Info("halt sentinel detected — stopping dispatch",
				"unit_id", unit.ID,
				"wp_ref", unit.WpRef,
			)
			return nil
		}

		// AC4: Check app-native stop flag. Re-read mode.
		state, err := d.queries.GetControlState(ctx)
		if err != nil {
			return fmt.Errorf("driver: read control state: %w", err)
		}
		if db.ControlMode(state.Mode) == db.ControlModeStopped {
			d.log.Info("mode is stopped — skipping dispatch",
				"unit_id", unit.ID,
			)
			return nil
		}

		d.log.Info("dispatching unit",
			"unit_id", unit.ID,
			"wp_ref", unit.WpRef,
			"shadow", d.config.ShadowMode,
		)

		// AC3: Validate gate independence — Gate 2 and Gate 3 must use
		// different model families.
		if err := d.validateGateIndependence(); err != nil {
			d.log.Error("gate independence violation — failing unit",
				"unit_id", unit.ID,
				"error", err,
			)
			if _, failErr := d.queries.FailWorkUnit(ctx, db.FailWorkUnitParams{
				ID: unit.ID,
				Error: pgtype.Text{
					String: err.Error(),
					Valid:  true,
				},
			}); failErr != nil {
				return fmt.Errorf("driver: fail unit after gate-independence violation: %w (original: %v)", failErr, err)
			}
			return nil // Don't propagate — the unit is failed, loop continues.
		}

		// Conduct gates.
		results, dispatchErr := d.conductGates(ctx, conductor, unit)

		// Record findings to the ledger.
		for _, gr := range results {
			if gr == nil {
				continue
			}
			_, logErr := d.ledger.AppendFinding(ctx,
				prRefFromUnit(unit),
				unit.WpRef,
				gr.Gate,
				"orchestrator-driver",
				gr.Model,
				gr.Severity,
				gr.Class,
				pgtype.Text{String: gr.RootCause, Valid: gr.RootCause != ""},
				pgtype.Text{String: gr.Summary, Valid: gr.Summary != ""},
			)
			if logErr != nil {
				d.log.Error("failed to record finding in ledger",
					"unit_id", unit.ID,
					"gate", gr.Gate,
					"error", logErr,
				)
			}
		}

		// Record run_log entry.
		runLogSummary := "dispatched"
		if d.config.ShadowMode {
			runLogSummary = "dispatched (shadow)"
		}
		if dispatchErr != nil {
			runLogSummary = fmt.Sprintf("dispatch failed: %v", dispatchErr)
		}
		_, _ = d.ledger.AppendRunLog(ctx,
			"driver_dispatch",
			prRefFromUnit(unit),
			unit.WpRef,
			pgtype.Text{String: runLogSummary, Valid: true},
			unit.Payload,
		)

		if dispatchErr != nil {
			d.log.Error("gate pipeline failed — failing unit",
				"unit_id", unit.ID,
				"error", dispatchErr,
			)
			if _, failErr := d.queries.FailWorkUnit(ctx, db.FailWorkUnitParams{
				ID: unit.ID,
				Error: pgtype.Text{
					String: dispatchErr.Error(),
					Valid:  true,
				},
			}); failErr != nil {
				return fmt.Errorf("driver: fail unit: %w (dispatch error: %v)", failErr, dispatchErr)
			}
			return nil
		}

		// AC2: In shadow mode, do NOT merge. The existing Lead merge decision
		// is the single merge point. We only complete the unit.
		if d.config.ShadowMode {
			d.log.Info("shadow mode — skipping merge, completing unit",
				"unit_id", unit.ID,
				"wp_ref", unit.WpRef,
			)
		}

		// Mark unit done.
		if _, completeErr := d.queries.CompleteWorkUnit(ctx, unit.ID); completeErr != nil {
			return fmt.Errorf("driver: complete unit %d: %w", unit.ID, completeErr)
		}

		d.log.Info("unit dispatched and completed",
			"unit_id", unit.ID,
			"wp_ref", unit.WpRef,
			"shadow", d.config.ShadowMode,
			"gates_run", len(results),
		)
		return nil
	}
}

// conductGates runs all configured gates for a work unit via the conductor.
func (d *Driver) conductGates(ctx context.Context, conductor GateConductor, unit *db.WorkUnit) ([]*GateResult, error) {
	var results []*GateResult
	for gateNum := int32(1); gateNum <= 3; gateNum++ {
		modelFamily, ok := d.config.GateModelFamilies[gateNum]
		if !ok {
			// Gate not configured — skip.
			continue
		}
		d.log.Info("conducting gate",
			"unit_id", unit.ID,
			"gate", gateNum,
			"model_family", modelFamily,
		)
		gr, err := conductor.ConductGate(ctx, unit, gateNum, modelFamily)
		if err != nil {
			return results, fmt.Errorf("gate %d: %w", gateNum, err)
		}
		results = append(results, gr)
		if gr != nil && !gr.Pass {
			return results, fmt.Errorf("gate %d failed: %s", gateNum, gr.Summary)
		}
	}
	return results, nil
}

// validateGateIndependence ensures Gate 2 and Gate 3 use different model
// families. Returns an error if they are the same (AC3).
func (d *Driver) validateGateIndependence() error {
	g2, has2 := d.config.GateModelFamilies[2]
	g3, has3 := d.config.GateModelFamilies[3]
	if has2 && has3 {
		// Compare model families by prefix (before the specific model name).
		// e.g. "xai/grok-4" and "xai/grok-3" share family "xai".
		family2 := modelFamilyPrefix(g2)
		family3 := modelFamilyPrefix(g3)
		if family2 == family3 {
			return fmt.Errorf("gate independence violation: gate 2 and gate 3 use the same model family %q (gate2=%q, gate3=%q)",
				family2, g2, g3)
		}
	}
	return nil
}

// shouldHalt checks the halt sentinel file (AC4). Returns true if the file
// exists and contains "autonomy:halt".
func (d *Driver) shouldHalt() bool {
	if d.config.HaltSentinelPath == "" {
		return false
	}
	data, err := os.ReadFile(d.config.HaltSentinelPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			d.log.Error("failed to read halt sentinel", "path", d.config.HaltSentinelPath, "error", err)
		}
		return false
	}
	return strings.Contains(string(data), "autonomy:halt")
}

// prRefFromUnit extracts a PR reference from the work unit payload, if present.
func prRefFromUnit(unit *db.WorkUnit) string {
	if unit.Payload == nil {
		return ""
	}
	var payload struct {
		PRRef string `json:"pr_ref"`
	}
	if err := json.Unmarshal(unit.Payload, &payload); err == nil {
		return payload.PRRef
	}
	return ""
}

// modelFamilyPrefix extracts the model family prefix from a model identifier.
// For "xai/grok-4", returns "xai". For "openrouter/anthropic/claude-sonnet-4",
// returns "openrouter/anthropic". Single-component names are returned as-is.
func modelFamilyPrefix(model string) string {
	parts := strings.Split(model, "/")
	if len(parts) <= 1 {
		return model
	}
	// Use first two segments as the family — e.g. "openrouter/anthropic"
	// is a different family from "openrouter/openai".
	if len(parts) >= 3 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// DefaultDriverConfig returns a sensible default driver config for shadow mode.
func DefaultDriverConfig() DriverConfig {
	return DriverConfig{
		ShadowMode: true,
		GateModelFamilies: map[int32]string{
			2: "xai/grok-4",
			3: "openrouter/anthropic/claude-sonnet-4",
		},
	}
}

// WaitForReady polls the control_state until mode is not "stopped" or context
// is cancelled. Used by cmd/orchestrator to wait for an explicit start signal.
func WaitForReady(ctx context.Context, queries *db.Queries, log *slog.Logger) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			state, err := queries.GetControlState(ctx)
			if err != nil {
				log.Error("failed to read control state", "error", err)
				continue
			}
			if db.ControlMode(state.Mode) != db.ControlModeStopped {
				return nil
			}
		}
	}
}

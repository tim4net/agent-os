package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/tim4net/agent-os/internal/config"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

func main() {
	log := slog.Default()
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if _, err := db.MigrateUpWithLogger(ctx, pool, log); err != nil {
		log.Error("failed to apply database migrations", "error", err)
		os.Exit(1)
	}
	log.Info("database migrations up to date")

	pipelineCmd := strings.TrimSpace(os.Getenv("AOS_GATE_PIPELINE_CMD"))
	if pipelineCmd == "" {
		log.Error("AOS_GATE_PIPELINE_CMD is required; wire it to the existing Hermes gate shell adapter during cutover")
		os.Exit(1)
	}

	queries := db.New(pool)
	orch := service.NewOrchestrator(queries)
	ledger := service.NewLedgerService(queries)
	pipeline := shellGatePipeline{command: pipelineCmd}
	driver := service.NewOrchestratorDriver(
		queries,
		orch,
		ledger,
		pipeline,
		service.WithDriverLogger(log),
		service.WithDriverShadow(true),
		service.WithDriverHaltPredicate(shellHaltPredicate(log)),
	)

	log.Info("orchestrator driver starting", "shadow", true)
	if err := driver.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("orchestrator driver failed", "error", err)
		os.Exit(1)
	}
	log.Info("orchestrator driver stopped")
}

type shellGatePipeline struct {
	command string
}

type shellGateRequest struct {
	ID           int64           `json:"id"`
	WpRef        string          `json:"wp_ref"`
	Status       string          `json:"status"`
	Payload      json.RawMessage `json:"payload"`
	Shadow       bool            `json:"shadow"`
	MergeAllowed bool            `json:"merge_allowed"`
}

func (p shellGatePipeline) Run(ctx context.Context, unit *db.WorkUnit) (service.GateResult, error) {
	if p.command == "" {
		return service.GateResult{}, fmt.Errorf("gate pipeline command is not configured")
	}

	request := shellGateRequest{
		ID:           unit.ID,
		WpRef:        unit.WpRef,
		Status:       string(unit.Status),
		Payload:      json.RawMessage(unit.Payload),
		Shadow:       true,
		MergeAllowed: false,
	}
	stdin, err := json.Marshal(request)
	if err != nil {
		return service.GateResult{}, fmt.Errorf("marshal gate request: %w", err)
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", p.command)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = append(os.Environ(),
		"AOS_WORK_UNIT_ID="+strconv.FormatInt(unit.ID, 10),
		"AOS_WP_REF="+unit.WpRef,
		"AOS_ORCHESTRATOR_SHADOW=true",
		"AOS_MERGE_ALLOWED=false",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return service.GateResult{}, fmt.Errorf("gate pipeline command failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	trimmed := bytes.TrimSpace(stdout.Bytes())
	if len(trimmed) == 0 {
		return service.GateResult{}, fmt.Errorf("gate pipeline command produced empty stdout")
	}
	var result service.GateResult
	if err := json.Unmarshal(trimmed, &result); err != nil {
		return service.GateResult{}, fmt.Errorf("decode gate pipeline JSON stdout: %w: %s", err, string(trimmed))
	}
	return result, nil
}

func shellHaltPredicate(log *slog.Logger) service.HaltPredicate {
	command := strings.TrimSpace(os.Getenv("AOS_HALT_CHECK_CMD"))
	if command == "" {
		command = "gh issue list --repo tim4net/agent-os --label autonomy:halt --state open --json number --jq length"
	}
	if strings.EqualFold(command, "off") || strings.EqualFold(command, "disabled") {
		log.Warn("external autonomy:halt check disabled by AOS_HALT_CHECK_CMD")
		return nil
	}
	return func(ctx context.Context) (bool, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return false, fmt.Errorf("halt check command failed: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		out := strings.TrimSpace(stdout.String())
		if out == "" || out == "0" {
			return false, nil
		}
		return true, nil
	}
}

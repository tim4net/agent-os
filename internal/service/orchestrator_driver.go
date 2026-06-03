package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

var (
	// ErrGateModelFamilyNotIndependent is returned when Gate 2 and Gate 3 are
	// configured to use the same model family. The driver treats this as a
	// degraded fail-closed condition rather than silently collapsing the gates.
	ErrGateModelFamilyNotIndependent = errors.New("gate model independence violated")

	// ErrOrchestratorHalted is returned for a unit claimed after the external
	// halt predicate has fired. The driver sets mode=stopped and records the
	// claimed unit as failed instead of dispatching gate work.
	ErrOrchestratorHalted = errors.New("orchestrator halted")
)

// GatePipeline is the adapter seam between the in-app orchestrator and the
// gate implementation. Today the gate pipeline is implemented outside Go by the
// Hermes cron prompt/shell flow; production wiring may satisfy this interface by
// invoking that existing shell pipeline. Tests use a fake implementation.
type GatePipeline interface {
	Run(ctx context.Context, unit *db.WorkUnit) (GateResult, error)
}

// GateResult records the observable outcome of the gate pipeline. Shadow-mode
// callers must leave Merged and MergeAttempted false; the driver enforces this
// and fails closed if an adapter reports otherwise.
type GateResult struct {
	PRRef             string        `json:"pr_ref,omitempty"`
	WpRef             string        `json:"wp_ref,omitempty"`
	Summary           string        `json:"summary,omitempty"`
	Gate2Model        string        `json:"gate2_model,omitempty"`
	Gate3Model        string        `json:"gate3_model,omitempty"`
	Gate2Passed       bool          `json:"gate2_passed,omitempty"`
	Gate3Passed       bool          `json:"gate3_passed,omitempty"`
	Merged            bool          `json:"merged"`
	MergeAttempted    bool          `json:"merge_attempted"`
	Shadow            bool          `json:"shadow"`
	Degraded          bool          `json:"degraded"`
	DegradationReason string        `json:"degradation_reason,omitempty"`
	Findings          []GateFinding `json:"findings,omitempty"`
}

// GateFinding is persisted to the findings ledger for gate findings and driver
// guardrail violations.
type GateFinding struct {
	Gate        int32  `json:"gate"`
	AuthorAgent string `json:"author_agent,omitempty"`
	Model       string `json:"model,omitempty"`
	Severity    string `json:"severity,omitempty"`
	Class       string `json:"class,omitempty"`
	RootCause   string `json:"root_cause,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

// HaltPredicate returns true when the external autonomy halt sentinel is set.
// The command binary wires this to the same autonomy:halt check used by the
// cron prompts; tests can provide a deterministic predicate.
type HaltPredicate func(ctx context.Context) (bool, error)

// OrchestratorDriver owns the dispatch bookkeeping around RunLoop: gate
// invocation, ledger writes, and Complete/Fail transitions. It does not claim
// work itself; RunLoop performs the claim and supplies in-flight units to the
// driver's dispatch function.
type OrchestratorDriver struct {
	queries *db.Queries
	orch    *Orchestrator
	ledger  *LedgerService
	gate    GatePipeline
	log     *slog.Logger

	// Shadow defaults to true. In shadow mode the driver dispatches gates and
	// records ledger state, but it never grants merge permission and fails closed
	// if a gate adapter reports a merge or merge attempt.
	Shadow bool

	Halt              HaltPredicate
	HaltCheckInterval time.Duration
}

type OrchestratorDriverOption func(*OrchestratorDriver)

// WithDriverLogger sets the logger used by the driver and RunLoop.
func WithDriverLogger(log *slog.Logger) OrchestratorDriverOption {
	return func(d *OrchestratorDriver) {
		if log != nil {
			d.log = log
		}
	}
}

// WithDriverShadow configures shadow mode. Callers that omit this option get
// the load-bearing default: shadow=true.
func WithDriverShadow(shadow bool) OrchestratorDriverOption {
	return func(d *OrchestratorDriver) {
		d.Shadow = shadow
	}
}

// WithDriverHaltPredicate configures the external halt sentinel check.
func WithDriverHaltPredicate(pred HaltPredicate) OrchestratorDriverOption {
	return func(d *OrchestratorDriver) {
		d.Halt = pred
	}
}

// WithDriverHaltCheckInterval configures how often the driver polls the halt
// predicate while RunLoop is idle. Non-positive intervals disable the watcher;
// dispatch still checks the predicate immediately before invoking the gate.
func WithDriverHaltCheckInterval(interval time.Duration) OrchestratorDriverOption {
	return func(d *OrchestratorDriver) {
		d.HaltCheckInterval = interval
	}
}

func NewOrchestratorDriver(queries *db.Queries, orch *Orchestrator, ledger *LedgerService, gate GatePipeline, opts ...OrchestratorDriverOption) *OrchestratorDriver {
	d := &OrchestratorDriver{
		queries:           queries,
		orch:              orch,
		ledger:            ledger,
		gate:              gate,
		log:               slog.Default(),
		Shadow:            true,
		HaltCheckInterval: 5 * time.Second,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Run starts the in-app orchestrator driver using the existing service RunLoop.
func (d *OrchestratorDriver) Run(ctx context.Context) error {
	if d == nil {
		return errors.New("orchestrator driver is nil")
	}
	if d.queries == nil || d.orch == nil || d.ledger == nil || d.gate == nil {
		return errors.New("orchestrator driver dependencies are not fully configured")
	}

	halted, err := d.stopIfHalted(ctx)
	if err != nil {
		return err
	}
	if halted {
		return nil
	}

	runCtx := ctx
	cancel := func() {}
	var haltFired atomic.Bool
	if d.Halt != nil && d.HaltCheckInterval > 0 {
		runCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		go d.watchHalt(runCtx, cancel, &haltFired)
	}

	var dispatchMu sync.Mutex
	var firstDispatchErr error
	dispatchFn := func(ctx context.Context, unit *db.WorkUnit) error {
		err := d.dispatch(ctx, unit)
		if err != nil {
			dispatchMu.Lock()
			if firstDispatchErr == nil {
				firstDispatchErr = err
			}
			dispatchMu.Unlock()
		}
		return err
	}

	err = RunLoop(runCtx, d.queries, dispatchFn, d.log)
	if err != nil {
		if haltFired.Load() && errors.Is(err, context.Canceled) && ctx.Err() == nil {
			return nil
		}
		return err
	}

	dispatchMu.Lock()
	defer dispatchMu.Unlock()
	return firstDispatchErr
}

func (d *OrchestratorDriver) watchHalt(ctx context.Context, cancel context.CancelFunc, haltFired *atomic.Bool) {
	ticker := time.NewTicker(d.HaltCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			halted, err := d.stopIfHalted(ctx)
			if err != nil {
				d.log.Error("halt predicate failed", "error", err)
				continue
			}
			if halted {
				haltFired.Store(true)
				cancel()
				return
			}
		}
	}
}

func (d *OrchestratorDriver) stopIfHalted(ctx context.Context) (bool, error) {
	if d.Halt == nil {
		return false, nil
	}
	halted, err := d.Halt(ctx)
	if err != nil {
		return false, fmt.Errorf("halt predicate: %w", err)
	}
	if !halted {
		return false, nil
	}
	if _, err := d.orch.SetMode(ctx, ModeStopped); err != nil {
		return false, fmt.Errorf("set orchestrator stopped after halt: %w", err)
	}
	_, err = d.ledger.AppendRunLog(ctx,
		"orchestrator.halted",
		"",
		"",
		pgText("external autonomy halt sentinel set; mode forced to stopped"),
		[]byte(`{"halted":true}`),
	)
	if err != nil {
		return false, fmt.Errorf("record halt run log: %w", err)
	}
	return true, nil
}

func (d *OrchestratorDriver) dispatch(ctx context.Context, unit *db.WorkUnit) error {
	if unit == nil {
		return nil
	}

	halted, err := d.stopIfHalted(ctx)
	if err != nil {
		return d.failDispatch(ctx, unit, err, GateResult{Shadow: d.Shadow})
	}
	if halted {
		return d.failDispatch(ctx, unit, ErrOrchestratorHalted, GateResult{Shadow: d.Shadow})
	}

	prepared, plan, err := d.prepareUnit(unit)
	if err != nil {
		return d.failDispatch(ctx, unit, err, GateResult{Shadow: d.Shadow})
	}

	if err := GateModelsIndependent(plan.Gate2Model, plan.Gate3Model); err != nil {
		result := GateResult{
			PRRef:             plan.PRRef,
			WpRef:             unit.WpRef,
			Gate2Model:        plan.Gate2Model,
			Gate3Model:        plan.Gate3Model,
			Shadow:            d.Shadow,
			Degraded:          true,
			DegradationReason: err.Error(),
		}
		return d.failDispatch(ctx, unit, err, result)
	}

	result, err := d.gate.Run(ctx, prepared)
	result.Shadow = d.Shadow
	if result.WpRef == "" {
		result.WpRef = unit.WpRef
	}
	if result.PRRef == "" {
		result.PRRef = plan.PRRef
	}
	if result.Gate2Model == "" {
		result.Gate2Model = plan.Gate2Model
	}
	if result.Gate3Model == "" {
		result.Gate3Model = plan.Gate3Model
	}
	if err != nil {
		return d.failDispatch(ctx, unit, err, result)
	}

	if d.Shadow && (result.Merged || result.MergeAttempted) {
		err := fmt.Errorf("shadow-mode merge guard tripped: merged=%t merge_attempted=%t", result.Merged, result.MergeAttempted)
		result.Degraded = true
		result.DegradationReason = err.Error()
		return d.failDispatch(ctx, unit, err, result)
	}

	if err := GateModelsIndependent(result.Gate2Model, result.Gate3Model); err != nil {
		result.Degraded = true
		result.DegradationReason = err.Error()
		return d.failDispatch(ctx, unit, err, result)
	}

	if err := d.appendRunLog(ctx, "orchestrator.dispatch.completed", unit, result); err != nil {
		return d.failDispatch(ctx, unit, err, result)
	}
	if err := d.appendFindings(ctx, unit, result); err != nil {
		return d.failDispatch(ctx, unit, err, result)
	}
	if _, err := d.orch.Complete(ctx, unit.ID); err != nil {
		return fmt.Errorf("complete work unit %d: %w", unit.ID, err)
	}
	return nil
}

type gatePlan struct {
	PRRef      string
	Gate2Model string
	Gate3Model string
}

func (d *OrchestratorDriver) prepareUnit(unit *db.WorkUnit) (*db.WorkUnit, gatePlan, error) {
	payload := map[string]any{}
	if len(unit.Payload) > 0 && string(unit.Payload) != "null" {
		if err := json.Unmarshal(unit.Payload, &payload); err != nil {
			return nil, gatePlan{}, fmt.Errorf("decode work unit payload for unit %d: %w", unit.ID, err)
		}
	}

	plan := gatePlan{
		PRRef:      stringFromPayload(payload, "pr_ref", "pr", "pull_request"),
		Gate2Model: stringFromPayload(payload, "gate2_model", "gate_2_model", "gate2Model"),
		Gate3Model: stringFromPayload(payload, "gate3_model", "gate_3_model", "gate3Model"),
	}

	payload["orchestrator_shadow"] = d.Shadow
	payload["dispatch_only"] = d.Shadow
	payload["merge_allowed"] = !d.Shadow
	payload["work_unit_id"] = unit.ID
	payload["wp_ref"] = unit.WpRef

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, gatePlan{}, fmt.Errorf("encode driver payload for unit %d: %w", unit.ID, err)
	}
	prepared := *unit
	prepared.Payload = encoded
	return &prepared, plan, nil
}

func stringFromPayload(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := payload[key]
		if !ok || v == nil {
			continue
		}
		switch typed := v.(type) {
		case string:
			return strings.TrimSpace(typed)
		case json.Number:
			return typed.String()
		default:
			return strings.TrimSpace(fmt.Sprint(typed))
		}
	}
	return ""
}

func (d *OrchestratorDriver) failDispatch(ctx context.Context, unit *db.WorkUnit, dispatchErr error, result GateResult) error {
	if result.WpRef == "" {
		result.WpRef = unit.WpRef
	}
	result.Shadow = d.Shadow

	logErr := d.appendRunLog(ctx, "orchestrator.dispatch.failed", unit, result)
	findingErr := d.appendDriverFinding(ctx, unit, result, dispatchErr)
	_, failErr := d.orch.Fail(ctx, unit.ID, dispatchErr.Error())

	return errors.Join(dispatchErr, logErr, findingErr, failErr)
}

func (d *OrchestratorDriver) appendRunLog(ctx context.Context, eventType string, unit *db.WorkUnit, result GateResult) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal %s run log payload for unit %d: %w", eventType, unit.ID, err)
	}
	summary := result.Summary
	if summary == "" {
		summary = fmt.Sprintf("%s for work unit %d (%s)", eventType, unit.ID, unit.WpRef)
	}
	_, err = d.ledger.AppendRunLog(ctx, eventType, result.PRRef, unit.WpRef, pgText(summary), payload)
	if err != nil {
		return fmt.Errorf("append %s run log for unit %d: %w", eventType, unit.ID, err)
	}
	return nil
}

func (d *OrchestratorDriver) appendFindings(ctx context.Context, unit *db.WorkUnit, result GateResult) error {
	var errs []error
	for _, finding := range result.Findings {
		if finding.Class == "" && finding.Summary == "" {
			continue
		}
		if err := d.appendFinding(ctx, unit, result, finding); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (d *OrchestratorDriver) appendDriverFinding(ctx context.Context, unit *db.WorkUnit, result GateResult, dispatchErr error) error {
	class := "dispatch_error"
	severity := "error"
	gate := int32(0)
	model := ""
	if errors.Is(dispatchErr, ErrGateModelFamilyNotIndependent) {
		class = "gate_independence_degraded"
		severity = "warning"
		gate = 3
		model = result.Gate3Model
	} else if d.Shadow && strings.Contains(dispatchErr.Error(), "shadow-mode merge guard") {
		class = "shadow_merge_guard"
		severity = "critical"
	}

	return d.appendFinding(ctx, unit, result, GateFinding{
		Gate:        gate,
		AuthorAgent: "orchestrator-driver",
		Model:       model,
		Severity:    severity,
		Class:       class,
		RootCause:   dispatchErr.Error(),
		Summary:     dispatchErr.Error(),
	})
}

func (d *OrchestratorDriver) appendFinding(ctx context.Context, unit *db.WorkUnit, result GateResult, finding GateFinding) error {
	if finding.AuthorAgent == "" {
		finding.AuthorAgent = "gate-pipeline"
	}
	if finding.Severity == "" {
		finding.Severity = "info"
	}
	if finding.Class == "" {
		finding.Class = "gate_finding"
	}
	_, err := d.ledger.AppendFinding(ctx,
		result.PRRef,
		unit.WpRef,
		finding.Gate,
		finding.AuthorAgent,
		finding.Model,
		finding.Severity,
		finding.Class,
		pgText(finding.RootCause),
		pgText(finding.Summary),
	)
	if err != nil {
		return fmt.Errorf("append finding for unit %d class %q: %w", unit.ID, finding.Class, err)
	}
	return nil
}

func pgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// GateModelsIndependent returns an error when Gate 2 and Gate 3 resolve to the
// same normalized model family. Empty model names are tolerated so adapters can
// report partial/deferred gate metadata without being failed by this guard.
func GateModelsIndependent(gate2Model, gate3Model string) error {
	gate2Family := ModelFamily(gate2Model)
	gate3Family := ModelFamily(gate3Model)
	if gate2Family == "" || gate3Family == "" || gate2Family != gate3Family {
		return nil
	}
	return fmt.Errorf("%w: gate2=%q gate3=%q family=%q", ErrGateModelFamilyNotIndependent, gate2Model, gate3Model, gate2Family)
}

// ModelFamily normalizes provider/model strings enough to catch accidental
// same-family Gate 2/Gate 3 pairings without coupling the driver to one model
// vendor's exact version naming scheme.
func ModelFamily(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	if idx := strings.LastIndex(m, "/"); idx >= 0 && idx < len(m)-1 {
		m = m[idx+1:]
	}
	m = strings.TrimPrefix(m, "models/")
	m = strings.TrimPrefix(m, "model/")

	tokens := strings.FieldsFunc(m, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
	for _, token := range tokens {
		if token == "" || allDigits(token) {
			continue
		}
		return token
	}
	return m
}

func allDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

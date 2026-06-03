package service

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// LedgerService wraps ledger queries with typed methods.
type LedgerService struct {
	q *db.Queries
}

// NewLedgerService creates a new LedgerService.
func NewLedgerService(q *db.Queries) *LedgerService {
	return &LedgerService{q: q}
}

// AppendRunLog inserts a new run_log entry.
func (s *LedgerService) AppendRunLog(ctx context.Context, eventType, prRef, wpRef string, summary pgtype.Text, payload []byte) (db.RunLog, error) {
	return s.q.AppendRunLog(ctx, db.AppendRunLogParams{
		EventType: eventType,
		PrRef:     prRef,
		WpRef:     wpRef,
		Summary:   summary,
		Payload:   payload,
	})
}

// AppendFinding inserts a new findings entry.
func (s *LedgerService) AppendFinding(ctx context.Context, prRef, wpRef string, gate int32, authorAgent, model, severity, class string, rootCause, summary pgtype.Text) (db.Finding, error) {
	return s.q.AppendFinding(ctx, db.AppendFindingParams{
		PrRef:       prRef,
		WpRef:       wpRef,
		Gate:        gate,
		AuthorAgent: authorAgent,
		Model:       model,
		Severity:    severity,
		Class:       class,
		RootCause:   rootCause,
		Summary:     summary,
	})
}

// ListRunLog returns run_log entries ordered by ts DESC with pagination.
func (s *LedgerService) ListRunLog(ctx context.Context, limit, offset int32) ([]db.RunLog, error) {
	return s.q.ListRunLog(ctx, db.ListRunLogParams{
		Limit:  limit,
		Offset: offset,
	})
}

// CountRunLog returns the total number of run_log entries.
func (s *LedgerService) CountRunLog(ctx context.Context) (int64, error) {
	return s.q.CountRunLog(ctx)
}

// ListFindings returns findings ordered by ts DESC with pagination.
func (s *LedgerService) ListFindings(ctx context.Context, limit, offset int32) ([]db.Finding, error) {
	return s.q.ListFindings(ctx, db.ListFindingsParams{
		Limit:  limit,
		Offset: offset,
	})
}

// CountFindings returns the total number of findings.
func (s *LedgerService) CountFindings(ctx context.Context) (int64, error) {
	return s.q.CountFindings(ctx)
}

// ListFindingsByClass returns findings filtered by class.
func (s *LedgerService) ListFindingsByClass(ctx context.Context, class string, limit, offset int32) ([]db.Finding, error) {
	return s.q.ListFindingsByClass(ctx, db.ListFindingsByClassParams{
		Class:  class,
		Limit:  limit,
		Offset: offset,
	})
}

// ListFindingsBySeverity returns findings filtered by severity.
func (s *LedgerService) ListFindingsBySeverity(ctx context.Context, severity string, limit, offset int32) ([]db.Finding, error) {
	return s.q.ListFindingsBySeverity(ctx, db.ListFindingsBySeverityParams{
		Severity: severity,
		Limit:    limit,
		Offset:   offset,
	})
}

// ListFindingsByWpRef returns findings filtered by wp_ref.
func (s *LedgerService) ListFindingsByWpRef(ctx context.Context, wpRef string, limit, offset int32) ([]db.Finding, error) {
	return s.q.ListFindingsByWpRef(ctx, db.ListFindingsByWpRefParams{
		WpRef:  wpRef,
		Limit:  limit,
		Offset: offset,
	})
}

// RecurringFindings returns finding classes with >= minCount occurrences.
func (s *LedgerService) RecurringFindings(ctx context.Context, minCount int64) ([]db.RecurringFindingsRow, error) {
	return s.q.RecurringFindings(ctx, minCount)
}

package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// fakeLister implements WorkUnitLister for table-free unit testing.
type fakeLister struct {
	rows  []db.ListWorkUnitsRow
	count int64
	err   error
}

func (f *fakeLister) ListWorkUnits(_ context.Context, arg db.ListWorkUnitsParams) ([]db.ListWorkUnitsRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	// emulate LIMIT/OFFSET so the default-clamp behavior is exercised
	start := int(arg.Offset)
	if start > len(f.rows) {
		start = len(f.rows)
	}
	end := start + int(arg.Limit)
	if end > len(f.rows) {
		end = len(f.rows)
	}
	return f.rows[start:end], nil
}

func (f *fakeLister) CountWorkUnits(_ context.Context) (int64, error) {
	return f.count, f.err
}

func boolRow(correlated bool, ext, branch, sha string, events int64) db.ListWorkUnitsRow {
	return db.ListWorkUnitsRow{
		ProjectKey:   "p1",
		ExternalRef:  ext,
		Branch:       branch,
		Sha:          sha,
		EventCount:   events,
		SessionCount: 1,
		Correlated:   pgtype.Bool{Bool: correlated, Valid: true},
	}
}

// The cardinal correlation requirement (ADR-001 F3): un-joinable events are surfaced as
// an uncorrelated unit, NEVER dropped.
func TestListWorkUnits_UncorrelatedBucketSurfaced(t *testing.T) {
	f := &fakeLister{
		rows: []db.ListWorkUnitsRow{
			boolRow(true, "SC-91130", "wp-g/x", "abc123", 4), // correlated
			boolRow(false, "", "", "", 2),                    // uncorrelated bucket
		},
		count: 2,
	}
	eng := NewCorrelationEngine(f)
	units, err := eng.ListWorkUnits(context.Background(), 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("expected 2 work units (1 correlated + 1 uncorrelated), got %d — an event group was dropped", len(units))
	}
	var sawCorrelated, sawUncorrelated bool
	for _, u := range units {
		if u.Correlated {
			sawCorrelated = true
			if u.ExternalRef != "SC-91130" {
				t.Errorf("correlated unit lost its external_ref: %+v", u)
			}
		} else {
			sawUncorrelated = true
			if u.EventCount != 2 {
				t.Errorf("uncorrelated bucket should retain its 2 events, got %d", u.EventCount)
			}
		}
	}
	if !sawCorrelated || !sawUncorrelated {
		t.Fatalf("both correlated and uncorrelated units must be present (correlated=%v uncorrelated=%v)", sawCorrelated, sawUncorrelated)
	}
}

func TestListWorkUnits_DefaultsAndClamps(t *testing.T) {
	f := &fakeLister{rows: []db.ListWorkUnitsRow{boolRow(true, "SC-1", "b", "s", 1)}, count: 1}
	eng := NewCorrelationEngine(f)
	// limit<=0 and offset<0 must be normalized, not passed through as-is.
	units, err := eng.ListWorkUnits(context.Background(), 0, -5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(units) != 1 {
		t.Fatalf("expected 1 unit with clamped paging, got %d", len(units))
	}
}

func TestCount(t *testing.T) {
	f := &fakeLister{count: 7}
	eng := NewCorrelationEngine(f)
	n, err := eng.Count(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 7 {
		t.Fatalf("expected count 7, got %d", n)
	}
}

package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// fakeLister exercises engine-level logic (paging/clamp/mapping) WITHOUT a DB.
// NOTE: it deliberately does NOT emulate SQL grouping — the grouping/no-drop guarantee
// is proven by the Postgres integration test (correlation_integration_test.go), never here.
type fakeLister struct {
	rows  []db.ListWorkUnitsRow
	count int64
	err   error
	// captured args for assertions
	gotLimit  int32
	gotOffset int32
}

func (f *fakeLister) ListWorkUnits(_ context.Context, arg db.ListWorkUnitsParams) ([]db.ListWorkUnitsRow, error) {
	f.gotLimit, f.gotOffset = arg.Limit, arg.Offset
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func (f *fakeLister) CountWorkUnits(_ context.Context) (int64, error) {
	return f.count, f.err
}

func TestListWorkUnits_DefaultsAndCaps(t *testing.T) {
	f := &fakeLister{}
	eng := NewCorrelationEngine(f)

	// limit<=0 → default 50
	_, _ = eng.ListWorkUnits(context.Background(), 0, -5)
	if f.gotLimit != 50 {
		t.Errorf("limit<=0 should default to 50, passed %d", f.gotLimit)
	}
	if f.gotOffset != 0 {
		t.Errorf("offset<0 should clamp to 0, passed %d", f.gotOffset)
	}

	// limit over cap → hard-capped at 200 (DoS guard)
	_, _ = eng.ListWorkUnits(context.Background(), 99999, 0)
	if f.gotLimit != maxWorkUnitLimit {
		t.Errorf("limit should be capped at %d, passed %d", maxWorkUnitLimit, f.gotLimit)
	}
}

func TestRowToWorkUnit_Mapping(t *testing.T) {
	row := db.ListWorkUnitsRow{
		Tenant: "personal", ProjectKey: "p1", ExternalRef: "SC-1",
		Branch: "b", Sha: "s", EventCount: 3, SessionCount: 2,
		Correlated: pgtype.Bool{Bool: true, Valid: true},
	}
	u := rowToWorkUnit(row)
	if !u.Correlated || u.ExternalRef != "SC-1" || u.EventCount != 3 || u.Tenant != "personal" {
		t.Fatalf("mapping lost fields: %+v", u)
	}
}

func TestCount(t *testing.T) {
	eng := NewCorrelationEngine(&fakeLister{count: 7})
	n, err := eng.Count(context.Background())
	if err != nil || n != 7 {
		t.Fatalf("expected 7,nil got %d,%v", n, err)
	}
}

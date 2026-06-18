package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
)

type agentVersionTestDB struct {
	agent db.Agent
	err   error
}

func (f *agentVersionTestDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *agentVersionTestDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("no resource rows in unit test")
}

func (f *agentVersionTestDB) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return agentVersionTestRow{agent: f.agent, err: f.err}
}

type agentVersionTestRow struct {
	agent db.Agent
	err   error
}

func (r agentVersionTestRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	agent := r.agent
	*(dest[0].(*pgtype.UUID)) = agent.ID
	*(dest[1].(*string)) = agent.Name
	*(dest[2].(*string)) = agent.DisplayName
	*(dest[3].(*string)) = agent.Harness
	*(dest[4].(*string)) = agent.BaseUrl
	*(dest[5].(*string)) = agent.Status
	*(dest[6].(*[]byte)) = agent.Metadata
	*(dest[7].(*pgtype.Timestamptz)) = agent.LastSeen
	*(dest[8].(*pgtype.Timestamptz)) = agent.CreatedAt
	*(dest[9].(*pgtype.Timestamptz)) = agent.UpdatedAt
	*(dest[10].(*pgtype.Text)) = agent.Role
	*(dest[11].(*pgtype.Text)) = agent.SystemPrompt
	*(dest[12].(*[]byte)) = agent.Persona
	*(dest[13].(*bool)) = agent.Visible
	*(dest[14].(*pgtype.UUID)) = agent.OwnerID
	return nil
}

type agentVersionBaseHarness struct{}

func (agentVersionBaseHarness) Name() string { return "version-test" }
func (agentVersionBaseHarness) Health(context.Context) (*harness.HealthStatus, error) {
	return &harness.HealthStatus{Status: "online"}, nil
}
func (agentVersionBaseHarness) Chat(context.Context, []harness.ChatMessage, harness.ChatOptions) (<-chan harness.ChatChunk, error) {
	return nil, harness.ErrNotSupported
}
func (agentVersionBaseHarness) ListModels(context.Context) ([]harness.ModelInfo, error) {
	return nil, harness.ErrNotSupported
}
func (agentVersionBaseHarness) Commands() []harness.Command { return nil }
func (agentVersionBaseHarness) Init(map[string]any) error   { return nil }
func (agentVersionBaseHarness) Close() error                { return nil }

type agentVersionProberHarness struct {
	agentVersionBaseHarness
	info *harness.VersionInfo
	err  error
}

func (h *agentVersionProberHarness) VersionInfo(context.Context) (*harness.VersionInfo, error) {
	return h.info, h.err
}

// testOwnerID returns a fixed owner UUID used by version tests so the handler's
// OwnerIDFromContext check succeeds without the full identity middleware.
func testOwnerID() pgtype.UUID {
	var id pgtype.UUID
	if err := id.Scan("00000000-0000-0000-0000-000000000001"); err != nil {
		panic(err)
	}
	return id
}

// withTestOwner injects a test owner identity into ctx (mirrors what the
// identity middleware does for real requests).
func withTestOwner(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownerIDKey, testOwnerID())
}

func newAgentVersionTestAPI(t *testing.T, hname string, factory func() harness.Harness) (*API, string) {
	t.Helper()
	idStr := uuid.NewString()
	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		t.Fatalf("scan uuid: %v", err)
	}
	reg := harness.NewRegistry()
	reg.Register(hname, factory)
	agent := db.Agent{
		ID:          id,
		Name:        "version-agent",
		DisplayName: "Version Agent",
		Harness:     hname,
		BaseUrl:     "http://agent.test",
		Status:      "online",
		Metadata:    []byte(`{}`),
		Persona:     []byte(`{}`),
		Visible:     true,
		OwnerID:     testOwnerID(),
	}
	return &API{queries: db.New(&agentVersionTestDB{agent: agent}), registry: reg}, idStr
}

func getAgentVersionForTest(t *testing.T, a *API, id string) harness.VersionInfo {
	t.Helper()
	rec := invokeAgentVersion(a, id, withTestOwner(context.Background()))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET version status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var info harness.VersionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return info
}

// invokeAgentVersion calls the handler directly with a chi route context carrying
// {id} and the given parent context, so tests do not depend on the route mount in
// router.go (an integrator-owned file that feature branches must not edit — the
// mount is applied at merge). Returns the recorder so callers can assert any status.
func invokeAgentVersion(a *API, id string, parent context.Context) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agents/"+id+"/version", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx := context.WithValue(parent, chi.RouteCtxKey, rctx)
	a.GetAgentVersion(rec, req.WithContext(ctx))
	return rec
}

func TestGetAgentVersionProberReturnsVersion(t *testing.T) {
	checkedAt := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	a, id := newAgentVersionTestAPI(t, "prober", func() harness.Harness {
		return &agentVersionProberHarness{info: &harness.VersionInfo{Current: "9.8.7", Source: "http", CheckedAt: checkedAt}}
	})

	info := getAgentVersionForTest(t, a, id)
	if info.Current != "9.8.7" || info.Source != "http" || !info.CheckedAt.Equal(checkedAt) {
		t.Fatalf("VersionInfo = %+v, want current 9.8.7 source http checkedAt %s", info, checkedAt)
	}
}

func TestGetAgentVersionWithoutProberReturnsUnknown(t *testing.T) {
	a, id := newAgentVersionTestAPI(t, "no-prober", func() harness.Harness {
		return agentVersionBaseHarness{}
	})

	info := getAgentVersionForTest(t, a, id)
	if info.Current != "" || info.Source != "unknown" || info.CheckedAt.IsZero() {
		t.Fatalf("VersionInfo = %+v, want unknown with CheckedAt", info)
	}
}

func TestGetAgentVersionProberErrorReturnsUnknown(t *testing.T) {
	a, id := newAgentVersionTestAPI(t, "error-prober", func() harness.Harness {
		return &agentVersionProberHarness{err: errors.New("offline")}
	})

	info := getAgentVersionForTest(t, a, id)
	if info.Current != "" || info.Source != "unknown" || info.CheckedAt.IsZero() {
		t.Fatalf("VersionInfo = %+v, want unknown with CheckedAt", info)
	}
}

// --- AC4 error-status contract (these MUST NOT collapse to 200/500) ---

func TestGetAgentVersionInvalidIDReturns400(t *testing.T) {
	a := &API{queries: db.New(&agentVersionTestDB{}), registry: harness.NewRegistry()}
	rec := invokeAgentVersion(a, "not-a-uuid", withTestOwner(context.Background()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed id; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetAgentVersionUnknownAgentReturns404(t *testing.T) {
	// DB Scan returns ErrNoRows → GetAgent fails → 404.
	a := &API{queries: db.New(&agentVersionTestDB{err: pgx.ErrNoRows}), registry: harness.NewRegistry()}
	rec := invokeAgentVersion(a, uuid.NewString(), withTestOwner(context.Background()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unknown agent; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetAgentVersionUnknownHarnessReturns400(t *testing.T) {
	idStr := uuid.NewString()
	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		t.Fatalf("scan uuid: %v", err)
	}
	// Agent resolves fine, but its harness is not registered → 400.
	agent := db.Agent{
		ID: id, Name: "ghost-agent", DisplayName: "Ghost", Harness: "ghost-harness",
		BaseUrl: "http://agent.test", Status: "online",
		Metadata: []byte(`{}`), Persona: []byte(`{}`), Visible: true,
		OwnerID: testOwnerID(),
	}
	a := &API{queries: db.New(&agentVersionTestDB{agent: agent}), registry: harness.NewRegistry()}
	rec := invokeAgentVersion(a, idStr, withTestOwner(context.Background()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown harness; body=%s", rec.Code, rec.Body.String())
	}
}

// ctxBlockingProberHarness blocks until the probe context is done, then reports
// the context error — used to prove the handler's deadline propagates to the prober.
type ctxBlockingProberHarness struct {
	agentVersionBaseHarness
}

func (ctxBlockingProberHarness) VersionInfo(ctx context.Context) (*harness.VersionInfo, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// AC5: the handler must honor its deadline. A canceled request context makes the
// handler's 10s child context immediately Done, so a ctx-respecting prober returns
// at once with source:"unknown" — proving cancellation is wired through to the probe
// (the 10s bound itself is enforced by context.WithTimeout in the handler).
func TestGetAgentVersionHonorsContextCancellation(t *testing.T) {
	a, id := newAgentVersionTestAPI(t, "blocking", func() harness.Harness {
		return ctxBlockingProberHarness{}
	})
	parent, cancel := context.WithCancel(withTestOwner(context.Background()))
	cancel()
	start := time.Now()
	rec := invokeAgentVersion(a, id, parent)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("handler took %s; deadline/cancellation not honored by the probe", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var info harness.VersionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if info.Source != "unknown" {
		t.Fatalf("VersionInfo = %+v, want source unknown on canceled probe", info)
	}
}

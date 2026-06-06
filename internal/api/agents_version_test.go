package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	}
	return &API{queries: db.New(&agentVersionTestDB{agent: agent}), registry: reg}, idStr
}

func getAgentVersionForTest(t *testing.T, a *API, id string) harness.VersionInfo {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agents/"+id+"/version", nil)
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET version status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var info harness.VersionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return info
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

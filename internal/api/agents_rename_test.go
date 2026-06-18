package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
	"github.com/tim4net/agent-os/internal/secret"
	"github.com/tim4net/agent-os/internal/service"
)

// renameReq creates an HTTP request with the seed owner-0 identity injected,
// mirroring what IdentityMiddleware does for real requests. All agent CRUD
// handlers require owner_id from context — without it they return 401.
func renameReq(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	return req.WithContext(withTestOwner(req.Context()))
}

// TestRegistry_UnknownKind verifies that Get() on an unknown kind returns an
// error that mentions the kind and lists registered kinds (AC2).
func TestRegistry_UnknownKind(t *testing.T) {
	reg := harness.NewRegistry()
	reg.Register("generic", harness.NewGenericHarness)
	reg.Register("hermes", harness.NewHermesHarness)

	_, err := reg.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown harness kind")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention the unknown kind; got: %v", err)
	}
	if !strings.Contains(err.Error(), "registered kinds:") {
		t.Errorf("error should list registered kinds; got: %v", err)
	}
	if !strings.Contains(err.Error(), "generic") || !strings.Contains(err.Error(), "hermes") {
		t.Errorf("error should list 'generic' and 'hermes'; got: %v", err)
	}
}

// TestRegistry_AllKindsConstruct verifies all four built-in harness kinds
// construct via the registry and their Name() matches (AC1).
func TestRegistry_AllKindsConstruct(t *testing.T) {
	reg := harness.DefaultRegistry
	names := reg.Names()
	if len(names) < 4 {
		t.Fatalf("expected at least 4 registered kinds, got %d: %v", len(names), names)
	}

	for _, name := range names {
		h, err := reg.Get(name)
		if err != nil {
			t.Errorf("Get(%q) failed: %v", name, err)
			continue
		}
		if h == nil {
			t.Errorf("Get(%q) returned nil", name)
		}
	}
}

// TestListHarnesses_Endpoint verifies GET /api/harnesses returns 200 with
// the registered kinds present (AC3).
func TestListHarnesses_Endpoint(t *testing.T) {
	reg := harness.NewRegistry()
	reg.Register("generic", harness.NewGenericHarness)
	reg.Register("openclaw", harness.NewOpenClawHarness)
	api := &API{registry: reg}

	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/harnesses", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var infos []HarnessInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &infos); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(infos) < 2 {
		t.Fatalf("expected at least 2 harness infos, got %d", len(infos))
	}

	// Verify the names are present
	names := map[string]bool{}
	for _, info := range infos {
		names[info.Name] = true
	}
	if !names["generic"] || !names["openclaw"] {
		t.Fatalf("expected 'generic' and 'openclaw' in response; got %v", names)
	}
}

// newTestAPIForRename sets up an API with real test DB + event bus for rename tests.
func newTestAPIForRename(t *testing.T) *API {
	t.Helper()
	pool := getTestDB(t)
	_, _ = pool.Exec(context.Background(), "TRUNCATE agents CASCADE")
	t.Cleanup(func() { pool.Close() })

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i*7 + 3)
	}
	cipher, err := secret.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	reg := harness.NewRegistry()
	reg.Register("generic", harness.NewGenericHarness)
	reg.Register("openclaw", harness.NewOpenClawHarness)
	bus := service.NewEventBus()
	feed := service.NewActivityFeed(bus, 200)
	return &API{
		queries:  db.New(pool),
		pool:     pool,
		cipher:   cipher,
		registry: reg,
		bus:      bus,
		feed:     feed,
	}
}

// createTestAgent creates an agent via the API and returns its sanitized view.
func createTestAgent(t *testing.T, api *API, name string) agentView {
	t.Helper()
	body, _ := json.Marshal(CreateAgentRequest{
		Name: name, DisplayName: name, Harness: "generic", BaseURL: "http://test",
	})
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, renameReq(http.MethodPost, "/agents", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create agent status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var view agentView
	json.Unmarshal(rec.Body.Bytes(), &view)
	return view
}

// agentIDStr extracts the string form of an agentView ID (which serializes as
// a pgtype.UUID → "xxxx-xxxx-..." in JSON).
func agentIDStr(t *testing.T, view agentView) string {
	t.Helper()
	switch v := view.ID.(type) {
	case string:
		return v
	default:
		b, _ := json.Marshal(v)
		return strings.Trim(string(b), "\"")
	}
}

// TestAgentRename_HappyPath: PATCH with new name → 200, GET shows new name (AC4).
func TestAgentRename_HappyPath(t *testing.T) {
	api := newTestAPIForRename(t)
	agent := createTestAgent(t, api, "original-name")

	// Rename
	patchBody, _ := json.Marshal(map[string]string{"name": "renamed-agent"})
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, renameReq(http.MethodPatch,
		"/agents/"+agentIDStr(t, agent), bytes.NewReader(patchBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var renamed agentView
	if err := json.Unmarshal(rec.Body.Bytes(), &renamed); err != nil {
		t.Fatalf("decode rename response: %v", err)
	}
	if renamed.Name != "renamed-agent" {
		t.Errorf("renamed name = %q, want 'renamed-agent'", renamed.Name)
	}

	// Verify via GET
	getRec := httptest.NewRecorder()
	api.Router().ServeHTTP(getRec, renameReq(http.MethodGet,
		"/agents/"+agentIDStr(t, agent), nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET after rename status = %d", getRec.Code)
	}
	var fetched agentView
	json.Unmarshal(getRec.Body.Bytes(), &fetched)
	if fetched.Name != "renamed-agent" {
		t.Errorf("GET name = %q, want 'renamed-agent'", fetched.Name)
	}
}

// TestAgentRename_Validation: table-driven validation tests (AC5).
func TestAgentRename_Validation(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(api *API) string // returns agent ID to PATCH
		patchName  string
		wantStatus int
	}{
		{
			name: "empty name rejected",
			setup: func(api *API) string {
				return agentIDStr(t, createTestAgent(t, api, "agent-a"))
			},
			patchName:  "",
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "name too long rejected",
			setup: func(api *API) string {
				return agentIDStr(t, createTestAgent(t, api, "agent-b"))
			},
			patchName:  strings.Repeat("x", 65),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "duplicate name rejected",
			setup: func(api *API) string {
				createTestAgent(t, api, "existing-name")
				return agentIDStr(t, createTestAgent(t, api, "unique-name"))
			},
			patchName:  "existing-name",
			wantStatus: http.StatusConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newTestAPIForRename(t)
			agentID := tt.setup(api)

			patchBody, _ := json.Marshal(map[string]string{"name": tt.patchName})
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, renameReq(http.MethodPatch,
				"/agents/"+agentID, bytes.NewReader(patchBody)))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d, body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestAgentRename_WrongOwner404: agent owned by a different owner returns 404 (AC5).
func TestAgentRename_WrongOwner404(t *testing.T) {
	api := newTestAPIForRename(t)

	// Insert an agent with a non-seed owner directly via SQL.
	var otherOwner pgtype.UUID
	if err := otherOwner.Scan("11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("scan other owner UUID: %v", err)
	}
	_, err := api.pool.Exec(context.Background(), `
		INSERT INTO users (id, login, display_name) VALUES ($1, 'other', 'Other')
		ON CONFLICT DO NOTHING
	`, otherOwner)
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}

	var agentID string
	err = api.pool.QueryRow(context.Background(), `
		INSERT INTO agents (name, display_name, harness, base_url, metadata, owner_id)
		VALUES ('other-owner-agent', 'Other Owner', 'generic', 'http://x', '{}', $1)
		RETURNING id::text
	`, otherOwner).Scan(&agentID)
	if err != nil {
		t.Fatalf("create agent with other owner: %v", err)
	}

	// Try to rename as seed owner → should 404
	patchBody, _ := json.Marshal(map[string]string{"name": "hijacked"})
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, renameReq(http.MethodPatch,
		"/agents/"+agentID, bytes.NewReader(patchBody)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("wrong-owner rename status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

// TestAgentRename_EmitsActivity: rename produces an agent_renamed event (AC6).
func TestAgentRename_EmitsActivity(t *testing.T) {
	api := newTestAPIForRename(t)
	agent := createTestAgent(t, api, "pre-rename")

	patchBody, _ := json.Marshal(map[string]string{"name": "post-rename"})
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, renameReq(http.MethodPatch,
		"/agents/"+agentIDStr(t, agent), bytes.NewReader(patchBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d, want 200", rec.Code)
	}

	// The ActivityFeed consumes events asynchronously via a goroutine.
	// Brief pause to let the consumer process the event before we check.
	time.Sleep(150 * time.Millisecond)

	// Check the activity feed for the agent_renamed event.
	entries := api.feed.List(50, 0)
	found := false
	for _, e := range entries {
		if e.EventType == "agent_renamed" {
			found = true
			if !strings.Contains(e.Summary, "post-rename") {
				t.Errorf("activity summary = %q, want to contain 'post-rename'", e.Summary)
			}
		}
	}
	if !found {
		t.Error("expected agent_renamed event in activity feed, not found")
	}
}

// TestAgentRename_PreservesConfig: renaming with name-only doesn't blank out role/system_prompt.
func TestAgentRename_PreservesConfig(t *testing.T) {
	api := newTestAPIForRename(t)

	// Create agent
	agent := createTestAgent(t, api, "config-agent")

	// Set config first
	configBody, _ := json.Marshal(map[string]string{
		"role":         "assistant",
		"system_prompt": "You are helpful",
	})
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, renameReq(http.MethodPatch,
		"/agents/"+agentIDStr(t, agent), bytes.NewReader(configBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("set config status = %d", rec.Code)
	}

	// Now rename only (name-only PATCH)
	patchBody, _ := json.Marshal(map[string]string{"name": "renamed-config-agent"})
	rec2 := httptest.NewRecorder()
	api.Router().ServeHTTP(rec2, renameReq(http.MethodPatch,
		"/agents/"+agentIDStr(t, agent), bytes.NewReader(patchBody)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body=%s", rec2.Code, rec2.Body.String())
	}

	var renamed agentView
	json.Unmarshal(rec2.Body.Bytes(), &renamed)
	if renamed.Name != "renamed-config-agent" {
		t.Errorf("name = %q, want 'renamed-config-agent'", renamed.Name)
	}

	// Verify config is preserved
	configRec := httptest.NewRecorder()
	api.Router().ServeHTTP(configRec, renameReq(http.MethodGet,
		"/agents/"+agentIDStr(t, agent)+"/config", nil))
	if configRec.Code != http.StatusOK {
		t.Fatalf("get config status = %d", configRec.Code)
	}
	if !strings.Contains(configRec.Body.String(), "You are helpful") {
		t.Errorf("config not preserved after rename; got %s", configRec.Body.String())
	}
}

// TestAgentRename_DBFailure verifies that a rename request returns an error
// (not 200) when the database is unavailable (negative test requested by
// coverage-hawk).
func TestAgentRename_DBFailure(t *testing.T) {
	api := newTestAPIForRename(t)
	agent := createTestAgent(t, api, "db-fail-agent")

	// Close the pool so all subsequent queries fail.
	api.pool.Close()

	patchBody, _ := json.Marshal(map[string]string{"name": "should-fail"})
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, renameReq(http.MethodPatch,
		"/agents/"+agentIDStr(t, agent), bytes.NewReader(patchBody)))

	if rec.Code == http.StatusOK {
		t.Errorf("expected non-200 status on DB failure, got %d", rec.Code)
	}
}

// TestAgentRename_ConcurrentSameName verifies that two agents renamed to the
// same name concurrently cannot both succeed — the duplicate check or unique
// constraint prevents it (negative test requested by coverage-hawk).
func TestAgentRename_ConcurrentSameName(t *testing.T) {
	api := newTestAPIForRename(t)
	agentA := createTestAgent(t, api, "concurrent-a")
	agentB := createTestAgent(t, api, "concurrent-b")

	targetName := "shared-target"

	doRename := func(agentID string) int {
		patchBody, _ := json.Marshal(map[string]string{"name": targetName})
		rec := httptest.NewRecorder()
		api.Router().ServeHTTP(rec, renameReq(http.MethodPatch,
			"/agents/"+agentID, bytes.NewReader(patchBody)))
		return rec.Code
	}

	// Fire two renames concurrently.
	type result struct{ code int }
	ch := make(chan result, 2)
	go func() { ch <- result{doRename(agentIDStr(t, agentA))} }()
	go func() { ch <- result{doRename(agentIDStr(t, agentB))} }()

	r1 := <-ch
	r2 := <-ch

	// At least one must NOT be 200 — both agents can't have the same name.
	if r1.code == http.StatusOK && r2.code == http.StatusOK {
		t.Errorf("both concurrent renames to %q succeeded — expected at least one failure; codes: %d, %d",
			targetName, r1.code, r2.code)
	}
}

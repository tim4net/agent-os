package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/harness"
	"github.com/tim4net/agent-os/internal/secret"
)

// newTestAPIForAgents builds an API wired to the real test DB + a real cipher +
// the live harness registry, and TRUNCATEs agents so assertions are isolated.
func newTestAPIForAgents(t *testing.T) *API {
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
	reg.Register("openclaw", harness.NewOpenClawHarness)
	reg.Register("generic", harness.NewGenericHarness)
	return &API{queries: db.New(pool), pool: pool, cipher: cipher, registry: reg}
}

// TestAgentAuthTokenEncryptedAtRestAndNeverLeaks proves the per-agent openclaw
// auth token is (a) NOT stored as plaintext in the DB, and (b) NOT returned in
// the create/list/get responses — only a has_auth_token boolean is exposed.
func TestAgentAuthTokenEncryptedAtRestAndNeverLeaks(t *testing.T) {
	a := newTestAPIForAgents(t)
	const token = "oc-secret-token-7777"

	body, _ := json.Marshal(CreateAgentRequest{
		Name: "my-claw", DisplayName: "My Claw", Harness: "openclaw",
		BaseURL: "http://claw:2222", AuthToken: token,
	})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/agents", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Fatalf("create response leaked auth token: %s", rec.Body.String())
	}
	// has_auth_token should be surfaced true.
	if !strings.Contains(rec.Body.String(), "\"has_auth_token\":true") {
		t.Fatalf("create response should report has_auth_token=true; got %s", rec.Body.String())
	}

	// Raw DB row must NOT contain the plaintext token.
	var meta []byte
	if err := a.pool.QueryRow(context.Background(),
		"SELECT metadata FROM agents WHERE name = $1", "my-claw").Scan(&meta); err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if strings.Contains(string(meta), token) {
		t.Fatalf("DB metadata stored plaintext token: %s", string(meta))
	}
	if !strings.Contains(string(meta), "auth_token_enc") {
		t.Fatalf("DB metadata should hold encrypted token under auth_token_enc; got %s", string(meta))
	}

	// LIST response must not leak the token.
	listRec := httptest.NewRecorder()
	a.Router().ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/agents/", nil))
	if strings.Contains(listRec.Body.String(), token) {
		t.Fatalf("list response leaked auth token: %s", listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), "auth_token_enc") {
		t.Fatalf("list response leaked raw encrypted metadata: %s", listRec.Body.String())
	}

	// But buildHarnessConfig must decrypt it back for actual use.
	ag, err := a.queries.GetAgentByName(context.Background(), "my-claw")
	if err != nil {
		t.Fatalf("GetAgentByName: %v", err)
	}
	cfg := a.buildHarnessConfig(context.Background(), ag)
	if cfg["auth_token"] != token {
		t.Fatalf("buildHarnessConfig auth_token = %v, want decrypted %q", cfg["auth_token"], token)
	}
}

// TestCreateAgentRejectsUnknownHarness proves harness validation.
func TestCreateAgentRejectsUnknownHarness(t *testing.T) {
	a := newTestAPIForAgents(t)
	body, _ := json.Marshal(CreateAgentRequest{
		Name: "x", DisplayName: "X", Harness: "not-a-harness", BaseURL: "http://x",
	})
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/agents", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown harness status = %d, want 400", rec.Code)
	}
}

// TestDeleteAgentLifecycle: 404 on missing, 204 on real delete.
func TestDeleteAgentLifecycle(t *testing.T) {
	a := newTestAPIForAgents(t)
	// missing id -> 404
	missRec := httptest.NewRecorder()
	a.Router().ServeHTTP(missRec, httptest.NewRequest(http.MethodDelete, "/agents/00000000-0000-0000-0000-000000000000", nil))
	if missRec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d, want 404", missRec.Code)
	}
	// create then delete -> 204
	body, _ := json.Marshal(CreateAgentRequest{Name: "tmp", DisplayName: "Tmp", Harness: "generic", BaseURL: "http://t"})
	cRec := httptest.NewRecorder()
	a.Router().ServeHTTP(cRec, httptest.NewRequest(http.MethodPost, "/agents", bytes.NewReader(body)))
	if cRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", cRec.Code)
	}
	var created agentView
	if err := json.Unmarshal(cRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	idStr, _ := json.Marshal(created.ID)
	id := strings.Trim(string(idStr), "\"")
	dRec := httptest.NewRecorder()
	a.Router().ServeHTTP(dRec, httptest.NewRequest(http.MethodDelete, "/agents/"+id, nil))
	if dRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204 (id=%s)", dRec.Code, id)
	}
}

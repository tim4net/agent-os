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
	req := httptest.NewRequest(http.MethodPost, "/agents", bytes.NewReader(body))
	req = req.WithContext(withTestOwner(req.Context()))
	a.Router().ServeHTTP(rec, req)
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
	reqList := httptest.NewRequest(http.MethodGet, "/agents/", nil)
	reqList = reqList.WithContext(withTestOwner(reqList.Context()))
	a.Router().ServeHTTP(listRec, reqList)
	if strings.Contains(listRec.Body.String(), token) {
		t.Fatalf("list response leaked auth token: %s", listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), "auth_token_enc") {
		t.Fatalf("list response leaked raw encrypted metadata: %s", listRec.Body.String())
	}

	// But buildHarnessConfig must decrypt it back for actual use.
	ag, err := a.queries.GetAgentByName(context.Background(), db.GetAgentByNameParams{Name: "my-claw", OwnerID: owner0UUID})
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
	reqUnk := httptest.NewRequest(http.MethodPost, "/agents", bytes.NewReader(body))
	reqUnk = reqUnk.WithContext(withTestOwner(reqUnk.Context()))
	a.Router().ServeHTTP(rec, reqUnk)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown harness status = %d, want 400", rec.Code)
	}
}

// TestDeleteAgentLifecycle: 404 on missing, 204 on real delete.
func TestDeleteAgentLifecycle(t *testing.T) {
	a := newTestAPIForAgents(t)
	// missing id -> 404
	missRec := httptest.NewRecorder()
	reqMiss := httptest.NewRequest(http.MethodDelete, "/agents/00000000-0000-0000-0000-000000000000", nil)
	reqMiss = reqMiss.WithContext(withTestOwner(reqMiss.Context()))
	a.Router().ServeHTTP(missRec, reqMiss)
	if missRec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d, want 404", missRec.Code)
	}
	// create then delete -> 204
	body, _ := json.Marshal(CreateAgentRequest{Name: "tmp", DisplayName: "Tmp", Harness: "generic", BaseURL: "http://t"})
	cRec := httptest.NewRecorder()
	reqCr := httptest.NewRequest(http.MethodPost, "/agents", bytes.NewReader(body))
	reqCr = reqCr.WithContext(withTestOwner(reqCr.Context()))
	a.Router().ServeHTTP(cRec, reqCr)
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
	reqDel := httptest.NewRequest(http.MethodDelete, "/agents/"+id, nil)
	reqDel = reqDel.WithContext(withTestOwner(reqDel.Context()))
	a.Router().ServeHTTP(dRec, reqDel)
	if dRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204 (id=%s)", dRec.Code, id)
	}
}

// TestCreateAgentDefaultsDisplayNameToName verifies that when display_name is
// omitted the handler falls back to the agent's name rather than rejecting the
// request (#121). It also confirms an explicit display_name is preserved.
func TestCreateAgentDefaultsDisplayNameToName(t *testing.T) {
	a := newTestAPIForAgents(t)

	// 1. Omitted display_name -> defaults to name.
	body, _ := json.Marshal(CreateAgentRequest{
		Name: "fallback-bot", Harness: "generic", BaseURL: "http://fb:8080",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/agents", bytes.NewReader(body))
	req = req.WithContext(withTestOwner(req.Context()))
	a.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create (no display_name) status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got agentView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if got.DisplayName != "fallback-bot" {
		t.Fatalf("display_name = %q, want %q (fallback to name)", got.DisplayName, "fallback-bot")
	}

	// 2. Explicit display_name -> preserved verbatim.
	body2, _ := json.Marshal(CreateAgentRequest{
		Name: "named-bot", DisplayName: "Fancy Bot", Harness: "generic", BaseURL: "http://nb:8080",
	})
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/agents", bytes.NewReader(body2))
	req2 = req2.WithContext(withTestOwner(req2.Context()))
	a.Router().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("create (explicit display_name) status = %d, body=%s", rec2.Code, rec2.Body.String())
	}
	var got2 agentView
	if err := json.Unmarshal(rec2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if got2.DisplayName != "Fancy Bot" {
		t.Fatalf("display_name = %q, want %q", got2.DisplayName, "Fancy Bot")
	}
}

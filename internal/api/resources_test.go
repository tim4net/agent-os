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

func newTestAPIForVault(t *testing.T) *API {
	t.Helper()
	pool := getTestDB(t)
	_, _ = pool.Exec(context.Background(), "TRUNCATE agent_grants, resources, agents CASCADE")
	t.Cleanup(func() { pool.Close() })

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i*5 + 11)
	}
	cipher, err := secret.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	reg := harness.NewRegistry()
	reg.Register("hermes", harness.NewHermesHarness)
	reg.Register("openclaw", harness.NewOpenClawHarness)
	reg.Register("generic", harness.NewGenericHarness)
	return &API{queries: db.New(pool), pool: pool, cipher: cipher, registry: reg}
}

func doJSON(t *testing.T, a *API, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, r)
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	return rec
}

// TestMultipleCredsPerProvider proves the slug model allows many creds for one provider.
func TestMultipleCredsPerProvider(t *testing.T) {
	a := newTestAPIForVault(t)
	for _, slug := range []string{"openrouter-personal", "openrouter-work", "openrouter-cheap"} {
		rec := doJSON(t, a, http.MethodPost, "/resources", CreateResourceRequest{
			Slug: slug, Kind: "credential", Provider: "openrouter", Label: slug, Secret: "sk-or-" + slug,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: status %d body %s", slug, rec.Code, rec.Body.String())
		}
	}
	// duplicate slug rejected
	dup := doJSON(t, a, http.MethodPost, "/resources", CreateResourceRequest{
		Slug: "openrouter-personal", Kind: "credential", Provider: "openrouter", Secret: "x",
	})
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate slug status = %d, want 409", dup.Code)
	}
	// list shows all three, same provider, none leaking plaintext
	list := doJSON(t, a, http.MethodGet, "/resources?kind=credential", nil)
	if strings.Contains(list.Body.String(), "sk-or-") {
		t.Fatalf("list leaked plaintext secret: %s", list.Body.String())
	}
	var out struct {
		Resources []resourceView `json:"resources"`
	}
	json.Unmarshal(list.Body.Bytes(), &out)
	n := 0
	for _, r := range out.Resources {
		if r.Provider == "openrouter" {
			n++
			if r.Last4 == "" || !r.IsSet {
				t.Errorf("%s should be set with last4", r.Slug)
			}
		}
	}
	if n != 3 {
		t.Fatalf("expected 3 openrouter creds, got %d", n)
	}
}

// TestGrantRevokeDefaultDeny proves: an agent gets a resource's secret ONLY when
// granted, and revoking removes it — verified through buildHarnessConfig.
func TestGrantRevokeDefaultDeny(t *testing.T) {
	a := newTestAPIForVault(t)

	// seed an agent + a credential resource
	ag, err := a.queries.CreateAgent(context.Background(), db.CreateAgentParams{
		Name: "scout", DisplayName: "Scout", Harness: "hermes", BaseUrl: "http://scout", Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	rec := doJSON(t, a, http.MethodPost, "/resources", CreateResourceRequest{
		Slug: "anthropic-main", Kind: "credential", Provider: "anthropic", Secret: "sk-ant-GRANTME",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create resource: %d %s", rec.Code, rec.Body.String())
	}
	var res resourceView
	json.Unmarshal(rec.Body.Bytes(), &res)
	resID := strings.Trim(mustJSON(res.ID), "\"")
	agID := strings.Trim(mustJSON(ag.ID), "\"")

	// DEFAULT-DENY: before any grant, buildHarnessConfig has no api_key
	cfg := a.buildHarnessConfig(context.Background(), ag)
	if _, ok := cfg["api_key"]; ok {
		t.Fatal("default-deny violated: agent got api_key with NO grant")
	}

	// GRANT
	g := doJSON(t, a, http.MethodPut, "/agents/"+agID+"/grants/"+resID, nil)
	if g.Code != http.StatusOK {
		t.Fatalf("grant status = %d body %s", g.Code, g.Body.String())
	}
	cfg = a.buildHarnessConfig(context.Background(), ag)
	if cfg["api_key"] != "sk-ant-GRANTME" {
		t.Fatalf("after grant api_key = %v, want decrypted secret", cfg["api_key"])
	}

	// the per-agent grants list shows the resource, masked
	gl := doJSON(t, a, http.MethodGet, "/agents/"+agID+"/grants", nil)
	if strings.Contains(gl.Body.String(), "GRANTME") {
		t.Fatalf("agent grants list leaked plaintext: %s", gl.Body.String())
	}

	// REVOKE
	rv := doJSON(t, a, http.MethodDelete, "/agents/"+agID+"/grants/"+resID, nil)
	if rv.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d", rv.Code)
	}
	cfg = a.buildHarnessConfig(context.Background(), ag)
	if _, ok := cfg["api_key"]; ok {
		t.Fatal("after revoke the api_key should be gone (capability removed)")
	}
}

// TestResourceSecretEncryptedAtRest proves the DB row holds ciphertext, not plaintext.
func TestResourceSecretEncryptedAtRest(t *testing.T) {
	a := newTestAPIForVault(t)
	const sk = "sk-secret-at-rest-1234"
	rec := doJSON(t, a, http.MethodPost, "/resources", CreateResourceRequest{
		Slug: "anthropic-x", Kind: "credential", Provider: "anthropic", Secret: sk,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var encValue []byte
	var last4 string
	if err := a.pool.QueryRow(context.Background(),
		"SELECT enc_value, last4 FROM resources WHERE slug=$1", "anthropic-x").Scan(&encValue, &last4); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if bytes.Contains(encValue, []byte(sk)) {
		t.Fatal("enc_value contains plaintext — not encrypted")
	}
	if last4 != "1234" {
		t.Fatalf("last4 = %q, want 1234", last4)
	}
}

// TestCreateResourceRejectsBadSlug enforces the slug format.
func TestCreateResourceRejectsBadSlug(t *testing.T) {
	a := newTestAPIForVault(t)
	for _, bad := range []string{"", "Has Space", "UPPER", "bad_underscore", "-leading"} {
		rec := doJSON(t, a, http.MethodPost, "/resources", CreateResourceRequest{
			Slug: bad, Kind: "credential", Secret: "x",
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("slug %q status = %d, want 400", bad, rec.Code)
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

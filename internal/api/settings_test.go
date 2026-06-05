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
	"github.com/tim4net/agent-os/internal/secret"
)

// newTestAPIForSettings builds an API wired to the real test DB plus a real
// cipher, and TRUNCATEs app_settings so prior rows can't pollute assertions.
func newTestAPIForSettings(t *testing.T) *API {
	t.Helper()
	pool := getTestDB(t)
	_, _ = pool.Exec(context.Background(), "TRUNCATE app_settings")
	t.Cleanup(func() { pool.Close() })

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	cipher, err := secret.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return &API{queries: db.New(pool), pool: pool, cipher: cipher}
}

// putSetting drives PUT /api/settings/{key} through the real router.
func putSetting(t *testing.T, a *API, key, value string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(UpdateSettingRequest{Value: value})
	req := httptest.NewRequest(http.MethodPut, "/settings/"+key, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	return rec
}

// TestSecretNeverLeaksInAnyResponse is the core security invariant: a stored
// secret's plaintext must NOT appear in the PUT response or the GET list.
func TestSecretNeverLeaksInAnyResponse(t *testing.T) {
	a := newTestAPIForSettings(t)
	const plaintext = "sk-ant-SUPERSECRET-zzz9999"

	put := putSetting(t, a, "anthropic_api_key", plaintext)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", put.Code, put.Body.String())
	}
	if strings.Contains(put.Body.String(), plaintext) {
		t.Fatalf("PUT response leaked plaintext secret: %s", put.Body.String())
	}
	if strings.Contains(put.Body.String(), "9999") == false {
		t.Fatalf("PUT response should expose last4 (9999); got %s", put.Body.String())
	}

	// GET list must not contain plaintext either, but must report is_set+last4.
	getReq := httptest.NewRequest(http.MethodGet, "/settings/", nil)
	getRec := httptest.NewRecorder()
	a.Router().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", getRec.Code)
	}
	if strings.Contains(getRec.Body.String(), plaintext) {
		t.Fatalf("GET /settings leaked plaintext secret: %s", getRec.Body.String())
	}

	var out struct {
		Settings []SettingView `json:"settings"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var found bool
	for _, s := range out.Settings {
		if s.Key == "anthropic_api_key" {
			found = true
			if !s.IsSet {
				t.Error("anthropic_api_key should be is_set")
			}
			if s.Last4 != "9999" {
				t.Errorf("last4 = %q, want 9999", s.Last4)
			}
			if s.Value != "" {
				t.Errorf("secret Value field must be empty in list view, got %q", s.Value)
			}
			if s.Source != "stored" {
				t.Errorf("source = %q, want stored", s.Source)
			}
		}
	}
	if !found {
		t.Fatal("anthropic_api_key not present in settings list")
	}
}

// TestSecretResolvesToPlaintextInternally proves the encrypted value round-trips
// for INTERNAL use (resolveSecret) even though it never serializes outward.
func TestSecretResolvesToPlaintextInternally(t *testing.T) {
	a := newTestAPIForSettings(t)
	const plaintext = "hermes-token-abcd1234"
	if rec := putSetting(t, a, "hermes_api_key", plaintext); rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", rec.Code)
	}
	got := a.resolveSecret(context.Background(), "hermes_api_key")
	if got != plaintext {
		t.Fatalf("resolveSecret = %q, want %q (decrypt round-trip broken)", got, plaintext)
	}
}

// TestDeleteSettingRevertsToUnset confirms DELETE clears a stored value.
func TestDeleteSettingRevertsToUnset(t *testing.T) {
	a := newTestAPIForSettings(t)
	if rec := putSetting(t, a, "openai_api_key", "sk-openai-xyz0001"); rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", rec.Code)
	}
	delReq := httptest.NewRequest(http.MethodDelete, "/settings/openai_api_key", nil)
	delRec := httptest.NewRecorder()
	a.Router().ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", delRec.Code)
	}
	// After delete with no env fallback, resolveSecret is empty.
	if got := a.resolveSecret(context.Background(), "openai_api_key"); got != "" {
		t.Fatalf("after delete resolveSecret = %q, want empty", got)
	}
}

// TestUnknownSettingKeyRejected ensures only catalog keys are writable.
func TestUnknownSettingKeyRejected(t *testing.T) {
	a := newTestAPIForSettings(t)
	if rec := putSetting(t, a, "totally_made_up_key", "x"); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown key status = %d, want 400", rec.Code)
	}
}

// TestEmptySecretRejected ensures clearing is via DELETE, not empty PUT.
func TestEmptySecretRejected(t *testing.T) {
	a := newTestAPIForSettings(t)
	if rec := putSetting(t, a, "anthropic_api_key", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT empty secret status = %d, want 400", rec.Code)
	}
}

// TestSecretStorageDisabledWithoutCipher proves we REFUSE to store a secret
// rather than persisting plaintext when no master key is configured.
func TestSecretStorageDisabledWithoutCipher(t *testing.T) {
	pool := getTestDB(t)
	_, _ = pool.Exec(context.Background(), "TRUNCATE app_settings")
	t.Cleanup(func() { pool.Close() })
	a := &API{queries: db.New(pool), pool: pool, cipher: nil} // no cipher

	rec := putSetting(t, a, "anthropic_api_key", "sk-should-not-store")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("PUT secret without cipher status = %d, want 503", rec.Code)
	}
	// Nothing should be persisted.
	if _, err := a.queries.GetSetting(context.Background(), "anthropic_api_key"); err == nil {
		t.Fatal("secret was persisted despite disabled encryption — plaintext-leak risk")
	}
}

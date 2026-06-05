package api

import (
	"encoding/base64"
	"encoding/json"

	"github.com/tim4net/agent-os/internal/db"
)

// agentView is the safe-to-serialize projection of a db.Agent. It deliberately
// OMITS the raw Metadata/Persona JSONB blobs, which can carry per-agent secret
// material (e.g. an encrypted openclaw auth token). Every agent HTTP response
// goes through sanitizeAgent so secret material never leaves the server.
type agentView struct {
	ID           any    `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Harness      string `json:"harness"`
	BaseURL      string `json:"base_url"`
	Status       string `json:"status"`
	HasAuthToken bool   `json:"has_auth_token"` // surfaces presence without revealing the token
	LastSeen     any    `json:"last_seen"`
	CreatedAt    any    `json:"created_at"`
	UpdatedAt    any    `json:"updated_at"`
	Role         any    `json:"role"`
	SystemPrompt any    `json:"system_prompt"`
	Visible      bool   `json:"visible"`
}

// sanitizeAgent projects a db.Agent into an agentView, stripping secret-bearing
// JSONB and reporting only whether an auth token is configured.
func sanitizeAgent(a db.Agent) agentView {
	return agentView{
		ID:           a.ID,
		Name:         a.Name,
		DisplayName:  a.DisplayName,
		Harness:      a.Harness,
		BaseURL:      a.BaseUrl,
		Status:       a.Status,
		HasAuthToken: metadataHasAuthToken(a.Metadata),
		LastSeen:     a.LastSeen,
		CreatedAt:    a.CreatedAt,
		UpdatedAt:    a.UpdatedAt,
		Role:         a.Role,
		SystemPrompt: a.SystemPrompt,
		Visible:      a.Visible,
	}
}

func sanitizeAgents(in []db.Agent) []agentView {
	out := make([]agentView, 0, len(in))
	for _, a := range in {
		out = append(out, sanitizeAgent(a))
	}
	return out
}

// metadataHasAuthToken reports whether agent metadata carries an (encrypted or
// legacy plaintext) auth token, without exposing its value.
func metadataHasAuthToken(meta []byte) bool {
	if len(meta) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		return false
	}
	if v, ok := m["auth_token_enc"].(string); ok && v != "" {
		return true
	}
	if v, ok := m["auth_token"].(string); ok && v != "" {
		return true
	}
	return false
}

// encodeAuthTokenMetadata returns JSONB metadata with the auth token ENCRYPTED
// at rest (base64 of AES-GCM ciphertext under "auth_token_enc"). Returns ok=false
// if a token was supplied but no cipher is available, so the caller can refuse
// rather than persist plaintext.
func (a *API) encodeAuthTokenMetadata(token string) (meta []byte, ok bool) {
	if token == "" {
		return []byte("{}"), true
	}
	if !a.cipher.Enabled() {
		return nil, false
	}
	ct, err := a.cipher.Encrypt(token)
	if err != nil {
		return nil, false
	}
	b, err := json.Marshal(map[string]string{
		"auth_token_enc": base64.StdEncoding.EncodeToString(ct),
	})
	if err != nil {
		return nil, false
	}
	return b, true
}

// decodeAuthToken extracts the plaintext auth token from agent metadata,
// transparently handling both the encrypted form ("auth_token_enc") and the
// legacy plaintext form ("auth_token") for backward compatibility.
func (a *API) decodeAuthToken(meta []byte) string {
	if len(meta) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		return ""
	}
	if enc, ok := m["auth_token_enc"].(string); ok && enc != "" {
		if a.cipher.Enabled() {
			if raw, err := base64.StdEncoding.DecodeString(enc); err == nil {
				if pt, derr := a.cipher.Decrypt(raw); derr == nil {
					return pt
				}
			}
		}
		return ""
	}
	// Legacy plaintext token (pre-encryption rows).
	if tok, ok := m["auth_token"].(string); ok {
		return tok
	}
	return ""
}

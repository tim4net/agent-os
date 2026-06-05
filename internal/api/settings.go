package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/secret"
)

// SettingDef describes a single known app setting for the Settings UI.
// The catalog is server-defined so the frontend never hard-codes provider
// lists or has to know which keys are secret.
type SettingDef struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Group    string `json:"group"` // "Providers" | "General"
	IsSecret bool   `json:"is_secret"`
	Help     string `json:"help,omitempty"`
	EnvVar   string `json:"env_var,omitempty"` // env fallback that backs this key
}

// settingsCatalog is the authoritative list of configurable settings.
// Provider API keys map 1:1 to the env vars in internal/config; a value set
// here is stored encrypted and OVERRIDES the env fallback at request time.
var settingsCatalog = []SettingDef{
	{Key: "anthropic_api_key", Label: "Anthropic API Key", Group: "Providers", IsSecret: true, EnvVar: "ANTHROPIC_API_KEY", Help: "Claude (Anthropic) — subscription/metered."},
	{Key: "openai_api_key", Label: "OpenAI API Key", Group: "Providers", IsSecret: true, EnvVar: "OPENAI_API_KEY", Help: "GPT (OpenAI) — metered."},
	{Key: "gemini_api_key", Label: "Google Gemini API Key", Group: "Providers", IsSecret: true, EnvVar: "GEMINI_API_KEY", Help: "Gemini / Antigravity (Google)."},
	{Key: "openrouter_api_key", Label: "OpenRouter API Key", Group: "Providers", IsSecret: true, EnvVar: "OPENROUTER_API_KEY", Help: "Multi-provider router."},
	{Key: "xai_api_key", Label: "xAI API Key", Group: "Providers", IsSecret: true, EnvVar: "XAI_API_KEY", Help: "Grok (xAI)."},
	{Key: "zai_api_key", Label: "Z.AI API Key", Group: "Providers", IsSecret: true, EnvVar: "ZAI_API_KEY", Help: "Z.AI (GLM)."},
	{Key: "fal_key", Label: "FAL Key", Group: "Providers", IsSecret: true, EnvVar: "FAL_KEY", Help: "FAL image/video generation."},
	{Key: "hermes_api_key", Label: "Hermes API Key", Group: "Providers", IsSecret: true, EnvVar: "HERMES_API_KEY", Help: "Auth token for Hermes-harness agents (e.g. Roux)."},
	{Key: "default_llm_model", Label: "Default LLM Model", Group: "General", IsSecret: false, EnvVar: "LLM_MODEL", Help: "Model id used for server-side LLM tasks."},
}

// settingDefByKey indexes the catalog for O(1) validation.
var settingDefByKey = func() map[string]SettingDef {
	m := make(map[string]SettingDef, len(settingsCatalog))
	for _, d := range settingsCatalog {
		m[d.Key] = d
	}
	return m
}()

// SettingView is the masked, safe-to-serialize view of a setting.
// For secrets it NEVER includes plaintext — only is_set + last4.
type SettingView struct {
	SettingDef
	IsSet     bool   `json:"is_set"`
	Last4     string `json:"last4,omitempty"`
	Value     string `json:"value,omitempty"` // non-secret plaintext only
	Source    string `json:"source"`          // "stored" | "env" | "unset"
	UpdatedAt string `json:"updated_at,omitempty"`
}

// ListSettings handles GET /api/settings — returns the catalog with masked state.
// Secrets are reported as is_set + last4; plaintext is never returned.
func (a *API) ListSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := a.queries.ListSettings(r.Context())
	if err != nil {
		http.Error(w, "failed to list settings", http.StatusInternalServerError)
		return
	}
	stored := make(map[string]db.AppSetting, len(rows))
	for _, s := range rows {
		stored[s.Key] = s
	}

	views := make([]SettingView, 0, len(settingsCatalog))
	for _, def := range settingsCatalog {
		v := SettingView{SettingDef: def, Source: "unset"}
		if s, ok := stored[def.Key]; ok {
			v.IsSet = true
			v.Source = "stored"
			v.UpdatedAt = s.UpdatedAt.Time.Format("2006-01-02T15:04:05Z07:00")
			if def.IsSecret {
				v.Last4 = s.Last4
			} else {
				v.Value = s.Value
			}
		} else if def.EnvVar != "" && a.envFallback(def.Key) != "" {
			// Backed by an env var (legacy / deploy-injected) but not stored.
			v.IsSet = true
			v.Source = "env"
			if def.IsSecret {
				v.Last4 = secret.Last4(a.envFallback(def.Key))
			} else {
				v.Value = a.envFallback(def.Key)
			}
		}
		views = append(views, v)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"settings":        views,
		"secrets_enabled": a.cipher.Enabled(),
	})
}

// UpdateSettingRequest is the body for PUT /api/settings/{key}.
type UpdateSettingRequest struct {
	Value string `json:"value"`
}

// UpdateSetting handles PUT /api/settings/{key}. Secrets are encrypted at rest;
// if encryption is unavailable the request is refused (never stored plaintext).
func (a *API) UpdateSetting(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	def, ok := settingDefByKey[key]
	if !ok {
		http.Error(w, "unknown setting key", http.StatusBadRequest)
		return
	}

	var req UpdateSettingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	params := db.UpsertSettingParams{Key: key, IsSecret: def.IsSecret}
	if def.IsSecret {
		if req.Value == "" {
			http.Error(w, "secret value cannot be empty (use DELETE to clear)", http.StatusBadRequest)
			return
		}
		if !a.cipher.Enabled() {
			http.Error(w, "secret storage is disabled: set AOS_MASTER_KEY on the server to enable encrypted secrets", http.StatusServiceUnavailable)
			return
		}
		enc, err := a.cipher.Encrypt(req.Value)
		if err != nil {
			http.Error(w, "failed to encrypt secret", http.StatusInternalServerError)
			return
		}
		params.EncValue = enc
		params.Last4 = secret.Last4(req.Value)
		// value column stays empty for secrets.
	} else {
		params.Value = req.Value
	}

	row, err := a.queries.UpsertSetting(r.Context(), params)
	if err != nil {
		http.Error(w, "failed to save setting", http.StatusInternalServerError)
		return
	}

	// Return the masked view, never plaintext.
	v := SettingView{SettingDef: def, IsSet: true, Source: "stored",
		UpdatedAt: row.UpdatedAt.Time.Format("2006-01-02T15:04:05Z07:00")}
	if def.IsSecret {
		v.Last4 = row.Last4
	} else {
		v.Value = row.Value
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// DeleteSetting handles DELETE /api/settings/{key} — clears a stored value,
// reverting to the env fallback (if any).
func (a *API) DeleteSetting(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if _, ok := settingDefByKey[key]; !ok {
		http.Error(w, "unknown setting key", http.StatusBadRequest)
		return
	}
	if err := a.queries.DeleteSetting(r.Context(), key); err != nil {
		http.Error(w, "failed to delete setting", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// envFallback returns the process-env value backing a catalog key, if any.
func (a *API) envFallback(key string) string {
	switch key {
	case "anthropic_api_key":
		return a.anthropicAPIKey
	case "openai_api_key":
		return a.openaiAPIKey
	case "gemini_api_key":
		return a.geminiAPIKey
	case "openrouter_api_key":
		return a.openrouterAPIKey
	case "xai_api_key":
		return a.xaiAPIKey
	case "zai_api_key":
		return a.zaiAPIKey
	case "fal_key":
		return a.falKey
	case "hermes_api_key":
		return a.hermesAPIKey
	case "default_llm_model":
		return a.llmModel
	}
	return ""
}

// resolveSecret returns the effective plaintext for a catalog key:
// a stored (encrypted) value if present, otherwise the env fallback.
// Returns "" when unset or when a stored secret can't be decrypted.
func (a *API) resolveSecret(ctx context.Context, key string) string {
	row, err := a.queries.GetSetting(ctx, key)
	if err == nil {
		if row.IsSecret {
			if len(row.EncValue) > 0 && a.cipher.Enabled() {
				if pt, derr := a.cipher.Decrypt(row.EncValue); derr == nil {
					return pt
				}
			}
		} else if row.Value != "" {
			return row.Value
		}
	}
	// Not found / decrypt failure / lookup error all fall back to env — a
	// settings lookup must never wedge a chat or health call.
	return a.envFallback(key)
}

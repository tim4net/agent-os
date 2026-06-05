package api

import (
	"encoding/json"
	"net/http"
	"sort"
)

// HarnessInfo describes a registered harness type for the Settings UI's
// "Add Agent" form. RequiresAuthToken hints the UI to show the token field.
type HarnessInfo struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	RequiresAuthToken bool   `json:"requires_auth_token"`
}

// harnessMeta carries UI hints for known harness types. Names not present here
// still appear (from the registry) with a generic description.
var harnessMeta = map[string]HarnessInfo{
	"generic":  {Description: "HTTP health-check only (GET /health). No chat.", RequiresAuthToken: false},
	"hermes":   {Description: "Hermes agent (chat, models, slash-commands). Uses the Hermes API key.", RequiresAuthToken: false},
	"openclaw": {Description: "OpenClaw agent over WebSocket. Supports a per-agent auth token.", RequiresAuthToken: true},
	"litellm":  {Description: "LiteLLM proxy — model gateway, infrastructure target.", RequiresAuthToken: false},
}

// ListHarnesses handles GET /api/harnesses — the registered harness types,
// derived from the live registry (single source of truth), enriched with UI meta.
func (a *API) ListHarnesses(w http.ResponseWriter, r *http.Request) {
	names := a.registry.Names()
	sort.Strings(names)

	out := make([]HarnessInfo, 0, len(names))
	for _, n := range names {
		info := HarnessInfo{Name: n, Description: "Custom harness."}
		if meta, ok := harnessMeta[n]; ok {
			info.Description = meta.Description
			info.RequiresAuthToken = meta.RequiresAuthToken
		}
		out = append(out, info)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/secret"
)

// slugRe validates resource slugs: lowercase alphanumerics + hyphens, 1..64 chars.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

var validKinds = map[string]bool{"credential": true, "integration": true, "mcp_server": true}

// resourceView is the masked, safe-to-serialize projection of a vault resource.
// It NEVER includes enc_value/enc_config (ciphertext) or any plaintext secret —
// only is_set + last4 for secrets, plus non-secret config.
type resourceView struct {
	ID        any             `json:"id"`
	Slug      string          `json:"slug"`
	Kind      string          `json:"kind"`
	Label     string          `json:"label"`
	Provider  string          `json:"provider"`
	IsSecret  bool            `json:"is_secret"`
	IsSet     bool            `json:"is_set"`
	Last4     string          `json:"last4,omitempty"`
	Config    json.RawMessage `json:"config"`
	Status    string          `json:"status"`
	CreatedAt any             `json:"created_at"`
	UpdatedAt any             `json:"updated_at"`
}

func toResourceView(r db.Resource) resourceView {
	cfg := json.RawMessage(r.Config)
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	return resourceView{
		ID:        r.ID,
		Slug:      r.Slug,
		Kind:      r.Kind,
		Label:     r.Label,
		Provider:  r.Provider,
		IsSecret:  r.IsSecret,
		IsSet:     resourceIsSet(r),
		Last4:     r.Last4,
		Config:    cfg,
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// resourceIsSet reports whether a resource is "configured". A SECRET resource is
// set only when it actually holds ciphertext; a non-secret resource (e.g. an
// MCP server defined purely by config) is always considered set.
func resourceIsSet(r db.Resource) bool {
	if r.IsSecret {
		return len(r.EncValue) > 0
	}
	return true
}

func toResourceViews(in []db.Resource) []resourceView {
	out := make([]resourceView, 0, len(in))
	for _, r := range in {
		out = append(out, toResourceView(r))
	}
	return out
}

// ListResources handles GET /api/resources?kind= — masked vault list.
func (a *API) ListResources(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	var (
		rows []db.Resource
		err  error
	)
	if kind != "" {
		rows, err = a.queries.ListResourcesByKind(r.Context(), kind)
	} else {
		rows, err = a.queries.ListResources(r.Context())
	}
	if err != nil {
		http.Error(w, "failed to list resources", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resources":       toResourceViews(rows),
		"secrets_enabled": a.envelope.Enabled(),
	})
}

// CreateResourceRequest is the body for POST /api/resources.
type CreateResourceRequest struct {
	Slug     string          `json:"slug"`
	Kind     string          `json:"kind"`
	Label    string          `json:"label"`
	Provider string          `json:"provider"`
	Secret   string          `json:"secret"`           // plaintext secret material (encrypted server-side)
	Config   json.RawMessage `json:"config,omitempty"` // non-secret config
}

// CreateResource handles POST /api/resources. Secret material is encrypted at
// rest; if a secret is supplied without an active cipher the request is refused.
func (a *API) CreateResource(w http.ResponseWriter, r *http.Request) {
	var req CreateResourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	if !slugRe.MatchString(req.Slug) {
		http.Error(w, "slug must be lowercase alphanumerics and hyphens (1-64 chars), e.g. openrouter-personal", http.StatusBadRequest)
		return
	}
	if !validKinds[req.Kind] {
		http.Error(w, "kind must be credential, integration, or mcp_server", http.StatusBadRequest)
		return
	}
	if req.Label == "" {
		req.Label = req.Slug
	}

	isSecret := req.Secret != ""
	var encValue []byte
	var last4 string
	ownerID := [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	if isSecret {
		if !a.envelope.Enabled() {
			http.Error(w, "secret storage is disabled: set AOS_MASTER_KEY on the server", http.StatusServiceUnavailable)
			return
		}
		ct, err := a.envelope.EncryptForOwner(r.Context(), ownerID, req.Secret)
		if err != nil {
			http.Error(w, "failed to encrypt secret", http.StatusInternalServerError)
			return
		}
		encValue = ct
		last4 = secret.Last4(req.Secret)
	}

	config := []byte("{}")
	if len(req.Config) > 0 {
		config = req.Config
	}
	status := "unset"
	if isSecret || req.Kind != "credential" {
		status = "active"
	}

	res, err := a.queries.CreateResource(r.Context(), db.CreateResourceParams{
		Slug:     req.Slug,
		Kind:     req.Kind,
		Label:    req.Label,
		Provider: req.Provider,
		IsSecret: isSecret,
		EncValue: encValue,
		Config:        config,
		Last4:         last4,
		Status:        status,
		EncKeyVersion: 1,
	})
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			http.Error(w, "a resource with that slug already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create resource: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, toResourceView(res))
}

// UpdateResourceRequest is the body for PUT /api/resources/{id}. A non-empty
// Secret rotates the stored secret; omit it to leave the secret unchanged.
type UpdateResourceRequest struct {
	Label    *string         `json:"label,omitempty"`
	Provider *string         `json:"provider,omitempty"`
	Secret   *string         `json:"secret,omitempty"`
	Config   json.RawMessage `json:"config,omitempty"`
}

// UpdateResource handles PUT /api/resources/{id}.
func (a *API) UpdateResource(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	cur, err := a.queries.GetResource(r.Context(), id)
	if err != nil {
		http.Error(w, "resource not found", http.StatusNotFound)
		return
	}

	var req UpdateResourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	label := cur.Label
	if req.Label != nil {
		label = *req.Label
	}
	provider := cur.Provider
	if req.Provider != nil {
		provider = *req.Provider
	}
	config := cur.Config
	if len(req.Config) > 0 {
		config = req.Config
	}
	isSecret := cur.IsSecret
	encValue := cur.EncValue
	last4 := cur.Last4
	status := cur.Status
	if req.Secret != nil {
		if *req.Secret == "" {
			// explicit clear
			isSecret = false
			encValue = nil
			last4 = ""
			status = "unset"
		} else {
			if !a.envelope.Enabled() {
				http.Error(w, "secret storage is disabled: set AOS_MASTER_KEY on the server", http.StatusServiceUnavailable)
				return
			}
			ct, encErr := a.envelope.EncryptForOwner(r.Context(), cur.OwnerID.Bytes, *req.Secret)
			if encErr != nil {
				http.Error(w, "failed to encrypt secret", http.StatusInternalServerError)
				return
			}
			isSecret = true
			encValue = ct
			last4 = secret.Last4(*req.Secret)
			status = "active"
		}
	}

	res, err := a.queries.UpdateResource(r.Context(), db.UpdateResourceParams{
		ID:        id,
		Label:     label,
		Provider:  provider,
		IsSecret:  isSecret,
		EncValue:  encValue,
		EncConfig: cur.EncConfig,
		Config:        config,
		Last4:         last4,
		Status:        status,
		EncKeyVersion: 1,
	})
	if err != nil {
		http.Error(w, "failed to update resource: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, toResourceView(res))
}

// DeleteResource handles DELETE /api/resources/{id}. Grants cascade-delete.
func (a *API) DeleteResource(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	if _, err := a.queries.GetResource(r.Context(), id); err != nil {
		http.Error(w, "resource not found", http.StatusNotFound)
		return
	}
	if err := a.queries.DeleteResource(r.Context(), id); err != nil {
		http.Error(w, "failed to delete resource: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveResourceSecret returns the decrypted secret string for a resource, or
// "" if unset / undecryptable. Internal use only (never serialized).
func (a *API) resolveResourceSecret(ctx context.Context, res db.Resource) string {
	if len(res.EncValue) == 0 || !a.envelope.Enabled() {
		return ""
	}
	if !res.OwnerID.Valid {
		return ""
	}
	pt, err := a.envelope.DecryptForOwner(ctx, res.OwnerID.Bytes, res.EncValue)
	if err != nil {
		return ""
	}
	return pt
}

// --- grants ---------------------------------------------------------------

type grantView struct {
	AgentID    any    `json:"agent_id"`
	ResourceID any    `json:"resource_id"`
	Scope      string `json:"scope"`
	GrantedAt  any    `json:"granted_at"`
}

func toGrantView(g db.AgentGrant) grantView {
	return grantView{AgentID: g.AgentID, ResourceID: g.ResourceID, Scope: g.Scope, GrantedAt: g.GrantedAt}
}

// ListAllGrants handles GET /api/grants — every (agent,resource) edge, for the
// permission matrix. Returns flat grant edges; the UI joins against agents+resources.
func (a *API) ListAllGrants(w http.ResponseWriter, r *http.Request) {
	rows, err := a.queries.ListAllGrants(r.Context())
	if err != nil {
		http.Error(w, "failed to list grants", http.StatusInternalServerError)
		return
	}
	out := make([]grantView, 0, len(rows))
	for _, g := range rows {
		out = append(out, toGrantView(g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": out})
}

// ListAgentGrants handles GET /api/agents/{id}/grants — resources granted to one
// agent (masked resource views), for the per-agent access drawer.
func (a *API) ListAgentGrants(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	rows, err := a.queries.ListResourcesForAgent(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to list agent grants", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": toResourceViews(rows)})
}

// GrantRequest is the body for PUT /api/agents/{id}/grants/{resourceId}.
type GrantRequest struct {
	Scope string `json:"scope"`
}

// GrantAgentResource handles PUT /api/agents/{id}/grants/{resourceId} (idempotent).
func (a *API) GrantAgentResource(w http.ResponseWriter, r *http.Request) {
	agentID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	resourceID, ok := parseUUIDParam(w, r, "resourceId")
	if !ok {
		return
	}
	scope := "use"
	var req GrantRequest
	if json.NewDecoder(r.Body).Decode(&req) == nil && req.Scope != "" {
		scope = req.Scope
	}
	// Validate both ends exist so we never create a dangling grant.
	if _, err := a.queries.GetAgent(r.Context(), agentID); err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	if _, err := a.queries.GetResource(r.Context(), resourceID); err != nil {
		http.Error(w, "resource not found", http.StatusNotFound)
		return
	}
	g, err := a.queries.GrantResource(r.Context(), db.GrantResourceParams{
		AgentID: agentID, ResourceID: resourceID, Scope: scope,
	})
	if err != nil {
		http.Error(w, "failed to grant: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, toGrantView(g))
}

// RevokeAgentResource handles DELETE /api/agents/{id}/grants/{resourceId}.
func (a *API) RevokeAgentResource(w http.ResponseWriter, r *http.Request) {
	agentID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}
	resourceID, ok := parseUUIDParam(w, r, "resourceId")
	if !ok {
		return
	}
	// Validate both ends exist so a bad id returns 404 (consistent with grant).
	if _, err := a.queries.GetAgent(r.Context(), agentID); err != nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	if _, err := a.queries.GetResource(r.Context(), resourceID); err != nil {
		http.Error(w, "resource not found", http.StatusNotFound)
		return
	}
	if err := a.queries.RevokeResource(r.Context(), db.RevokeResourceParams{
		AgentID: agentID, ResourceID: resourceID,
	}); err != nil {
		http.Error(w, "failed to revoke: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers --------------------------------------------------------------

func parseUUIDParam(w http.ResponseWriter, r *http.Request, name string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	if err := id.Scan(chi.URLParam(r, name)); err != nil {
		http.Error(w, "invalid "+name, http.StatusBadRequest)
		return id, false
	}
	return id, true
}

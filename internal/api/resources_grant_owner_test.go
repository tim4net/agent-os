package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ---------------------------------------------------------------------------
// Cross-owner isolation for the resource-grant path (WP-92 owner_id spine).
//
// ListResourcesForAgent (internal/db/queries/resources.sql) filters ONLY by
// agent_id and intentionally does not re-check owner_id. Its safety rests on an
// invariant created one layer up: GrantAgentResource validates that BOTH the
// agent and the resource belong to the calling owner (via owner-scoped
// GetAgent/GetResource) before it ever inserts an agent_grants row, so every
// grant is inherently intra-owner.
//
// These tests lock that invariant in. If a future change creates a grant
// without the ownership check — or drops owner_id from GetAgent/GetResource —
// the cross-owner assertions go red instead of silently leaking another owner's
// (possibly secret) resources.
// ---------------------------------------------------------------------------

// ctxWithOwner injects an arbitrary owner identity into ctx — a generalization
// of withTestOwner (which is hard-wired to the seeded owner-0) for multi-owner
// isolation tests.
func ctxWithOwner(ctx context.Context, owner pgtype.UUID) context.Context {
	return context.WithValue(ctx, ownerIDKey, owner)
}

// uuidStr renders a pgtype.UUID as a canonical hyphenated string for chi URL
// params (parseUUIDParam feeds the string back through pgtype.UUID.Scan).
func uuidStr(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }

// newGrantHTTPRequest builds a PUT /grants request with the chi {id}/{resourceId}
// params and the caller's owner identity wired into the context.
func newGrantHTTPRequest(t *testing.T, agentID, resourceID, owner pgtype.UUID) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuidStr(agentID))
	rctx.URLParams.Add("resourceId", uuidStr(resourceID))
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(ctxWithOwner(ctx, owner))
}

// newListAgentGrantsHTTPRequest builds a GET grants request with the chi {id}
// param and the caller's owner identity wired into the context.
func newListAgentGrantsHTTPRequest(t *testing.T, agentID, owner pgtype.UUID) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuidStr(agentID))
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(ctxWithOwner(ctx, owner))
}

// TestGrantCrossOwnerIsolation proves an owner can never grant — or resolve
// granted resources for — an agent/resource outside its own ownership boundary.
func TestGrantCrossOwnerIsolation(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}

	pool := getTestDB(t)
	queries := db.New(pool)
	a := &API{queries: queries, pool: pool}
	ctx := context.Background()
	suf := "-" + uuid.NewString()[:8] // unique suffix → no collisions on the shared test DB

	ownerX := testOwnerID() // owner-0 (seeded 'tim')

	// Second owner Y (must exist for the FK on agents/resources/agent_grants.owner_id).
	userY, err := queries.CreateUser(ctx, db.CreateUserParams{
		Login:       "iso-y" + suf,
		DisplayName: "Owner Y",
	})
	if err != nil {
		t.Fatalf("CreateUser Y: %v", err)
	}
	ownerY := userY.ID

	// X owns agent A_X and (non-secret) resource R_X.
	aX, err := queries.CreateAgent(ctx, db.CreateAgentParams{
		OwnerID: ownerX, Name: "iso-agent-x" + suf, DisplayName: "X agent",
		Harness: "generic", BaseUrl: "http://x.test", Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateAgent X: %v", err)
	}
	rX, err := queries.CreateResource(ctx, db.CreateResourceParams{
		OwnerID: ownerX, Slug: "iso-res-x" + suf, Kind: "credential", Label: "X res",
		Provider: "p", IsSecret: false, Config: []byte("{}"), Status: "active",
		EncKeyVersion: 1,
	})
	if err != nil {
		t.Fatalf("CreateResource X: %v", err)
	}
	// Y owns only agent A_Y (no resources).
	aY, err := queries.CreateAgent(ctx, db.CreateAgentParams{
		OwnerID: ownerY, Name: "iso-agent-y" + suf, DisplayName: "Y agent",
		Harness: "generic", BaseUrl: "http://y.test", Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateAgent Y: %v", err)
	}

	t.Cleanup(func() {
		// Grants first (FK), then agents/resource/user.
		_, _ = pool.Exec(ctx,
			"DELETE FROM agent_grants WHERE resource_id = $1 OR agent_id = ANY($2::uuid[])",
			rX.ID, []string{uuidStr(aX.ID), uuidStr(aY.ID)})
		_ = queries.DeleteAgent(ctx, db.DeleteAgentParams{ID: aX.ID, OwnerID: ownerX})
		_ = queries.DeleteAgent(ctx, db.DeleteAgentParams{ID: aY.ID, OwnerID: ownerY})
		_ = queries.DeleteResource(ctx, db.DeleteResourceParams{ID: rX.ID, OwnerID: ownerX})
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", ownerY)
	})

	// ── NEGATIVE 1: Y cannot grant X's resource to Y's own agent.
	//    Y owns the agent but NOT the resource → GetResource(ownerID=Y) 404s.
	rec := httptest.NewRecorder()
	a.GrantAgentResource(rec, newGrantHTTPRequest(t, aY.ID, rX.ID, ownerY))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Y grants X's resource to Y's agent: want 404, got %d; body=%s", rec.Code, rec.Body.String())
	}

	// ── NEGATIVE 2: X cannot grant X's resource to Y's agent.
	//    X owns the resource but NOT the agent → GetAgent(ownerID=X) 404s.
	rec2 := httptest.NewRecorder()
	a.GrantAgentResource(rec2, newGrantHTTPRequest(t, aY.ID, rX.ID, ownerX))
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("X grants X's resource to Y's agent: want 404, got %d; body=%s", rec2.Code, rec2.Body.String())
	}

	// ── POSITIVE: X grants R_X to A_X (intra-owner) → 200.
	rec3 := httptest.NewRecorder()
	a.GrantAgentResource(rec3, newGrantHTTPRequest(t, aX.ID, rX.ID, ownerX))
	if rec3.Code != http.StatusOK {
		t.Fatalf("X grants X's resource to X's agent (intra-owner): want 200, got %d; body=%s", rec3.Code, rec3.Body.String())
	}

	// ── READ ISOLATION (handler boundary): X's agent resolves exactly {R_X}.
	xCount := len(grantedResourceIDs(t, a, aX.ID, ownerX))
	if xCount != 1 {
		t.Fatalf("ListAgentGrants for X's agent: want 1 resource, got %d", xCount)
	}
	// ── READ ISOLATION (handler boundary): Y's agent resolves to NOTHING.
	yCount := len(grantedResourceIDs(t, a, aY.ID, ownerY))
	if yCount != 0 {
		t.Fatalf("ListAgentGrants for Y's agent: want 0 resources (no cross-owner leak), got %d", yCount)
	}

	// ── READ ISOLATION (SQL layer): the unguarded ListResourcesForAgent query
	//    returns only intra-owner rows because no cross-owner grant can exist.
	rows, err := queries.ListResourcesForAgent(ctx, aX.ID)
	if err != nil {
		t.Fatalf("ListResourcesForAgent(X's agent): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != rX.ID {
		t.Fatalf("ListResourcesForAgent(X's agent): want exactly R_X, got %d rows", len(rows))
	}
	rowsY, err := queries.ListResourcesForAgent(ctx, aY.ID)
	if err != nil {
		t.Fatalf("ListResourcesForAgent(Y's agent): %v", err)
	}
	if len(rowsY) != 0 {
		t.Fatalf("ListResourcesForAgent(Y's agent): want 0 rows (no cross-owner leak), got %d", len(rowsY))
	}
}

// grantedResourceIDs calls GET /api/agents/{id}/grants as `owner` and returns the
// IDs of resources visible to that agent.
func grantedResourceIDs(t *testing.T, a *API, agentID, owner pgtype.UUID) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	a.ListAgentGrants(rec, newListAgentGrantsHTTPRequest(t, agentID, owner))
	if rec.Code != http.StatusOK {
		t.Fatalf("ListAgentGrants: want 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Resources []struct {
			ID any `json:"id"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse ListAgentGrants response: %v; body=%s", err, rec.Body.String())
	}
	ids := make([]string, len(resp.Resources))
	for i, r := range resp.Resources {
		switch v := r.ID.(type) {
		case string:
			ids[i] = v
		default:
			ids[i] = ""
		}
	}
	return ids
}

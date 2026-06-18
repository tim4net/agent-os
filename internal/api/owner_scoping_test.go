package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
	"github.com/tim4net/agent-os/internal/service"
)

// owner0UUID is the seed owner-0 UUID from migration 024.
var owner0UUID = pgtype.UUID{
	Bytes: [16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
	Valid: true,
}

// ownerScopingTestDB returns a test DB pool or skips. It also verifies that
// migration 025 (owner_id backfill) has been applied by checking that the
// agents table has an owner_id column.
func ownerScopingTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := getTestDB(t)

	// Verify migration 025 is applied: agents.owner_id must exist.
	var colName string
	err := pool.QueryRow(context.Background(),
		`SELECT column_name FROM information_schema.columns
		 WHERE table_name = 'agents' AND column_name = 'owner_id'`).Scan(&colName)
	if err != nil {
		t.Skip("owner_id column not found on agents — migration 025 not applied, skipping owner-scoping tests")
	}
	return pool
}

// newOwnerScopingTestAPI creates a test API with a real DB for owner-scoping tests.
func newOwnerScopingTestAPI(t *testing.T) (*API, *pgxpool.Pool) {
	t.Helper()
	pool := ownerScopingTestDB(t)
	queries := db.New(pool)
	bus := service.NewEventBus()
	a := &API{
		queries: queries,
		bus:     bus,
		pool:    pool,
	}
	return a, pool
}

// seedTestOwner creates a user row for testing and returns the UUID.
func seedTestOwner(t *testing.T, pool *pgxpool.Pool, login string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var id pgtype.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (login, display_name) VALUES ($1, $2)
		 ON CONFLICT (login) DO UPDATE SET display_name = EXCLUDED.display_name
		 RETURNING id`, login, login).Scan(&id)
	if err != nil {
		t.Fatalf("seed test owner %q: %v", login, err)
	}
	return id
}

// cleanupTestAgents removes all test agents with names matching the given prefix.
func cleanupTestAgents(t *testing.T, pool *pgxpool.Pool, prefix string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`DELETE FROM agents WHERE name LIKE $1`, prefix+"%")
	if err != nil {
		t.Logf("cleanup test agents: %v", err)
	}
}

// TestOwnerID_InsertSetsFromContext tests AC5: Insert via sqlc with owner_id from context;
// fetch confirms stored owner_id matches.
func TestOwnerID_InsertSetsFromContext(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	_, pool := newOwnerScopingTestAPI(t)
	defer pool.Close()

	ctx := context.Background()
	queries := db.New(pool)

	ownerID := seedTestOwner(t, pool, "test-owner-ac5")
	defer cleanupTestAgents(t, pool, "test-ac5")

	agent, err := queries.CreateAgent(ctx, db.CreateAgentParams{
		OwnerID:     ownerID,
		Name:        fmt.Sprintf("test-ac5-%s", uuid.New().String()[:8]),
		DisplayName: "AC5 Test Agent",
		Harness:     "hermes",
		BaseUrl:     "http://localhost:9999",
		Metadata:    []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if !agent.OwnerID.Valid {
		t.Fatal("agent.OwnerID is NULL — expected owner_id to be set")
	}
	if agent.OwnerID != ownerID {
		t.Fatalf("agent.OwnerID = %v, want %v", agent.OwnerID, ownerID)
	}
}

// TestOwnerID_ListFiltersByOwner tests AC6: seed rows for owner-A and owner-B;
// List for A returns only A's rows.
func TestOwnerID_ListFiltersByOwner(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	_, pool := newOwnerScopingTestAPI(t)
	defer pool.Close()

	ctx := context.Background()
	queries := db.New(pool)

	ownerA := seedTestOwner(t, pool, "test-owner-a-ac6")
	ownerB := seedTestOwner(t, pool, "test-owner-b-ac6")
	defer cleanupTestAgents(t, pool, "test-ac6")

	// Create agents for owner A
	nameA1 := fmt.Sprintf("test-ac6-a1-%s", uuid.New().String()[:8])
	nameA2 := fmt.Sprintf("test-ac6-a2-%s", uuid.New().String()[:8])
	_, err := queries.CreateAgent(ctx, db.CreateAgentParams{
		OwnerID: ownerA, Name: nameA1, DisplayName: "A1",
		Harness: "hermes", BaseUrl: "http://localhost:1", Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateAgent A1: %v", err)
	}
	_, err = queries.CreateAgent(ctx, db.CreateAgentParams{
		OwnerID: ownerA, Name: nameA2, DisplayName: "A2",
		Harness: "hermes", BaseUrl: "http://localhost:2", Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateAgent A2: %v", err)
	}

	// Create agent for owner B
	nameB1 := fmt.Sprintf("test-ac6-b1-%s", uuid.New().String()[:8])
	_, err = queries.CreateAgent(ctx, db.CreateAgentParams{
		OwnerID: ownerB, Name: nameB1, DisplayName: "B1",
		Harness: "hermes", BaseUrl: "http://localhost:3", Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateAgent B1: %v", err)
	}

	// List for owner A should return only A's agents
	agentsA, err := queries.ListAgents(ctx, ownerA)
	if err != nil {
		t.Fatalf("ListAgents A: %v", err)
	}
	for _, a := range agentsA {
		if a.OwnerID != ownerA {
			t.Errorf("ListAgents(A) returned agent with owner_id = %v, want %v (agent: %s)", a.OwnerID, ownerA, a.Name)
		}
	}

	// Verify A's results include the created agents
	foundA1, foundA2 := false, false
	for _, a := range agentsA {
		if a.Name == nameA1 {
			foundA1 = true
		}
		if a.Name == nameA2 {
			foundA2 = true
		}
	}
	if !foundA1 || !foundA2 {
		t.Errorf("ListAgents(A) missing expected agents: foundA1=%v foundA2=%v", foundA1, foundA2)
	}

	// List for owner B should return only B's agents
	agentsB, err := queries.ListAgents(ctx, ownerB)
	if err != nil {
		t.Fatalf("ListAgents B: %v", err)
	}
	for _, a := range agentsB {
		if a.OwnerID != ownerB {
			t.Errorf("ListAgents(B) returned agent with owner_id = %v, want %v (agent: %s)", a.OwnerID, ownerB, a.Name)
		}
	}
}

// TestOwnerID_CrossOwnerInvisible tests AC7: owner-A cannot GET rows belonging
// to owner-B (empty result or not-found).
func TestOwnerID_CrossOwnerInvisible(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	_, pool := newOwnerScopingTestAPI(t)
	defer pool.Close()

	ctx := context.Background()
	queries := db.New(pool)

	ownerA := seedTestOwner(t, pool, "test-owner-a-ac7")
	ownerB := seedTestOwner(t, pool, "test-owner-b-ac7")
	defer cleanupTestAgents(t, pool, "test-ac7")

	// Create an agent for owner B
	agentB, err := queries.CreateAgent(ctx, db.CreateAgentParams{
		OwnerID: ownerB,
		Name:    fmt.Sprintf("test-ac7-b-%s", uuid.New().String()[:8]),
		DisplayName: "B Agent",
		Harness: "hermes", BaseUrl: "http://localhost:99", Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateAgent B: %v", err)
	}

	// Owner A tries to GET owner B's agent — should get no rows
	_, err = queries.GetAgent(ctx, db.GetAgentParams{
		ID:      agentB.ID,
		OwnerID: ownerA,
	})
	if err == nil {
		t.Fatal("GetAgent with wrong owner_id should return no rows, but got nil error")
	}
}

// TestOwnerID_HandlerRejection tests that handlers without owner_id in context
// return 401.
func TestOwnerID_HandlerRejection(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	api, pool := newOwnerScopingTestAPI(t)
	defer pool.Close()

	seedTestIngestKey(t, pool)

	// Request without X-Webauth-User header should get 401
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	w := httptest.NewRecorder()

	api.ListAgents(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("ListAgents without identity: got status %d, want 401", w.Code)
	}
}

// TestOwnerID_HandlerWithIdentity tests that a request with proper identity
// returns 200 and properly scoped data (only the requesting owner's agents).
func TestOwnerID_HandlerWithIdentity(t *testing.T) {
	if os.Getenv("AOS_TEST_DATABASE_URL") == "" {
		t.Skip("AOS_TEST_DATABASE_URL not set — skipping integration test")
	}
	api, pool := newOwnerScopingTestAPI(t)
	defer pool.Close()

	ownerID := seedTestOwner(t, pool, "test-owner-handler")
	otherOwner := seedTestOwner(t, pool, "test-owner-other")
	defer cleanupTestAgents(t, pool, "test-handler")

	ctx := context.Background()
	queries := db.New(pool)

	// Create an agent for this owner (visible so ListVisibleAgents returns it).
	agentName := fmt.Sprintf("test-handler-%s", uuid.New().String()[:8])
	_, err := queries.CreateAgent(ctx, db.CreateAgentParams{
		OwnerID:     ownerID,
		Name:        agentName,
		DisplayName: "Handler Test",
		Harness:     "hermes", BaseUrl: "http://localhost:80", Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Create an agent for a DIFFERENT owner — must NOT appear in our response.
	otherName := fmt.Sprintf("test-handler-other-%s", uuid.New().String()[:8])
	_, err = queries.CreateAgent(ctx, db.CreateAgentParams{
		OwnerID:     otherOwner,
		Name:        otherName,
		DisplayName: "Other Owner",
		Harness:     "hermes", BaseUrl: "http://localhost:81", Metadata: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("CreateAgent other: %v", err)
	}

	// Make request with identity header
	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.Header.Set("X-Webauth-User", "test-owner-handler")
	w := httptest.NewRecorder()

	// We need to route through middleware — use the resolved context
	ctxWithOwner := context.WithValue(ctx, ownerIDKey, ownerID)
	ctxWithOwner = context.WithValue(ctxWithOwner, ownerLoginKey, "test-owner-handler")
	req = req.WithContext(ctxWithOwner)

	api.ListAgents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("ListAgents with identity: got status %d, want 200. Body: %s", w.Code, w.Body.String())
	}

	// The handler returns []agentView (a safe projection that deliberately omits
	// owner_id). Verify scoping by name: our agent must be present, the other
	// owner's agent must be absent.
	var agents []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &agents); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	found := false
	for _, a := range agents {
		name, _ := a["name"].(string)
		if name == otherName {
			t.Errorf("handler leaked another owner's agent %q into our scoped response", name)
		}
		if name == agentName {
			found = true
		}
	}
	if !found {
		t.Errorf("handler did not return our agent %q — scoping returned wrong owner's data", agentName)
	}
}

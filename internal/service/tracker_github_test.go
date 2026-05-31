package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// fakeGitHubTrackerQuerier is a fakeTrackerQuerier variant that returns
// github_issues projects in GetTrackerProjects.
type fakeGitHubTrackerQuerier struct {
	mu            sync.Mutex
	items         []db.TrackerItem
	upserted      []db.UpsertTrackerItemParams
	upsertErr     error
	failAfter     int
	byProject     map[string][]db.TrackerItem
	byExternalRef map[string]db.TrackerItem
}

func newFakeGitHubTrackerQuerier() *fakeGitHubTrackerQuerier {
	return &fakeGitHubTrackerQuerier{
		byProject:     make(map[string][]db.TrackerItem),
		byExternalRef: make(map[string]db.TrackerItem),
	}
}

func (f *fakeGitHubTrackerQuerier) UpsertTrackerItem(_ context.Context, arg db.UpsertTrackerItemParams) (db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.upserted = append(f.upserted, arg)

	if f.failAfter > 0 && len(f.upserted) > f.failAfter {
		return db.TrackerItem{}, f.upsertErr
	}

	now := time.Now()
	item := db.TrackerItem{
		ProjectID:    arg.ProjectID,
		ExternalRef:  arg.ExternalRef,
		Title:        arg.Title,
		Status:       arg.Status,
		ItemType:     arg.ItemType,
		CanonicalUrl: arg.CanonicalUrl,
		Payload:      arg.Payload,
		Tenant:       arg.Tenant,
		SyncedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
	}
	item.ID = pgtype.UUID{Valid: true}
	_ = item.ID.Scan(fmt.Sprintf("00000000-0000-0000-0000-%012s", arg.ExternalRef))

	f.items = append(f.items, item)

	pk := fmt.Sprintf("%s:%s", arg.ProjectID.String(), arg.Tenant)
	f.byProject[pk] = append(f.byProject[pk], item)

	ek := fmt.Sprintf("%s:%s", arg.ProjectID.String(), arg.ExternalRef)
	f.byExternalRef[ek] = item

	return item, nil
}

func (f *fakeGitHubTrackerQuerier) GetTrackerItem(_ context.Context, arg db.GetTrackerItemParams) (db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ek := fmt.Sprintf("%s:%s", arg.ProjectID.String(), arg.ExternalRef)
	if item, ok := f.byExternalRef[ek]; ok {
		return item, nil
	}
	return db.TrackerItem{}, fmt.Errorf("not found")
}

func (f *fakeGitHubTrackerQuerier) ListTrackerItemsByProject(_ context.Context, arg db.ListTrackerItemsByProjectParams) ([]db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pk := fmt.Sprintf("%s:%s", arg.ProjectID.String(), arg.Tenant)
	items := f.byProject[pk]
	if int(arg.Offset) < len(items) {
		items = items[arg.Offset:]
	}
	if int(arg.Limit) < len(items) {
		items = items[:arg.Limit]
	}
	if items == nil {
		return []db.TrackerItem{}, nil
	}
	return items, nil
}

func (f *fakeGitHubTrackerQuerier) ListTrackerItemsByTenant(_ context.Context, arg db.ListTrackerItemsByTenantParams) ([]db.TrackerItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var items []db.TrackerItem
	for _, v := range f.items {
		if v.Tenant == arg.Tenant {
			items = append(items, v)
		}
	}
	if items == nil {
		return []db.TrackerItem{}, nil
	}
	if int(arg.Offset) < len(items) {
		items = items[arg.Offset:]
	}
	if int(arg.Limit) < len(items) {
		items = items[:arg.Limit]
	}
	return items, nil
}

func (f *fakeGitHubTrackerQuerier) CountTrackerItemsByProject(_ context.Context, arg db.CountTrackerItemsByProjectParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pk := fmt.Sprintf("%s:%s", arg.ProjectID.String(), arg.Tenant)
	return int64(len(f.byProject[pk])), nil
}

func (f *fakeGitHubTrackerQuerier) CountTrackerItemsByTenant(_ context.Context, tenant string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int64
	for _, v := range f.items {
		if v.Tenant == tenant {
			count++
		}
	}
	return count, nil
}

// GetTrackerProjects returns a project with tracker="github_issues" and external_ref "tim4net/agent-os".
func (f *fakeGitHubTrackerQuerier) GetTrackerProjects(_ context.Context, arg db.GetTrackerProjectsParams) ([]db.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []db.Project{
		{
			ID:          testProjectUUID,
			Slug:        "agent-os",
			Name:        "Agent OS",
			Tenant:      arg.Tenant,
			Tracker:     arg.Tracker,
			ExternalRef: pgtype.Text{String: "tim4net/agent-os", Valid: true},
			CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	}, nil
}

// stubGitHubServer returns an httptest.Server that serves fake GitHub Issues API responses.
func stubGitHubServer(issues []githubIssueEnvelope) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only GET requests allowed (F5 gate).
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Must have Bearer token.
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(issues)
	}))
}

// TestGitHubSync_FetchMapUpsert tests the full Sync pipeline:
// fetch from GitHub API → map to external_ref #<n> → upsert via TrackerQuerier.

// testGitHubIssue is a helper to build a githubIssueEnvelope with the right struct types.
func testGitHubIssue(number int, title, state, htmlURL string, labels ...string) githubIssueEnvelope {
	env := githubIssueEnvelope{
		Number:  number,
		Title:   title,
		State:   state,
		HTMLURL: htmlURL,
	}
	if len(labels) > 0 {
		for _, l := range labels {
			env.Labels = append(env.Labels, githubLabel{Name: l})
		}
	}
	return env
}

func TestGitHubSync_FetchMapUpsert(t *testing.T) {
	issues := []githubIssueEnvelope{
		testGitHubIssue(14, "WP-F: GitHub Issues tracker source", "open",
			"https://github.com/tim4net/agent-os/issues/14", "spog", "wave:2"),
		testGitHubIssue(10, "WP-A2: Durable per-tenant ingest-key store", "open",
			"https://github.com/tim4net/agent-os/issues/10", "spog"),
		testGitHubIssue(7, "WP-A: Generic work-event ingestion", "closed",
			"https://github.com/tim4net/agent-os/issues/7"),
	}
	issues[0].Body = "Second tracker source..."
	issues[0].User = &githubUser{Login: "tim4net"}

	srv := stubGitHubServer(issues)
	defer srv.Close()

	fake := newFakeGitHubTrackerQuerier()
	log := slog.Default()

	src := NewGitHubSourceWithClient(fake, &GitHubClient{
		apiToken: "test-token",
		client:   srv.Client(),
		baseURL:  srv.URL,
		log:      log,
	}, log)

	result, err := src.Sync(context.Background(), testProjectUUID, "dayjob")
	if err != nil {
		t.Fatalf("Sync returned unexpected error: %v", err)
	}

	if result.Synced != len(issues) {
		t.Errorf("Synced=%d, want %d", result.Synced, len(issues))
	}
	if result.Failed != 0 {
		t.Errorf("Failed=%d, want 0", result.Failed)
	}

	if len(fake.upserted) != len(issues) {
		t.Fatalf("upserted=%d, want %d", len(fake.upserted), len(issues))
	}

	// Assert #<n> external_ref comes from issue.Number.
	if fake.upserted[0].ExternalRef != "#14" {
		t.Errorf("upserted[0].ExternalRef=%q, want %q", fake.upserted[0].ExternalRef, "#14")
	}
	if fake.upserted[1].ExternalRef != "#10" {
		t.Errorf("upserted[1].ExternalRef=%q, want %q", fake.upserted[1].ExternalRef, "#10")
	}
	if fake.upserted[2].ExternalRef != "#7" {
		t.Errorf("upserted[2].ExternalRef=%q, want %q", fake.upserted[2].ExternalRef, "#7")
	}

	// Verify item_type mapping — all should be "task" for GitHub issues.
	for i, up := range fake.upserted {
		if up.ItemType != "task" {
			t.Errorf("upserted[%d].ItemType=%q, want %q", i, up.ItemType, "task")
		}
	}

	// Verify tenant is threaded through.
	for i, up := range fake.upserted {
		if up.Tenant != "dayjob" {
			t.Errorf("upserted[%d].Tenant=%q, want %q", i, up.Tenant, "dayjob")
		}
	}

	// Verify canonical_url is populated.
	if !fake.upserted[0].CanonicalUrl.Valid || fake.upserted[0].CanonicalUrl.String != "https://github.com/tim4net/agent-os/issues/14" {
		t.Errorf("upserted[0].CanonicalUrl=%v, want valid with URL", fake.upserted[0].CanonicalUrl)
	}

	// Verify title mapping.
	if fake.upserted[0].Title != "WP-F: GitHub Issues tracker source" {
		t.Errorf("upserted[0].Title=%q, want %q", fake.upserted[0].Title, "WP-F: GitHub Issues tracker source")
	}

	// Verify payload contains the raw GitHub metadata.
	var payload map[string]any
	if err := json.Unmarshal(fake.upserted[0].Payload, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if payload["github_number"] != float64(14) {
		t.Errorf("payload.github_number=%v, want 14", payload["github_number"])
	}
	if payload["github_state"] != "open" {
		t.Errorf("payload.github_state=%v, want open", payload["github_state"])
	}

	// Verify labels are in payload.
	labels, ok := payload["github_labels"].([]any)
	if !ok {
		t.Fatalf("payload.github_labels is not an array: %T", payload["github_labels"])
	}
	if len(labels) != 2 {
		t.Errorf("payload.github_labels length=%d, want 2", len(labels))
	}
}

// TestGitHubSync_UpsertFailure tests that Sync returns a non-nil error when upserts fail.
func TestGitHubSync_UpsertFailure(t *testing.T) {
	issues := []githubIssueEnvelope{
		testGitHubIssue(1, "Issue A", "open", ""),
		testGitHubIssue(2, "Issue B", "closed", ""),
		testGitHubIssue(3, "Issue C", "open", ""),
	}

	srv := stubGitHubServer(issues)
	defer srv.Close()

	fake := newFakeGitHubTrackerQuerier()
	fake.failAfter = 1 // first upsert succeeds, rest fail
	fake.upsertErr = fmt.Errorf("DB connection lost")

	log := slog.Default()
	src := NewGitHubSourceWithClient(fake, &GitHubClient{
		apiToken: "test-token",
		client:   srv.Client(),
		baseURL:  srv.URL,
		log:      log,
	}, log)

	result, err := src.Sync(context.Background(), testProjectUUID, "dayjob")
	if err == nil {
		t.Fatal("Sync should return non-nil error when upserts fail")
	}

	if result.Synced != 1 {
		t.Errorf("Synced=%d, want 1", result.Synced)
	}
	if result.Failed != 2 {
		t.Errorf("Failed=%d, want 2", result.Failed)
	}
}

// TestGitHubSync_EmptyRepo tests that Sync returns an error when the repo has no issues
// (guardrail: no silent failures — empty sync must surface a real error).
func TestGitHubSync_EmptyRepo(t *testing.T) {
	srv := stubGitHubServer([]githubIssueEnvelope{})
	defer srv.Close()

	fake := newFakeGitHubTrackerQuerier()
	log := slog.Default()

	src := NewGitHubSourceWithClient(fake, &GitHubClient{
		apiToken: "test-token",
		client:   srv.Client(),
		baseURL:  srv.URL,
		log:      log,
	}, log)

	_, err := src.Sync(context.Background(), testProjectUUID, "dayjob")
	if err == nil {
		t.Fatal("Sync should return error for empty repo (no silent 200 {synced:0})")
	}
}

// TestGitHubSync_MissingToken tests that Sync returns an error when no token is configured.
func TestGitHubSync_MissingToken(t *testing.T) {
	fake := newFakeGitHubTrackerQuerier()
	log := slog.Default()

	src := NewGitHubSourceWithClient(fake, &GitHubClient{
		apiToken: "",
		log:      log,
	}, log)

	_, err := src.Sync(context.Background(), testProjectUUID, "dayjob")
	if err == nil {
		t.Fatal("Sync should return error when GITHUB_TOKEN is not configured")
	}
}

// badRefQuerier wraps a fakeGitHubTrackerQuerier and overrides GetTrackerProjects
// to return a malformed external_ref.
type badRefQuerier struct {
	*fakeGitHubTrackerQuerier
}

func (b *badRefQuerier) GetTrackerProjects(ctx context.Context, arg db.GetTrackerProjectsParams) ([]db.Project, error) {
	projects, err := b.fakeGitHubTrackerQuerier.GetTrackerProjects(ctx, arg)
	if err != nil {
		return nil, err
	}
	projects[0].ExternalRef = pgtype.Text{String: "not-a-repo-ref", Valid: true}
	return projects, nil
}

// TestGitHubSync_InvalidExternalRef tests that Sync returns an error when
// the project's external_ref is malformed (not owner/repo).
func TestGitHubSync_InvalidExternalRef(t *testing.T) {
	fake := newFakeGitHubTrackerQuerier()
	bad := &badRefQuerier{fakeGitHubTrackerQuerier: fake}

	log := slog.Default()
	src := NewGitHubSourceWithClient(bad, nil, log)

	_, err := src.Sync(context.Background(), testProjectUUID, "dayjob")
	if err == nil {
		t.Fatal("Sync should return error for malformed external_ref")
	}
}

// TestGitHubSync_SkipsPullRequests verifies that pull requests returned by the
// GitHub /issues endpoint are NOT mirrored as tracker_items. GitHub's endpoint
// returns both issues and PRs; PRs include a non-nil "pull_request" JSON key.
// This is a regression test for the dogfood AC violation.
func TestGitHubSync_SkipsPullRequests(t *testing.T) {
	// Build a mixed page: one real issue + one pull request.
	issue := testGitHubIssue(42, "A real issue", "open", "https://github.com/tim4net/agent-os/issues/42")

	prEnv := testGitHubIssue(16, "WP-F: GitHub Issues tracker", "open", "https://github.com/tim4net/agent-os/pull/16")
	prRaw := json.RawMessage(`{"url":"https://api.github.com/repos/tim4net/agent-os/pulls/16","html_url":"https://github.com/tim4net/agent-os/pull/16","diff_url":"https://github.com/tim4net/agent-os/pull/16.diff","patch_url":"https://github.com/tim4net/agent-os/pull/16.patch"}`)
	prEnv.PullRequest = &prRaw

	mixed := []githubIssueEnvelope{issue, prEnv}

	srv := stubGitHubServer(mixed)
	defer srv.Close()

	fake := newFakeGitHubTrackerQuerier()
	log := slog.Default()

	src := NewGitHubSourceWithClient(fake, &GitHubClient{
		apiToken: "test-token",
		client:   srv.Client(),
		baseURL:  srv.URL,
		log:      log,
	}, log)

	result, err := src.Sync(context.Background(), testProjectUUID, "dayjob")
	if err != nil {
		t.Fatalf("Sync returned unexpected error: %v", err)
	}

	// Only the issue should have been upserted; the PR must be skipped.
	if result.Synced != 1 {
		t.Errorf("Synced=%d, want 1 (PR should have been skipped)", result.Synced)
	}
	if result.Failed != 0 {
		t.Errorf("Failed=%d, want 0", result.Failed)
	}
	if len(fake.upserted) != 1 {
		t.Fatalf("upserted=%d, want 1 — PR leaked into tracker_items", len(fake.upserted))
	}
	if fake.upserted[0].ExternalRef != "#42" {
		t.Errorf("upserted[0].ExternalRef=%q, want %q (the issue, not the PR)", fake.upserted[0].ExternalRef, "#42")
	}
}

// TestGitHubList_TenantIsolation verifies that List scoped to a tenant only returns
// items for that tenant, even when another tenant has items in the same project.
func TestGitHubList_TenantIsolation(t *testing.T) {
	fake := newFakeGitHubTrackerQuerier()
	log := slog.Default()

	// Seed items for two tenants under the same project.
	dayjobItem := db.TrackerItem{
		ProjectID:    testProjectUUID,
		ExternalRef:  "#14",
		Title:        "WP-F: GitHub Issues",
		Status:       "open",
		ItemType:     "task",
		Tenant:       "dayjob",
		SyncedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
		CreatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	personalItem := db.TrackerItem{
		ProjectID:    testProjectUUID,
		ExternalRef:  "#99",
		Title:        "Personal Issue",
		Status:       "open",
		ItemType:     "task",
		Tenant:       "personal",
		SyncedAt:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
		CreatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	fake.items = append(fake.items, dayjobItem, personalItem)
	fake.byProject[fmt.Sprintf("%s:dayjob", testProjectUUID.String())] = []db.TrackerItem{dayjobItem}
	fake.byProject[fmt.Sprintf("%s:personal", testProjectUUID.String())] = []db.TrackerItem{personalItem}

	src := NewGitHubSourceWithClient(fake, nil, log)

	// List with tenant="personal" should only return the personal item.
	items, err := src.List(context.Background(), testProjectUUID, "personal", 50, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("List(personal) returned %d items, want 1", len(items))
	}
	if items[0].ExternalRef != "#99" {
		t.Errorf("items[0].ExternalRef=%q, want %q", items[0].ExternalRef, "#99")
	}
	if items[0].Tenant != "personal" {
		t.Errorf("items[0].Tenant=%q, want %q", items[0].Tenant, "personal")
	}

	// List with tenant="dayjob" should only return the dayjob item.
	items, err = src.List(context.Background(), testProjectUUID, "dayjob", 50, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("List(dayjob) returned %d items, want 1", len(items))
	}
	if items[0].ExternalRef != "#14" {
		t.Errorf("items[0].ExternalRef=%q, want %q", items[0].ExternalRef, "#14")
	}

	// The dayjob list must NOT contain the personal item.
	for _, item := range items {
		if item.Tenant == "personal" {
			t.Errorf("dayjob list leaked personal item: %q", item.ExternalRef)
		}
	}
}

// TestGitHubList_Pagination verifies limit clamping and offset behavior.
func TestGitHubList_Pagination(t *testing.T) {
	fake := newFakeGitHubTrackerQuerier()
	log := slog.Default()

	// Seed MaxTrackerItemLimit+1 (201) items to actually exercise boundary conditions.
	testPK := fmt.Sprintf("%s:test", testProjectUUID.String())
	for i := 0; i < MaxTrackerItemLimit+1; i++ {
		item := db.TrackerItem{
			ProjectID: testProjectUUID,
			ExternalRef: fmt.Sprintf("#%d", i+1),
			Tenant:      "test",
			SyncedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			UpdatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}
		fake.items = append(fake.items, item)
		fake.byProject[testPK] = append(fake.byProject[testPK], item)
	}

	src := NewGitHubSourceWithClient(fake, nil, log)

	// limit=0 defaults to 50.
	items, err := src.List(context.Background(), testProjectUUID, "test", 0, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 50 {
		t.Errorf("List(limit=0) returned %d items, want 50 (default)", len(items))
	}

	// limit > MaxTrackerItemLimit is clamped to MaxTrackerItemLimit (200).
	items, err = src.List(context.Background(), testProjectUUID, "test", 999, 0)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != MaxTrackerItemLimit {
		t.Errorf("List(limit=999) returned %d items, want %d (MaxTrackerItemLimit clamped)", len(items), MaxTrackerItemLimit)
	}

	// offset=150, limit=200 returns 51 items (201 total - 150 offset).
	items, err = src.List(context.Background(), testProjectUUID, "test", 200, 150)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(items) != 51 {
		t.Errorf("List(limit=200,offset=150) returned %d items, want 51", len(items))
	}
}

// TestGitHubStateToItemType verifies the state-to-itemType mapping.
func TestGitHubStateToItemType(t *testing.T) {
	tests := []struct {
		state string
		want  string
	}{
		{"open", "task"},
		{"closed", "task"},
		{"Open", "task"},
		{"CLOSED", "task"},
		{"weird", "task"},
	}
	for _, tt := range tests {
		got := githubStateToItemType(tt.state)
		if got != tt.want {
			t.Errorf("githubStateToItemType(%q) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestXAIVideoProvider_SubmitAndPoll(t *testing.T) {
	apiKey := "test-key-123"

	mockVideoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake-video-bytes"))
	}))
	defer mockVideoSrv.Close()

	var getCalls int
	mu := sync.Mutex{}

	xaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+apiKey {
			t.Errorf("expected auth Bearer %s, got %s", apiKey, auth)
		}

		if r.Method == http.MethodPost && r.URL.Path == "/v1/video/generations" {
			var req map[string]interface{}
			json.NewDecoder(r.Body).Decode(&req)
			if req["model"] != "grok-2-video" {
				t.Errorf("expected model grok-2-video, got %v", req["model"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"gen_123"}`))
			return
		}

		if r.Method == http.MethodGet && r.URL.Path == "/v1/video/generations/gen_123" {
			mu.Lock()
			getCalls++
			calls := getCalls
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			if calls == 1 {
				w.Write([]byte(`{"id":"gen_123","state":"pending"}`))
			} else {
				w.Write([]byte(`{"id":"gen_123","state":"complete","video":{"url":"` + mockVideoSrv.URL + `"}}`))
			}
			return
		}

		http.NotFound(w, r)
	}))
	defer xaiSrv.Close()

	provider := NewXAIVideoProvider(apiKey)
	provider.baseURL = xaiSrv.URL

	ctx := context.Background()
	jobID, err := provider.Submit(ctx, "a cool video", "grok-2-video")
	if err != nil {
		t.Fatalf("Submit error: %v", err)
	}
	if jobID != "gen_123" {
		t.Errorf("expected jobID gen_123, got %s", jobID)
	}

	// Poll 1: pending
	res, err := provider.FetchStatus(ctx, jobID)
	if err != nil {
		t.Fatalf("FetchStatus 1 error: %v", err)
	}
	if res.State != "pending" {
		t.Errorf("expected pending, got %s", res.State)
	}

	// Poll 2: complete
	res, err = provider.FetchStatus(ctx, jobID)
	if err != nil {
		t.Fatalf("FetchStatus 2 error: %v", err)
	}
	if res.State != "complete" {
		t.Errorf("expected complete, got %s", res.State)
	}
	if res.VideoURL != mockVideoSrv.URL {
		t.Errorf("expected %s, got %s", mockVideoSrv.URL, res.VideoURL)
	}

	// Test missing key
	pNoKey := NewXAIVideoProvider("")
	_, err = pNoKey.Submit(ctx, "prompt", "model")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected not configured error, got %v", err)
	}
}

func TestVideoJobStore_Lifecycle(t *testing.T) {
	store := NewVideoJobStore()
	job := store.Create("prompt", "model", "test-prov")

	if job.State != "queued" || job.Progress != 0 {
		t.Errorf("expected queued/0, got %s/%d", job.State, job.Progress)
	}

	store.SetProcessing(job.ID, "up_123")

	j2, ok := store.Get(job.ID)
	if !ok || j2.State != "processing" || j2.Progress != 50 || j2.UpstreamID != "up_123" {
		t.Errorf("expected processing/50/up_123, got %s/%d/%s", j2.State, j2.Progress, j2.UpstreamID)
	}

	store.Complete(job.ID, "http://vid", "art_123")
	j3, _ := store.Get(job.ID)
	if j3.State != "complete" || j3.Progress != 100 || j3.VideoURL != "http://vid" || j3.ArtifactID != "art_123" {
		t.Errorf("expected complete/100, got %s/%d", j3.State, j3.Progress)
	}

	store.Fail(job.ID, "some error")
	j4, _ := store.Get(job.ID)
	if j4.State != "failed" || j4.Error != "some error" {
		t.Errorf("expected failed, got %s", j4.State)
	}

	// Mutating the returned copy should not change store
	j4.State = "hacked"
	j5, _ := store.Get(job.ID)
	if j5.State == "hacked" {
		t.Errorf("store leaked mutable reference")
	}
}

type fakeProvider struct {
	upstreamID string
	res        VideoResult
	err        error
}

func (f *fakeProvider) Submit(ctx context.Context, prompt, model string) (string, error) {
	return f.upstreamID, f.err
}

func (f *fakeProvider) FetchStatus(ctx context.Context, jobID string) (VideoResult, error) {
	return f.res, f.err
}

type fakeFinalizer struct {
	artID string
	err   error
}

func (f *fakeFinalizer) Store(ctx context.Context, job *VideoJob, videoURL string) (string, error) {
	return f.artID, f.err
}

func TestStudioAPI_SubmitVideoJob_Endpoint(t *testing.T) {
	s := &StudioAPI{
		videoProviders: make(map[string]VideoProvider),
		videoJobs:      NewVideoJobStore(),
		providerInfo:   make(map[string]ProviderInfo),
	}

	s.videoProviders["test-prov"] = &fakeProvider{upstreamID: "up_123"}
	s.providerInfo["test-prov"] = ProviderInfo{Available: true}

	router := s.StudioRoutes()

	// Submit
	reqBody := strings.NewReader(`{"prompt":"a prompt","model":"model","provider":"test-prov"}`)
	req := httptest.NewRequest(http.MethodPost, "/video/jobs", reqBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d. Body: %s", w.Code, w.Body.String())
	}

	var res map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &res)
	id := res["id"].(string)

	if res["state"] != "processing" {
		t.Errorf("expected processing, got %v", res["state"])
	}

	// Get
	req2 := httptest.NewRequest(http.MethodGet, "/video/jobs/"+id, nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w2.Code)
	}
	var res2 map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &res2)
	if res2["id"] != id || res2["state"] != "processing" {
		t.Errorf("expected match, got %v", res2)
	}

	// 404
	req3 := httptest.NewRequest(http.MethodGet, "/video/jobs/unknown", nil)
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w3.Code)
	}
}

func TestVideoPoller_Flow(t *testing.T) {
	store := NewVideoJobStore()
	fp := &fakeProvider{}
	ff := &fakeFinalizer{}

	s := &StudioAPI{
		videoProviders: map[string]VideoProvider{"test-prov": fp},
		videoJobs:      store,
		videoFinalizer: ff,
	}

	job1 := store.Create("prompt", "model", "test-prov")
	store.SetProcessing(job1.ID, "up_1")

	job2 := store.Create("prompt", "model", "test-prov")
	store.SetProcessing(job2.ID, "up_2")

	ctx := context.Background()

	// Case 1: job1 completes
	fp.res = VideoResult{State: "complete", VideoURL: "http://vid"}
	ff.artID = "art_1"

	s.pollVideoJobsOnce(ctx)

	j1, _ := store.Get(job1.ID)
	if j1.State != "complete" || j1.ArtifactID != "art_1" || j1.VideoURL != "http://vid" {
		t.Errorf("expected complete state, got %v", j1)
	}

	j2, _ := store.Get(job2.ID)
	if j2.State != "complete" {
		t.Errorf("expected job2 complete too, got %v", j2)
	}

	// Reset for job3
	job3 := store.Create("prompt", "model", "test-prov")
	store.SetProcessing(job3.ID, "up_3")

	// Case 2: fail
	fp.res = VideoResult{State: "failed", ErrMsg: "error msg"}

	s.pollVideoJobsOnce(ctx)

	j3, _ := store.Get(job3.ID)
	if j3.State != "failed" || j3.Error != "error msg" {
		t.Errorf("expected failed, got %v", j3)
	}
}

// TestVideoJobStore_CapRejectsWhenFullOfActive is a NEGATIVE test: when the store
// is at capacity with only active (non-terminal) jobs, Create must refuse rather
// than grow the map without bound.
func TestVideoJobStore_CapRejectsWhenFullOfActive(t *testing.T) {
	store := NewVideoJobStore()
	store.maxActive = 3

	for i := 0; i < 3; i++ {
		j := store.Create("p", "m", "prov")
		if j == nil {
			t.Fatalf("Create #%d unexpectedly refused", i)
		}
		store.SetProcessing(j.ID, "up")
	}

	if j := store.Create("p", "m", "prov"); j != nil {
		t.Errorf("expected Create to be refused at cap of active jobs, got %v", j)
	}
}

// TestVideoJobStore_EvictsTerminalToMakeRoom proves completed/failed jobs are
// evicted oldest-first to make room when the store is at capacity.
func TestVideoJobStore_EvictsTerminalToMakeRoom(t *testing.T) {
	store := NewVideoJobStore()
	store.maxActive = 3

	old := store.Create("old", "m", "prov")
	store.Complete(old.ID, "http://vid", "art1")

	mid := store.Create("mid", "m", "prov")
	store.Fail(mid.ID, "boom")

	third := store.Create("third", "m", "prov")
	store.SetProcessing(third.ID, "up")

	fourth := store.Create("fourth", "m", "prov")
	if fourth == nil {
		t.Fatalf("expected Create to succeed by evicting a terminal job")
	}
	if _, ok := store.Get(old.ID); ok {
		t.Errorf("expected oldest terminal job to be evicted to make room")
	}
	if _, ok := store.Get(fourth.ID); !ok {
		t.Errorf("expected new job to be present after eviction")
	}
}

// TestVideoJobStore_ReapTerminal proves the poller's reaper drops only aged-out
// terminal jobs and never touches active ones.
func TestVideoJobStore_ReapTerminal(t *testing.T) {
	store := NewVideoJobStore()

	completed := store.Create("c", "m", "prov")
	store.Complete(completed.ID, "u", "a")
	store.jobs[completed.ID].UpdatedAt = time.Now().Add(-2 * time.Hour) // aged out

	recent := store.Create("r", "m", "prov")
	store.Fail(recent.ID, "x")

	active := store.Create("a", "m", "prov")
	store.SetProcessing(active.ID, "up")

	removed := store.ReapTerminal(time.Now(), 1*time.Hour)
	if removed != 1 {
		t.Errorf("expected 1 reaped terminal job, got %d", removed)
	}
	if _, ok := store.Get(completed.ID); ok {
		t.Errorf("expected aged-out completed job to be reaped")
	}
	if _, ok := store.Get(recent.ID); !ok {
		t.Errorf("expected recent failed job to be retained")
	}
	if _, ok := store.Get(active.ID); !ok {
		t.Errorf("expected active job to never be reaped")
	}
}

// TestStudioAPI_CloseIsIdempotent proves the poller lifecycle stop channel can be
// closed more than once without panicking (sync.Once guard).
func TestStudioAPI_CloseIsIdempotent(t *testing.T) {
	s := &StudioAPI{
		stopVideoPoller: make(chan struct{}),
	}
	s.Close()
	s.Close() // double-close of the underlying channel would panic without sync.Once

	select {
	case <-s.stopVideoPoller:
	default:
		t.Errorf("expected stopVideoPoller channel to be closed after Close")
	}
}

// ctxAwareProvider blocks FetchStatus until ctx is cancelled (or block closed),
// simulating a hung upstream.
type ctxAwareProvider struct {
	block chan struct{}
}

func (p *ctxAwareProvider) Submit(ctx context.Context, prompt, model string) (string, error) {
	return "up_ctx", nil
}

func (p *ctxAwareProvider) FetchStatus(ctx context.Context, jobID string) (VideoResult, error) {
	select {
	case <-ctx.Done():
		return VideoResult{}, ctx.Err()
	case <-p.block:
		return VideoResult{State: "complete"}, nil
	}
}

// TestVideoPoller_PerCallTimeoutDoesNotBlockOtherJobs is the key NEGATIVE
// concurrency test: a single hung upstream job must NOT starve the other jobs
// queued behind it. The per-call deadline cancels the hung call and bounded
// concurrency lets the fast job proceed in parallel.
func TestVideoPoller_PerCallTimeoutDoesNotBlockOtherJobs(t *testing.T) {
	store := NewVideoJobStore()

	hungJob := store.Create("hung", "m", "slow-prov")
	store.SetProcessing(hungJob.ID, "up_hung")

	fastJob := store.Create("fast", "m", "fast-prov")
	store.SetProcessing(fastJob.ID, "up_fast")

	slow := &ctxAwareProvider{block: make(chan struct{})}
	fast := &fakeProvider{res: VideoResult{State: "complete", VideoURL: "http://vid"}}
	ff := &fakeFinalizer{artID: "art_fast"}

	s := &StudioAPI{
		videoProviders:  map[string]VideoProvider{"slow-prov": slow, "fast-prov": fast},
		videoJobs:       store,
		videoFinalizer:  ff,
		pollCallTimeout: 50 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		s.pollVideoJobsOnce(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pollVideoJobsOnce did not return within 2s — hung job blocked the whole tick")
	}

	if j, _ := store.Get(fastJob.ID); j.State != "complete" {
		t.Errorf("expected fast job to complete while hung job was timing out, got %s", j.State)
	}
	if hj, _ := store.Get(hungJob.ID); hj.State != "processing" {
		t.Errorf("expected hung job to remain processing (transient timeout, retry next tick), got %s", hj.State)
	}

	close(slow.block)
}

// TestXAIVideoProvider_HTTPClientTimeout proves the provider no longer uses
// http.DefaultClient (which has no timeout) and honors its client deadline.
func TestXAIVideoProvider_HTTPClientTimeout(t *testing.T) {
	def := NewXAIVideoProvider("k")
	if def.http.Timeout == 0 {
		t.Error("default xAI video HTTP client must have a non-zero timeout (was http.DefaultClient)")
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer slow.Close()

	p := NewXAIVideoProvider("key")
	p.baseURL = slow.URL
	p.http = &http.Client{Timeout: 40 * time.Millisecond}

	start := time.Now()
	_, err := p.Submit(context.Background(), "p", "m")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected Submit to fail when the upstream stalls past the client timeout")
	}
	if elapsed > 400*time.Millisecond {
		t.Errorf("expected Submit to bail out near the client timeout (~40ms), took %v", elapsed)
	}
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
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

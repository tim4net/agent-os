package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tim4net/agent-os/internal/db"
)

// VideoProvider is an async video generation backend: submit returns an upstream
// job id immediately; FetchStatus polls the upstream until the video is ready.
type VideoProvider interface {
	Submit(ctx context.Context, prompt, model string) (jobID string, err error)
	FetchStatus(ctx context.Context, jobID string) (VideoResult, error)
}

// VideoResult is the upstream status snapshot for a video job.
type VideoResult struct {
	State    string // "pending" | "complete" | "failed"
	VideoURL string // populated when State == "complete"
	ErrMsg   string // populated when State == "failed"
}

// XAIVideoProvider generates video via xAI's video generations API.
type XAIVideoProvider struct {
	apiKey  string
	baseURL string // default "https://api.x.ai"; overridable for tests
	http    *http.Client
}

// videoHTTPTimeout bounds every xAI video API call so a stalled upstream
// response cannot block Submit/FetchStatus indefinitely (http.DefaultClient has
// no timeout).
const videoHTTPTimeout = 30 * time.Second

func NewXAIVideoProvider(apiKey string) *XAIVideoProvider {
	return &XAIVideoProvider{
		apiKey:  apiKey,
		baseURL: "https://api.x.ai",
		http:    &http.Client{Timeout: videoHTTPTimeout},
	}
}

type xaiSubmitRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type xaiSubmitResponse struct {
	ID string `json:"id"`
	// Note: field names should be confirmed against xAI docs when credits are available
}

func (p *XAIVideoProvider) Submit(ctx context.Context, prompt, model string) (string, error) {
	if p.apiKey == "" {
		return "", fmt.Errorf("XAI_API_KEY not configured")
	}

	if model == "" {
		model = "grok-2-video"
	}

	reqBody := xaiSubmitRequest{
		Model:  model,
		Prompt: prompt,
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/video/generations", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("xai video submit failed: status %d", resp.StatusCode)
	}

	var res xaiSubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	return res.ID, nil
}

type xaiFetchResponse struct {
	ID    string `json:"id"`
	State string `json:"state"` // "pending" | "complete" | "failed"
	Video struct {
		URL string `json:"url"`
	} `json:"video"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *XAIVideoProvider) FetchStatus(ctx context.Context, jobID string) (VideoResult, error) {
	if p.apiKey == "" {
		return VideoResult{}, fmt.Errorf("XAI_API_KEY not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v1/video/generations/"+jobID, nil)
	if err != nil {
		return VideoResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.http.Do(req)
	if err != nil {
		return VideoResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return VideoResult{}, fmt.Errorf("xai video fetch failed: status %d", resp.StatusCode)
	}

	var res xaiFetchResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return VideoResult{}, err
	}

	var vr VideoResult
	switch res.State {
	case "complete":
		vr.State = "complete"
		vr.VideoURL = res.Video.URL
	case "failed":
		vr.State = "failed"
		vr.ErrMsg = res.Error.Message
	default:
		vr.State = "pending"
	}
	return vr, nil
}

// VideoJob is an in-memory representation of a video generation job.
type VideoJob struct {
	ID         string // internal uuid (uuid.NewString())
	Prompt     string
	Model      string
	Provider   string
	State      string // "queued" | "processing" | "complete" | "failed"
	Progress   int    // 0..100 (rough: queued=0, processing=50, complete=100)
	UpstreamID string // jobID from provider.Submit
	VideoURL   string // final remote video url
	ArtifactID string // set after asset pipeline stores it
	Error      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// videoJobMaxActive caps the number of jobs retained in memory. Completed/failed
// jobs are evicted oldest-first before this cap bites, so a flood of submissions
// cannot grow the store without bound.
const videoJobMaxActive = 500

// videoJobTerminalTTL is how long a completed/failed job is kept after it reaches
// a terminal state before the poller reaps it.
const videoJobTerminalTTL = 1 * time.Hour

// VideoJobStore is an in-memory, thread-safe store for video jobs.
type VideoJobStore struct {
	mu        sync.RWMutex
	jobs      map[string]*VideoJob
	maxActive int
}

func NewVideoJobStore() *VideoJobStore {
	return &VideoJobStore{
		jobs:      make(map[string]*VideoJob),
		maxActive: videoJobMaxActive,
	}
}

func (s *VideoJobStore) Create(prompt, model, provider string) *VideoJob {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Make room before adding: evict terminal jobs oldest-first if at the cap.
	if len(s.jobs) >= s.maxActive {
		s.evictTerminalLocked()
	}
	// Still at the cap (everything is active) — refuse rather than grow unbounded.
	if len(s.jobs) >= s.maxActive {
		return nil
	}

	job := &VideoJob{
		ID:        uuid.NewString(),
		Prompt:    prompt,
		Model:     model,
		Provider:  provider,
		State:     "queued",
		Progress:  0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	s.jobs[job.ID] = job

	jobCopy := *job
	return &jobCopy
}

func (s *VideoJobStore) Get(id string) (*VideoJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	job, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	jobCopy := *job
	return &jobCopy, true
}

// evictTerminalLocked removes completed/failed jobs, oldest first, until the
// store is below the cap. Caller must hold s.mu in write mode.
func (s *VideoJobStore) evictTerminalLocked() {
	type kv struct {
		id string
		ts time.Time
	}
	var terminal []kv
	for id, j := range s.jobs {
		if j.State == "complete" || j.State == "failed" {
			terminal = append(terminal, kv{id, j.CreatedAt})
		}
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].ts.Before(terminal[j].ts) })
	for len(s.jobs) >= s.maxActive && len(terminal) > 0 {
		delete(s.jobs, terminal[0].id)
		terminal = terminal[1:]
	}
}

// ReapTerminal removes completed/failed jobs whose UpdatedAt is older than maxAge
// relative to now. Returns the number removed. Called by the poller each tick so
// terminal jobs do not live in memory forever.
func (s *VideoJobStore) ReapTerminal(now time.Time, maxAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for id, j := range s.jobs {
		if (j.State == "complete" || j.State == "failed") && now.Sub(j.UpdatedAt) > maxAge {
			delete(s.jobs, id)
			removed++
		}
	}
	return removed
}

func (s *VideoJobStore) SetProcessing(id, upstreamID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if job, ok := s.jobs[id]; ok {
		job.State = "processing"
		job.Progress = 50
		job.UpstreamID = upstreamID
		job.UpdatedAt = time.Now()
	}
}

func (s *VideoJobStore) Complete(id, videoURL, artifactID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if job, ok := s.jobs[id]; ok {
		job.State = "complete"
		job.Progress = 100
		job.VideoURL = videoURL
		job.ArtifactID = artifactID
		job.UpdatedAt = time.Now()
	}
}

func (s *VideoJobStore) Fail(id, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if job, ok := s.jobs[id]; ok {
		job.State = "failed"
		job.Error = errMsg
		job.UpdatedAt = time.Now()
	}
}

// VideoFinalizer stores a completed video via the asset pipeline and returns the artifact id.
type VideoFinalizer interface {
	Store(ctx context.Context, job *VideoJob, videoURL string) (artifactID string, err error)
}

type studioVideoFinalizer struct {
	api *StudioAPI
}

func (f *studioVideoFinalizer) Store(ctx context.Context, job *VideoJob, videoURL string) (string, error) {
	fileData, err := downloadFile(ctx, videoURL)
	if err != nil {
		return "", err
	}

	ext := detectExtension(videoURL, "video")
	if ext == "" {
		ext = ".mp4"
	}
	relativePath := filepath.Join("studio", job.ID+ext)
	fullPath := filepath.Join(f.api.artifactsPath, relativePath)

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(fullPath, fileData, 0644); err != nil {
		return "", err
	}

	mimeType := typeToMime("video")
	title := fmt.Sprintf("Generated video: %s", truncate(job.Prompt, 50))

	metadata := map[string]any{
		"prompt":     job.Prompt,
		"model":      job.Model,
		"provider":   job.Provider,
		"source_url": videoURL,
	}
	metaBytes, _ := json.Marshal(metadata)

	artifact, err := f.api.queries.CreateArtifact(ctx, db.CreateArtifactParams{
		Type:        "video",
		Title:       pgtype.Text{String: title, Valid: true},
		Description: pgtype.Text{String: job.Prompt, Valid: true},
		FilePath:    pgtype.Text{String: relativePath, Valid: relativePath != ""},
		MimeType:    pgtype.Text{String: mimeType, Valid: mimeType != ""},
		Metadata:    metaBytes,
	})
	if err != nil {
		return "", err
	}

	return artifact.ID.String(), nil
}

const videoPollInterval = 2 * time.Second

// videoPollConcurrency bounds how many jobs are polled in parallel each tick, so
// one slow upstream cannot starve the jobs queued behind it.
const videoPollConcurrency = 8

// videoPollCallTimeout bounds each individual upstream status/finalize call made
// during a poll tick (in addition to the provider's own HTTP client timeout).
const videoPollCallTimeout = 15 * time.Second

// Close stops the background video poller goroutine. It is safe to call multiple
// times (idempotent via sync.Once) and from the server graceful-stop path. Tests
// that construct StudioAPI without starting the poller can call this harmlessly.
func (s *StudioAPI) Close() {
	s.stopVideoPollerOnce.Do(func() {
		if s.stopVideoPoller != nil {
			close(s.stopVideoPoller)
		}
	})
}

func (s *StudioAPI) runVideoPoller() {
	ticker := time.NewTicker(videoPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopVideoPoller:
			return
		case <-ticker.C:
			s.pollVideoJobsOnce(context.Background())
		}
	}
}

func (s *StudioAPI) pollVideoJobsOnce(ctx context.Context) {
	if s.videoJobs == nil || len(s.videoProviders) == 0 {
		return
	}

	// Reap terminal jobs that have aged out so the store stays bounded.
	s.videoJobs.ReapTerminal(time.Now(), videoJobTerminalTTL)

	// Gather processing jobs under a read lock (no HTTP calls while locked).
	s.videoJobs.mu.RLock()
	processing := make([]*VideoJob, 0)
	for _, job := range s.videoJobs.jobs {
		if job.State == "processing" {
			jobCopy := *job
			processing = append(processing, &jobCopy)
		}
	}
	s.videoJobs.mu.RUnlock()

	if len(processing) == 0 {
		return
	}

	// Poll concurrently with a bounded worker pool and a per-call deadline so a
	// single hung upstream call cannot block the whole tick.
	sem := make(chan struct{}, videoPollConcurrency)
	var wg sync.WaitGroup
	for _, job := range processing {
		wg.Add(1)
		sem <- struct{}{}
		go func(job *VideoJob) {
			defer wg.Done()
			defer func() { <-sem }()
			s.pollOneJob(ctx, job)
		}(job)
	}
	wg.Wait()
}

// pollOneJob checks a single processing job against its provider with a per-call
// timeout, then finalizes or fails it. State mutations go through the store's
// lock-protected methods, so concurrent calls are safe.
func (s *StudioAPI) pollOneJob(parent context.Context, job *VideoJob) {
	provider, ok := s.videoProviders[job.Provider]
	if !ok {
		s.videoJobs.Fail(job.ID, "unknown provider")
		return
	}

	timeout := s.pollCallTimeout
	if timeout <= 0 {
		timeout = videoPollCallTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	res, err := provider.FetchStatus(ctx, job.UpstreamID)
	if err != nil {
		// Don't fail the job on a transient network error; retry next tick.
		logf("studio: video poller failed to fetch status for job %s: %v", job.ID, err)
		return
	}

	switch res.State {
	case "complete":
		if s.videoFinalizer != nil {
			artifactID, err := s.videoFinalizer.Store(ctx, job, res.VideoURL)
			if err != nil {
				s.videoJobs.Fail(job.ID, "failed to store video: "+err.Error())
			} else {
				s.videoJobs.Complete(job.ID, res.VideoURL, artifactID)
			}
		} else {
			s.videoJobs.Complete(job.ID, res.VideoURL, "")
		}
	case "failed":
		s.videoJobs.Fail(job.ID, res.ErrMsg)
	}
}

type videoJobResponse struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Progress int    `json:"progress"`
	VideoURL string `json:"video_url,omitempty"`
	Error    string `json:"error,omitempty"`
}

func toVideoJobResponse(j *VideoJob) videoJobResponse {
	resp := videoJobResponse{
		ID:       j.ID,
		State:    j.State,
		Progress: j.Progress,
		Error:    j.Error,
	}
	if j.ArtifactID != "" {
		resp.VideoURL = fmt.Sprintf("/api/artifacts/%s/file", j.ArtifactID)
	} else if j.VideoURL != "" {
		resp.VideoURL = j.VideoURL
	}
	return resp
}

func (s *StudioAPI) SubmitVideoJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt   string `json:"prompt"`
		Model    string `json:"model"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	providerName := req.Provider
	if providerName == "" {
		providerName = "grok-video"
	}

	if len(s.videoProviders) == 0 || s.videoJobs == nil {
		http.Error(w, "video generation is not supported", http.StatusBadRequest)
		return
	}

	provider, ok := s.videoProviders[providerName]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown video provider: %s", providerName), http.StatusBadRequest)
		return
	}

	info, infoOk := s.providerInfo[providerName]
	if infoOk && !info.Available {
		http.Error(w, fmt.Sprintf("provider %s is not available (missing API key)", providerName), http.StatusBadRequest)
		return
	}

	upstreamID, err := provider.Submit(r.Context(), req.Prompt, req.Model)
	if err != nil {
		http.Error(w, "upstream submit failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	job := s.videoJobs.Create(req.Prompt, req.Model, providerName)
	if job == nil {
		http.Error(w, "video job queue is full, try again shortly", http.StatusServiceUnavailable)
		return
	}
	s.videoJobs.SetProcessing(job.ID, upstreamID)

	// Fetch updated state
	updatedJob, _ := s.videoJobs.Get(job.ID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toVideoJobResponse(updatedJob))
}

func (s *StudioAPI) GetVideoJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if s.videoJobs == nil {
		http.Error(w, "video generation is not supported", http.StatusNotFound)
		return
	}

	job, ok := s.videoJobs.Get(id)
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toVideoJobResponse(job))
}

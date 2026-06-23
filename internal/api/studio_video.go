package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

func NewXAIVideoProvider(apiKey string) *XAIVideoProvider {
	return &XAIVideoProvider{
		apiKey:  apiKey,
		baseURL: "https://api.x.ai",
		http:    http.DefaultClient,
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
	ID         string    // internal uuid (uuid.NewString())
	Prompt     string
	Model      string
	Provider   string
	State      string    // "queued" | "processing" | "complete" | "failed"
	Progress   int       // 0..100 (rough: queued=0, processing=50, complete=100)
	UpstreamID string    // jobID from provider.Submit
	VideoURL   string    // final remote video url
	ArtifactID string    // set after asset pipeline stores it
	Error      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// VideoJobStore is an in-memory, thread-safe store for video jobs.
type VideoJobStore struct {
	mu   sync.RWMutex
	jobs map[string]*VideoJob
}

func NewVideoJobStore() *VideoJobStore {
	return &VideoJobStore{
		jobs: make(map[string]*VideoJob),
	}
}

func (s *VideoJobStore) Create(prompt, model, provider string) *VideoJob {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	// Iterate over jobs to find processing ones
	// To avoid holding the lock during HTTP calls, we gather them first
	s.videoJobs.mu.RLock()
	var processing []*VideoJob
	for _, job := range s.videoJobs.jobs {
		if job.State == "processing" {
			jobCopy := *job
			processing = append(processing, &jobCopy)
		}
	}
	s.videoJobs.mu.RUnlock()

	for _, job := range processing {
		provider, ok := s.videoProviders[job.Provider]
		if !ok {
			s.videoJobs.Fail(job.ID, "unknown provider")
			continue
		}

		res, err := provider.FetchStatus(ctx, job.UpstreamID)
		if err != nil {
			// Don't fail the job immediately on network error, let it retry next tick
			logf("studio: video poller failed to fetch status for job %s: %v", job.ID, err)
			continue
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

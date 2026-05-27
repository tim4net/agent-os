package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
)

// VoiceTranscribeResponse is the JSON response for the transcribe endpoint.
type VoiceTranscribeResponse struct {
	Text string `json:"text"`
}

// VoiceSynthesizeRequest is the JSON body for the synthesize endpoint.
type VoiceSynthesizeRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
}

// Transcribe handles POST /api/voice/transcribe.
// It accepts a multipart audio file (webm/wav), forwards it to the Whisper
// endpoint on the LiteLLM proxy, and returns the transcribed text.
func (a *API) Transcribe(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form from the client (max 32 MB)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "failed to parse multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file in form", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read the uploaded file bytes
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "failed to read audio file", http.StatusInternalServerError)
		return
	}

	// Build a new multipart form to forward to LiteLLM
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", header.Filename)
	if err != nil {
		http.Error(w, "failed to create form file", http.StatusInternalServerError)
		return
	}
	if _, err := part.Write(fileBytes); err != nil {
		http.Error(w, "failed to write audio data", http.StatusInternalServerError)
		return
	}

	// Set the model for Whisper
	_ = writer.WriteField("model", "whisper-1")
	_ = writer.WriteField("response_format", "json")

	if err := writer.Close(); err != nil {
		http.Error(w, "failed to close multipart writer", http.StatusInternalServerError)
		return
	}

	// POST to LiteLLM proxy Whisper endpoint
	litellmURL := a.litellmURL + "/v1/audio/transcriptions"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, litellmURL, &buf)
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("voice: whisper request failed", "error", err)
		http.Error(w, "transcription request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Error("voice: whisper returned non-200", "status", resp.StatusCode, "body", string(body))
		http.Error(w, fmt.Sprintf("transcription failed: upstream status %d", resp.StatusCode), resp.StatusCode)
		return
	}

	// Parse the Whisper response to extract the text
	var whisperResp struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&whisperResp); err != nil {
		http.Error(w, "failed to decode transcription response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(VoiceTranscribeResponse{Text: whisperResp.Text})
}

// Synthesize handles POST /api/voice/synthesize.
// It accepts JSON {"text": "...", "voice": "alloy"}, calls the TTS endpoint
// on the LiteLLM proxy, and returns the audio bytes directly.
func (a *API) Synthesize(w http.ResponseWriter, r *http.Request) {
	var req VoiceSynthesizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}

	if req.Voice == "" {
		req.Voice = "alloy"
	}

	// Build the TTS request body (OpenAI-compatible)
	ttsBody := map[string]string{
		"model":           "tts-1",
		"input":           req.Text,
		"voice":           req.Voice,
		"response_format": "mp3",
	}
	bodyBytes, err := json.Marshal(ttsBody)
	if err != nil {
		http.Error(w, "failed to marshal request", http.StatusInternalServerError)
		return
	}

	// POST to LiteLLM proxy TTS endpoint
	litellmURL := a.litellmURL + "/v1/audio/speech"
	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, litellmURL, bytes.NewReader(bodyBytes))
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		slog.Error("voice: TTS request failed", "error", err)
		http.Error(w, "speech synthesis request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Error("voice: TTS returned non-200", "status", resp.StatusCode, "body", string(body))
		http.Error(w, fmt.Sprintf("speech synthesis failed: upstream status %d", resp.StatusCode), resp.StatusCode)
		return
	}

	// Stream the audio bytes back to the client
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "audio/mpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body)
}

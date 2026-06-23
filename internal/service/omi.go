package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// This file implements the Omi (Based Hardware) ingest source.
//
// The Omi wearable captures ambient audio and, via the Omi cloud, produces
// transcripts + structured summaries ("memories"). This adapter fetches those
// memories from the Omi REST API so they can be ingested into the agent-os
// memory index as ambient context.
//
// The integration is opt-in: the OmiIngester background service is only started
// by cmd/server when an OMI_API_TOKEN is configured (see issue #135 —
// "deferred behind higher-priority work").

// DefaultOmiBaseURL is the Omi cloud REST API root.
const DefaultOmiBaseURL = "https://api.omi.dev"

// OmiMemory is the normalized, source-agnostic representation of a single Omi
// device transcript/note. It is decoupled from both the wire JSON shape and the
// db.MemoryIndex row so the mapping logic can be unit-tested in isolation.
type OmiMemory struct {
	ID          string    // Omi memory id (stable across syncs)
	CreatedAt   time.Time // when the recording was captured
	Title       string    // structured title (may be empty)
	Overview    string    // structured summary (may be empty)
	Transcript  string    // assembled transcript text
	ActionItems []string  // structured action items
	Tags        []string  // user/system tags from Omi
}

// OmiSource is the pluggable interface for fetching Omi device data. The
// production implementation (OmiClient) talks to the Omi cloud REST API; tests
// inject a fake.
type OmiSource interface {
	// ListSince fetches Omi memories created strictly after `since`.
	// A zero since means "all available" (initial backfill).
	// Results are returned in ascending CreatedAt order so the caller can
	// safely advance a high-water mark.
	ListSince(ctx context.Context, since time.Time) ([]OmiMemory, error)
}

// ---------------------------------------------------------------------------
// OmiClient — production OmiSource backed by the Omi cloud REST API.
// ---------------------------------------------------------------------------

// OmiClient fetches memories from the Omi cloud REST API.
type OmiClient struct {
	baseURL string
	token   string
	http    *http.Client
	limit   int // page size for /v3/memories
}

// NewOmiClient creates a client for the Omi cloud API. If baseURL is empty the
// DefaultOmiBaseURL is used. A nil http.Client is replaced with a default.
func NewOmiClient(baseURL, token string, httpClient *http.Client) *OmiClient {
	if baseURL == "" {
		baseURL = DefaultOmiBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &OmiClient{
		baseURL: baseURL,
		token:   token,
		http:    httpClient,
		limit:   200,
	}
}

// omiMemoriesResponse is the v3 envelope. Some Omi API versions return the
// array directly (no wrapper); decodeMemories handles both.
type omiMemoriesResponse struct {
	Memories []omiWireMemory `json:"memories"`
}

// omiWireMemory mirrors the subset of the Omi memory JSON we care about.
// Unknown fields are ignored, making the parser resilient to API additions.
type omiWireMemory struct {
	ID        string             `json:"id"`
	CreatedAt string             `json:"created_at"`
	Structured omiWireStructured `json:"structured"`
	Transcript []omiWireSegment `json:"transcript"`
	Tags      []string          `json:"tags"`
}

type omiWireStructured struct {
	Title       string   `json:"title"`
	Overview    string   `json:"overview"`
	ActionItems []string `json:"action_items"`
}

type omiWireSegment struct {
	Text string `json:"text"`
}

// ListSince implements OmiSource. It pages through /v3/memories (newest-first)
// and returns memories created strictly after `since`, re-sorted ascending.
func (c *OmiClient) ListSince(ctx context.Context, since time.Time) ([]OmiMemory, error) {
	if c.token == "" {
		return nil, fmt.Errorf("omi: missing API token")
	}

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("omi: invalid base url: %w", err)
	}
	u = u.JoinPath("/v3/memories")
	q := u.Query()
	q.Set("limit", strconv.Itoa(c.limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("omi: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("omi: unexpected status %d", resp.StatusCode)
	}

	wire, err := decodeMemories(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("omi: decode: %w", err)
	}

	out := make([]OmiMemory, 0, len(wire))
	for _, w := range wire {
		m := normalizeOmiMemory(w)
		if m.ID == "" {
			continue // skip malformed entries without a stable id
		}
		// The API returns newest-first; keep only items strictly newer than
		// the high-water mark so we never re-process a memory twice.
		if !since.IsZero() && !m.CreatedAt.After(since) {
			continue
		}
		out = append(out, m)
	}
	// Sort ascending by CreatedAt so callers can advance a high-water mark.
	sortOmiAscending(out)
	return out, nil
}

// decodeMemories accepts both the {"memories":[...]} envelope and a bare array.
func decodeMemories(r io.Reader) ([]omiWireMemory, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var env omiMemoriesResponse
	if err := json.Unmarshal(buf, &env); err == nil && env.Memories != nil {
		return env.Memories, nil
	}
	var arr []omiWireMemory
	if err := json.Unmarshal(buf, &arr); err != nil {
		return nil, err
	}
	return arr, nil
}

// normalizeOmiMemory converts a wire record into the normalized domain type.
func normalizeOmiMemory(w omiWireMemory) OmiMemory {
	m := OmiMemory{
		ID:          w.ID,
		Title:       w.Structured.Title,
		Overview:    w.Structured.Overview,
		ActionItems: append([]string(nil), w.Structured.ActionItems...),
		Tags:        append([]string(nil), w.Tags...),
	}
	m.CreatedAt = parseOmiTime(w.CreatedAt)
	m.Transcript = assembleTranscript(w.Transcript)
	return m
}

// parseOmiTime parses the ISO-8601 timestamps Omi emits. A blank or unparseable
// timestamp yields the zero value; callers should treat that as "unknown".
func parseOmiTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// assembleTranscript joins transcript segment texts with spaces. Whitespace-only
// segments are skipped so a transcript never degrades to stray spaces.
func assembleTranscript(segs []omiWireSegment) string {
	if len(segs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		// Trim leading/trailing whitespace and drop blanks entirely; a segment
		// that is only spaces would otherwise inject stray spaces into the body.
		if t := strings.TrimSpace(s.Text); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}

// sortOmiAscending orders memories oldest-first by CreatedAt (stable on ties).
func sortOmiAscending(ms []OmiMemory) {
	// Simple insertion sort: memory sets per sync cycle are small (≤200) and
	// this avoids pulling in sort.Slice for a self-contained adapter.
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && ms[j-1].CreatedAt.After(ms[j].CreatedAt); j-- {
			ms[j-1], ms[j] = ms[j], ms[j-1]
		}
	}
}

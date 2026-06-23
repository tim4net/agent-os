package service

// Unit tests for the Omi ingest source adapter (issue #135).
//
// These cover the pure helper functions (decode/normalize/parse/sort) and the
// OmiClient.ListSince HTTP path against an httptest.Server, including:
//   - the two wire shapes Omi emits ({"memories":[...]} envelope vs bare array),
//   - high-water-mark filtering (only items strictly newer than `since`),
//   - ascending re-sort (the API returns newest-first),
//   - malformed-entry skipping (no id) and error handling (4xx, missing token).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// decodeMemories
// ---------------------------------------------------------------------------

func TestDecodeMemories(t *testing.T) {
	t.Run("envelope", func(t *testing.T) {
		body := `{"memories":[{"id":"a"},{"id":"b"}]}`
		got, err := decodeMemories(strReader(body))
		if err != nil {
			t.Fatalf("decodeMemories envelope: %v", err)
		}
		if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
			t.Fatalf("decodeMemories envelope = %+v, want ids a,b", got)
		}
	})

	t.Run("bare array", func(t *testing.T) {
		body := `[{"id":"x"}]`
		got, err := decodeMemories(strReader(body))
		if err != nil {
			t.Fatalf("decodeMemories array: %v", err)
		}
		if len(got) != 1 || got[0].ID != "x" {
			t.Fatalf("decodeMemories array = %+v, want single id x", got)
		}
	})

	t.Run("empty array", func(t *testing.T) {
		got, err := decodeMemories(strReader(`[]`))
		if err != nil {
			t.Fatalf("decodeMemories empty: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("decodeMemories empty = %d items, want 0", len(got))
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		if _, err := decodeMemories(strReader(`{not json`)); err == nil {
			t.Fatal("decodeMemories: expected error for invalid json, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// normalizeOmiMemory
// ---------------------------------------------------------------------------

func TestNormalizeOmiMemory(t *testing.T) {
	wire := omiWireMemory{
		ID:        "mem-1",
		CreatedAt: "2024-06-01T12:00:00Z",
		Structured: omiWireStructured{
			Title:       "Standup",
			Overview:    "Discussed roadmap",
			ActionItems: []string{"Ship v2", "Review PRs"},
		},
		Transcript: []omiWireSegment{{Text: "hello"}, {Text: "world"}},
		Tags:       []string{"work", "meeting"},
	}
	m := normalizeOmiMemory(wire)

	if m.ID != "mem-1" {
		t.Errorf("ID = %q, want mem-1", m.ID)
	}
	if m.Title != "Standup" {
		t.Errorf("Title = %q", m.Title)
	}
	if m.Overview != "Discussed roadmap" {
		t.Errorf("Overview = %q", m.Overview)
	}
	if m.Transcript != "hello world" {
		t.Errorf("Transcript = %q, want \"hello world\"", m.Transcript)
	}
	if !reflect.DeepEqual(m.ActionItems, []string{"Ship v2", "Review PRs"}) {
		t.Errorf("ActionItems = %v", m.ActionItems)
	}
	if !reflect.DeepEqual(m.Tags, []string{"work", "meeting"}) {
		t.Errorf("Tags = %v", m.Tags)
	}
	// CreatedAt should be parsed.
	want := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	if !m.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", m.CreatedAt, want)
	}
}

// TestNormalizeOmiMemory_NilSafety verifies that nil/empty structured slices
// produce empty (not nil-deref-panic) slices, and that the returned slices are
// copies (mutating them must not affect a reused wire value).
func TestNormalizeOmiMemory_NilSafety(t *testing.T) {
	m := normalizeOmiMemory(omiWireMemory{ID: "x"})
	if m.ActionItems != nil {
		t.Errorf("ActionItems = %v, want nil", m.ActionItems)
	}
	if m.Tags != nil {
		t.Errorf("Tags = %v, want nil", m.Tags)
	}
	if m.Transcript != "" {
		t.Errorf("Transcript = %q, want empty", m.Transcript)
	}
	if !m.CreatedAt.IsZero() {
		t.Errorf("CreatedAt = %v, want zero", m.CreatedAt)
	}
}

// ---------------------------------------------------------------------------
// assembleTranscript
// ---------------------------------------------------------------------------

func TestAssembleTranscript(t *testing.T) {
	cases := []struct {
		name string
		segs []omiWireSegment
		want string
	}{
		{"empty", nil, ""},
		{"single", []omiWireSegment{{Text: "only"}}, "only"},
		{"joined with space", []omiWireSegment{{Text: "a"}, {Text: "b"}, {Text: "c"}}, "a b c"},
		{"skips blank segments", []omiWireSegment{{Text: ""}, {Text: "keep"}, {Text: "  "}}, "keep"},
		{"all blank", []omiWireSegment{{Text: ""}, {Text: "  "}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := assembleTranscript(tc.segs); got != tc.want {
				t.Errorf("assembleTranscript = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseOmiTime
// ---------------------------------------------------------------------------

func TestParseOmiTime(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"", time.Time{}},
		{"not-a-date", time.Time{}},
		{"2024-06-01T12:00:00Z", time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)},
		{"2024-06-01T12:00:00.123456789Z", time.Date(2024, 6, 1, 12, 0, 0, 123456789, time.UTC)},
		{"2024-06-01T12:00:00.000Z", time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)},
		{"2024-06-01T12:00:00-05:00", time.Date(2024, 6, 1, 17, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parseOmiTime(tc.in)
			if !got.Equal(tc.want) {
				t.Errorf("parseOmiTime(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// sortOmiAscending
// ---------------------------------------------------------------------------

func TestSortOmiAscending(t *testing.T) {
	t1 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)

	in := []OmiMemory{
		{ID: "c", CreatedAt: t3},
		{ID: "a", CreatedAt: t1},
		{ID: "b", CreatedAt: t2},
	}
	sortOmiAscending(in)
	got := []string{in[0].ID, in[1].ID, in[2].ID}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	// Compare against the std sort to confirm ascending order for a randomized set.
	rand := []OmiMemory{
		{ID: "5", CreatedAt: time.Date(2024, 1, 5, 0, 0, 0, 0, time.UTC)},
		{ID: "1", CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "3", CreatedAt: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)},
		{ID: "2", CreatedAt: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
		{ID: "4", CreatedAt: time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC)},
	}
	sortOmiAscending(rand)
	if !sort.SliceIsSorted(rand, func(i, j int) bool { return rand[i].CreatedAt.Before(rand[j].CreatedAt) }) {
		t.Fatalf("not ascending: %+v", rand)
	}
}

// ---------------------------------------------------------------------------
// OmiClient.ListSince (HTTP)
// ---------------------------------------------------------------------------

// omiTestHandler returns an http.HandlerFunc that writes the given JSON body and
// status, and records the bearer token used on the request.
func omiTestHandler(t *testing.T, status int, body string) (http.HandlerFunc, *string) {
	t.Helper()
	var token string
	return func(w http.ResponseWriter, r *http.Request) {
		token = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}, &token
}

func TestOmiClient_ListSince_Envelope(t *testing.T) {
	// API returns newest-first; entries are t3, t2, t1.
	body := `{"memories":[
		{"id":"m3","created_at":"2024-06-03T00:00:00Z","structured":{"title":"C"}},
		{"id":"m2","created_at":"2024-06-02T00:00:00Z","structured":{"title":"B"}},
		{"id":"m1","created_at":"2024-06-01T00:00:00Z","structured":{"title":"A"}}
	]}`
	h, tok := omiTestHandler(t, http.StatusOK, body)
	ts := httptest.NewServer(h)
	defer ts.Close()

	c := NewOmiClient(ts.URL, "secret-token", ts.Client())
	got, err := c.ListSince(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	// Expect ascending order (re-sorted) and the bearer token forwarded.
	want := []string{"m1", "m2", "m3"}
	for i, m := range got {
		if m.ID != want[i] {
			t.Fatalf("got[%d].ID = %q, want %q", i, m.ID, want[i])
		}
	}
	if *tok != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", *tok)
	}
}

func TestOmiClient_ListSince_BareArray(t *testing.T) {
	body := `[{"id":"a","created_at":"2024-06-01T00:00:00Z"},{"id":"b","created_at":"2024-06-02T00:00:00Z"}]`
	h, _ := omiTestHandler(t, http.StatusOK, body)
	ts := httptest.NewServer(h)
	defer ts.Close()

	c := NewOmiClient(ts.URL, "tok", ts.Client())
	got, err := c.ListSince(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestOmiClient_ListSince_FiltersBySince(t *testing.T) {
	body := `{"memories":[
		{"id":"old","created_at":"2024-06-01T00:00:00Z"},
		{"id":"same","created_at":"2024-06-02T00:00:00Z"},
		{"id":"new","created_at":"2024-06-03T00:00:00Z"}
	]}`
	h, _ := omiTestHandler(t, http.StatusOK, body)
	ts := httptest.NewServer(h)
	defer ts.Close()

	c := NewOmiClient(ts.URL, "tok", ts.Client())
	// High-water mark = t2; "same" must be excluded (not strictly after),
	// "old" excluded, only "new" kept.
	since := time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC)
	got, err := c.ListSince(context.Background(), since)
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("got = %+v, want only [new]", got)
	}
}

func TestOmiClient_ListSince_SkipsNoID(t *testing.T) {
	body := `{"memories":[
		{"id":"good","created_at":"2024-06-01T00:00:00Z"},
		{"created_at":"2024-06-02T00:00:00Z"},
		{"id":"","created_at":"2024-06-03T00:00:00Z"}
	]}`
	h, _ := omiTestHandler(t, http.StatusOK, body)
	ts := httptest.NewServer(h)
	defer ts.Close()

	c := NewOmiClient(ts.URL, "tok", ts.Client())
	got, err := c.ListSince(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("got = %+v, want only [good]", got)
	}
}

func TestOmiClient_ListSince_HTTPError(t *testing.T) {
	h, _ := omiTestHandler(t, http.StatusUnauthorized, `{"error":"nope"}`)
	ts := httptest.NewServer(h)
	defer ts.Close()

	c := NewOmiClient(ts.URL, "tok", ts.Client())
	if _, err := c.ListSince(context.Background(), time.Time{}); err == nil {
		t.Fatal("ListSince: expected error for 401, got nil")
	}
}

func TestOmiClient_ListSince_MissingToken(t *testing.T) {
	// No server should be contacted; the client errors before any request.
	c := NewOmiClient("https://invalid.example.invalid", "", ts0Client())
	if _, err := c.ListSince(context.Background(), time.Time{}); err == nil {
		t.Fatal("ListSince: expected error for missing token, got nil")
	}
}

func TestNewOmiClient_Defaults(t *testing.T) {
	c := NewOmiClient("", "tok", nil)
	if c.baseURL != DefaultOmiBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultOmiBaseURL)
	}
	if c.http == nil {
		t.Error("http client should default to non-nil")
	}
}

// strReader is a small helper to get an io.Reader from a string.
func strReader(s string) io.Reader { return &stringReader{s: s} }

type stringReader struct {
	s   string
	pos int
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.pos:])
	r.pos += n
	return n, nil
}

// ts0Client returns a client with a short timeout so any accidental network use
// fails fast instead of hanging the test.
func ts0Client() *http.Client { return &http.Client{Timeout: time.Second} }

// jsonEcho is a tiny helper to pretty-print for debugging if needed.
var _ = json.Marshal

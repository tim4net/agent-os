package service

// Unit tests for the OmiIngester background poller (issue #135).
//
// The ingester depends on two narrow interfaces (OmiSource, memoryWriter), so
// we inject fakes and drive it deterministically via Sync() — no live network,
// no Postgres. Coverage:
//   - pure mapping (omiMemoryToParams): deterministic file_path, tag injection,
//     title fallback, content assembly.
//   - buildOmiContent: empty sections omitted.
//   - ensureTag: idempotent + case-insensitive.
//   - Sync: ingest + high-water-mark advance, incremental fetch, source-error
//     propagation, and partial-upsert-failure resilience (count successes,
//     advance mark to newest success).

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tim4net/agent-os/internal/db"
)

// ---------------------------------------------------------------------------
// Pure-function mapping
// ---------------------------------------------------------------------------

func TestOmiMemoryToParams(t *testing.T) {
	owner := systemOwnerUUID()
	ts := time.Date(2024, 6, 1, 9, 30, 0, 0, time.UTC)

	m := OmiMemory{
		ID:          "abc-123",
		CreatedAt:   ts,
		Title:       "Standup",
		Overview:    "Discussed the plan",
		Transcript:  "hello world",
		ActionItems: []string{"Do X"},
		Tags:        []string{"meeting"},
	}
	p := omiMemoryToParams(m, owner)

	if p.OwnerID != owner {
		t.Errorf("OwnerID = %v, want %v", p.OwnerID, owner)
	}
	// Deterministic file_path derived from the Omi id → idempotent upserts.
	if p.FilePath != omiFilePathPrefix+"abc-123" {
		t.Errorf("FilePath = %q, want omi://memories/abc-123", p.FilePath)
	}
	if p.Title.String != "Standup" || !p.Title.Valid {
		t.Errorf("Title = %+v, want Standup", p.Title)
	}
	if !p.Content.Valid || !strings.Contains(p.Content.String, "Discussed the plan") {
		t.Errorf("Content missing overview: %q", p.Content.String)
	}
	// Not associated to a project by default → zero UUID (Valid=false).
	if p.ProjectID.Valid {
		t.Errorf("ProjectID = %v, want invalid (zero) by default", p.ProjectID)
	}
	// Tags must include the source tags plus the omi + ambient markers.
	if !containsString(p.Tags, "meeting") || !containsString(p.Tags, "omi") || !containsString(p.Tags, "ambient") {
		t.Errorf("Tags = %v, want meeting+omi+ambient", p.Tags)
	}
}

func TestOmiMemoryToParams_TitleFallback(t *testing.T) {
	// Empty title → synthetic title derived from CreatedAt.
	ts := time.Date(2024, 6, 1, 9, 30, 0, 0, time.UTC)
	p := omiMemoryToParams(OmiMemory{ID: "x", CreatedAt: ts}, systemOwnerUUID())
	if !strings.Contains(p.Title.String, "2024-06-01 09:30") {
		t.Errorf("fallback Title = %q, want timestamp-derived", p.Title.String)
	}
}

func TestOmiMemoryToParams_TagsCopied(t *testing.T) {
	src := []string{"a"}
	p := omiMemoryToParams(OmiMemory{ID: "x", Tags: src}, systemOwnerUUID())
	// Mutate the params tags; the original source slice must be untouched.
	p.Tags[0] = "mutated"
	if src[0] != "a" {
		t.Errorf("omiMemoryToParams aliased the source tags slice: src=%v", src)
	}
}

// ---------------------------------------------------------------------------
// buildOmiContent
// ---------------------------------------------------------------------------

func TestBuildOmiContent(t *testing.T) {
	t.Run("all sections", func(t *testing.T) {
		m := OmiMemory{
			Overview:   "Sum",
			Transcript: "words",
			ActionItems: []string{"one", "two"},
		}
		s := buildOmiContent(m)
		for _, want := range []string{"## Summary", "Sum", "## Transcript", "words", "## Action items", "- one", "- two"} {
			if !strings.Contains(s, want) {
				t.Errorf("content missing %q in:\n%s", want, s)
			}
		}
	})

	t.Run("only transcript", func(t *testing.T) {
		s := buildOmiContent(OmiMemory{Transcript: "just this"})
		if strings.Contains(s, "## Summary") || strings.Contains(s, "## Action items") {
			t.Errorf("empty sections should be omitted:\n%s", s)
		}
		if !strings.Contains(s, "just this") {
			t.Errorf("transcript missing:\n%s", s)
		}
	})

	t.Run("empty", func(t *testing.T) {
		if s := buildOmiContent(OmiMemory{}); s != "" {
			t.Errorf("empty memory content = %q, want empty", s)
		}
	})

	t.Run("blank action items skipped", func(t *testing.T) {
		s := buildOmiContent(OmiMemory{ActionItems: []string{"", "  ", "real"}})
		if strings.Contains(s, "-  ") {
			t.Errorf("blank action items should be skipped:\n%s", s)
		}
		if !strings.Contains(s, "- real") {
			t.Errorf("real action item missing:\n%s", s)
		}
	})
}

// ---------------------------------------------------------------------------
// ensureTag
// ---------------------------------------------------------------------------

func TestEnsureTag(t *testing.T) {
	got := ensureTag([]string{"a"}, "b")
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("ensureTag append = %v", got)
	}
	// Already present → no-op (no duplicate).
	got = ensureTag([]string{"a", "B"}, "b") // case-insensitive
	if !reflect.DeepEqual(got, []string{"a", "B"}) {
		t.Errorf("ensureTag idempotent = %v", got)
	}
	// Nil slice → single-element.
	got = ensureTag(nil, "x")
	if !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("ensureTag nil = %v", got)
	}
}

// ---------------------------------------------------------------------------
// OmiIngester.Sync (integration with fakes)
// ---------------------------------------------------------------------------

// fakeOmiSource returns the configured memories that are strictly newer than the
// supplied high-water mark, ascending — mirroring the real OmiSource contract.
type fakeOmiSource struct {
	memories []OmiMemory
	calls    int
	err      error // if non-nil, ListSince returns this error
}

func (f *fakeOmiSource) ListSince(_ context.Context, since time.Time) ([]OmiMemory, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var out []OmiMemory
	for _, m := range f.memories {
		if since.IsZero() || m.CreatedAt.After(since) {
			out = append(out, m)
		}
	}
	return out, nil
}

// fakeMemoryWriter records every UpsertMemory call and can fail on a chosen
// file_path to simulate a partial batch failure.
type fakeMemoryWriter struct {
	calls   []db.UpsertMemoryParams
	failID  string // if set, UpsertMemory for this file_path's owning memory fails
	failErr error
}

func (w *fakeMemoryWriter) UpsertMemory(_ context.Context, arg db.UpsertMemoryParams) (db.MemoryIndex, error) {
	w.calls = append(w.calls, arg)
	if w.failID != "" && strings.HasSuffix(arg.FilePath, "/"+w.failID) {
		return db.MemoryIndex{}, w.failErr
	}
	return db.MemoryIndex{}, nil
}

func TestOmiIngester_Sync_IngestsAndAdvances(t *testing.T) {
	t1 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)

	src := &fakeOmiSource{memories: []OmiMemory{
		{ID: "a", CreatedAt: t1},
		{ID: "b", CreatedAt: t2},
		{ID: "c", CreatedAt: t3},
	}}
	w := &fakeMemoryWriter{}
	ing := NewOmiIngester(src, w)

	n, err := ing.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if n != 3 {
		t.Fatalf("ingested count = %d, want 3", n)
	}
	if len(w.calls) != 3 {
		t.Fatalf("writer calls = %d, want 3", len(w.calls))
	}
	if !ing.HighWaterMark().Equal(t3) {
		t.Fatalf("high-water = %v, want %v", ing.HighWaterMark(), t3)
	}
}

func TestOmiIngester_Sync_Incremental(t *testing.T) {
	t1 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC)

	src := &fakeOmiSource{memories: []OmiMemory{
		{ID: "old", CreatedAt: t1},
		{ID: "new", CreatedAt: t2},
	}}
	w := &fakeMemoryWriter{}
	ing := NewOmiIngester(src, w)

	// First sync: both are "after" zero → ingest all, mark = t2.
	n, _ := ing.Sync(context.Background())
	if n != 2 {
		t.Fatalf("first Sync count = %d, want 2", n)
	}
	if !ing.HighWaterMark().Equal(t2) {
		t.Fatalf("high-water after first = %v, want %v", ing.HighWaterMark(), t2)
	}

	// Second sync: mark is now t2, nothing is strictly newer → 0 new.
	n, _ = ing.Sync(context.Background())
	if n != 0 {
		t.Fatalf("incremental Sync count = %d, want 0", n)
	}
	// Writer should not have been called again for the second (empty) batch.
	if len(w.calls) != 2 {
		t.Fatalf("writer calls = %d, want 2 (no re-processing)", len(w.calls))
	}
}

func TestOmiIngester_Sync_SourceError(t *testing.T) {
	src := &fakeOmiSource{err: errors.New("boom")}
	w := &fakeMemoryWriter{}
	ing := NewOmiIngester(src, w)

	if _, err := ing.Sync(context.Background()); err == nil {
		t.Fatal("Sync: expected error from source, got nil")
	}
	if len(w.calls) != 0 {
		t.Fatalf("writer should not be called on source error, got %d calls", len(w.calls))
	}
}

func TestOmiIngester_Sync_PartialFailure(t *testing.T) {
	t1 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)

	src := &fakeOmiSource{memories: []OmiMemory{
		{ID: "a", CreatedAt: t1},
		{ID: "b", CreatedAt: t2}, // this upsert will fail
		{ID: "c", CreatedAt: t3},
	}}
	w := &fakeMemoryWriter{failID: "b", failErr: errors.New("db down")}
	ing := NewOmiIngester(src, w)

	n, err := ing.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync should not fail on a partial row error: %v", err)
	}
	// Two of three succeeded.
	if n != 2 {
		t.Fatalf("ingested count = %d, want 2 (partial success)", n)
	}
	if len(w.calls) != 3 {
		t.Fatalf("writer calls = %d, want 3 (all attempted)", len(w.calls))
	}
	// High-water should advance to the newest SUCCESS (t3), not be blocked by
	// the failed t2 row.
	if !ing.HighWaterMark().Equal(t3) {
		t.Fatalf("high-water = %v, want %v (newest success)", ing.HighWaterMark(), t3)
	}
}

func TestOmiIngester_WithInterval(t *testing.T) {
	ing := NewOmiIngester(&fakeOmiSource{}, &fakeMemoryWriter{})
	ing.WithInterval(time.Second)
	if ing.interval != time.Second {
		t.Errorf("interval = %v, want 1s", ing.interval)
	}
	// Zero/negative is ignored (keeps default).
	def := ing.interval
	ing.WithInterval(0)
	if ing.interval != def {
		t.Errorf("WithInterval(0) changed interval to %v", ing.interval)
	}
}

// containsString reports whether ss contains s.
func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

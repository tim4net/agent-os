package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// omiNamespace is a UUID v5 namespace used to derive deterministic file_path
// values for Omi memories, so re-syncing the same Omi memory upserts (updates)
// the same memory_index row instead of creating a duplicate.
const omiFilePathPrefix = "omi://memories/"

// memoryWriter is the subset of db.Querier the OmiIngester needs. The real
// *db.Queries satisfies it; tests inject a fake. Keeping this narrow lets the
// adapter be unit-tested without a live Postgres.
type memoryWriter interface {
	UpsertMemory(ctx context.Context, arg db.UpsertMemoryParams) (db.MemoryIndex, error)
}

// OmiIngester periodically polls an OmiSource for new device memories and
// upserts them into the memory_index as ambient context. It tracks a
// high-water mark (the newest created_at it has successfully ingested) so each
// cycle only fetches incremental data.
//
// The ingester is started by cmd/server only when OMI_API_TOKEN is set
// (opt-in / deferred — see issue #135).
type OmiIngester struct {
	source   OmiSource
	writer   memoryWriter
	ownerID  pgtype.UUID
	interval time.Duration

	stopCh chan struct{}

	mu        sync.Mutex
	highWater time.Time
}

// NewOmiIngester creates a background ingester that turns Omi device memories
// into memory_index rows attributed to the system owner (the same seed owner
// used by the memory indexer and artifact scanner).
func NewOmiIngester(source OmiSource, writer memoryWriter) *OmiIngester {
	return &OmiIngester{
		source:   source,
		writer:   writer,
		ownerID:  systemOwnerUUID(),
		interval: 10 * time.Minute,
		stopCh:   make(chan struct{}),
	}
}

// WithInterval overrides the sync interval (default 10m). Useful for tests.
func (oi *OmiIngester) WithInterval(d time.Duration) *OmiIngester {
	if d > 0 {
		oi.interval = d
	}
	return oi
}

// Start launches the background sync loop. It is safe to call once.
func (oi *OmiIngester) Start(ctx context.Context) {
	go func() {
		// Initial backfill.
		oi.syncOnce(ctx)

		ticker := time.NewTicker(oi.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-oi.stopCh:
				return
			case <-ticker.C:
				oi.syncOnce(ctx)
			}
		}
	}()
	slog.Info("omi-ingester: started", "interval", oi.interval)
}

// Stop signals the background loop to exit.
func (oi *OmiIngester) Stop() {
	select {
	case <-oi.stopCh:
		// already closed
	default:
		close(oi.stopCh)
	}
}

// HighWaterMark returns the created_at of the most recently ingested memory.
// Exposed for observability/tests.
func (oi *OmiIngester) HighWaterMark() time.Time {
	oi.mu.Lock()
	defer oi.mu.Unlock()
	return oi.highWater
}

// Sync fetches new memories since the high-water mark and upserts them.
// Returns the number of memories ingested and any error. The background loop
// delegates here; it is also exported so it can be driven deterministically in
// tests.
func (oi *OmiIngester) Sync(ctx context.Context) (int, error) {
	return oi.syncWith(ctx)
}

// syncOnce is the fire-and-forget variant used by the loop; it logs but does
// not propagate errors.
func (oi *OmiIngester) syncOnce(ctx context.Context) {
	n, err := oi.syncWith(ctx)
	if err != nil {
		slog.Error("omi-ingester: sync failed", "error", err)
		return
	}
	if n > 0 {
		slog.Info("omi-ingester: ingested", "count", n, "high_water", oi.HighWaterMark())
	}
}

// syncWith is the core: list -> filter -> upsert -> advance high-water mark.
func (oi *OmiIngester) syncWith(ctx context.Context) (int, error) {
	since := oi.HighWaterMark()

	memories, err := oi.source.ListSince(ctx, since)
	if err != nil {
		return 0, fmt.Errorf("omi-ingester: list: %w", err)
	}

	count := 0
	var newest time.Time
	for _, m := range memories {
		params := omiMemoryToParams(m, oi.ownerID)
		if _, err := oi.writer.UpsertMemory(ctx, params); err != nil {
			// A single failing row shouldn't abort the whole batch; record
			// the error but keep going so a partial cycle still advances.
			slog.Error("omi-ingester: upsert failed",
				"omi_id", m.ID, "file_path", params.FilePath, "error", err)
			continue
		}
		count++
		if m.CreatedAt.After(newest) {
			newest = m.CreatedAt
		}
	}

	// Only advance the high-water mark if we actually ingested something,
	// and only to a timestamp strictly newer than the current mark.
	oi.mu.Lock()
	if count > 0 && newest.After(oi.highWater) {
		oi.highWater = newest
	}
	oi.mu.Unlock()

	return count, nil
}

// omiMemoryToParams maps a normalized OmiMemory to a db.UpsertMemoryParams.
// The file_path is derived deterministically from the Omi memory id so repeated
// syncs update the same row (idempotent upsert via ON CONFLICT(file_path)).
//
// This is a pure function (no I/O), which makes the mapping unit-testable.
func omiMemoryToParams(m OmiMemory, ownerID pgtype.UUID) db.UpsertMemoryParams {
	title := m.Title
	if strings.TrimSpace(title) == "" {
		ts := m.CreatedAt
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		title = "Omi transcript " + ts.Format("2006-01-02 15:04")
	}

	content := buildOmiContent(m)

	tags := append([]string(nil), m.Tags...)
	tags = ensureTag(tags, "omi")
	tags = ensureTag(tags, "ambient")

	var projectID pgtype.UUID // not associated to a project by default

	return db.UpsertMemoryParams{
		OwnerID:   ownerID,
		FilePath:  omiFilePathPrefix + m.ID,
		Title:     pgtype.Text{String: title, Valid: true},
		Content:   pgtype.Text{String: content, Valid: content != ""},
		Tags:      tags,
		ProjectID: projectID,
	}
}

// buildOmiContent assembles a human-readable content body from an Omi memory:
// overview, transcript, and action items. Empty sections are omitted.
func buildOmiContent(m OmiMemory) string {
	var b strings.Builder
	if strings.TrimSpace(m.Overview) != "" {
		b.WriteString("## Summary\n")
		b.WriteString(strings.TrimSpace(m.Overview))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(m.Transcript) != "" {
		b.WriteString("## Transcript\n")
		b.WriteString(strings.TrimSpace(m.Transcript))
		b.WriteString("\n\n")
	}
	if len(m.ActionItems) > 0 {
		b.WriteString("## Action items\n")
		for _, a := range m.ActionItems {
			aa := strings.TrimSpace(a)
			if aa == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(aa)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ensureTag appends tag if not already present (case-insensitive).
func ensureTag(tags []string, tag string) []string {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return tags
		}
	}
	return append(tags, tag)
}

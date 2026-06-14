package service

// Unit tests for the MemoryIndexer project_id derivation logic.
//
// TestDeriveProjectID is a pure-logic test — it exercises the path-prefix →
// slug → UUID mapping without touching the database.  The slugToID cache is
// populated directly so we don't need a live Postgres.
//
// Integration coverage (against a real DB) lives in internal/api/memory_project_test.go.

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// mustUUIDStr parses a canonical UUID string into a pgtype.UUID, failing the
// test on a parse error.  (Named distinctly from the package-level mustUUID in
// workevent_ingest_fake_test.go which takes a uuid.UUID, not a string.)
func mustUUIDStr(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("scan uuid %q: %v", s, err)
	}
	return u
}

func TestDeriveProjectID(t *testing.T) {
	riftwingID := mustUUIDStr(t, "11111111-1111-1111-1111-111111111111")
	agentOSID := mustUUIDStr(t, "22222222-2222-2222-2222-222222222222")

	mi := &MemoryIndexer{
		projectPathMappings: DefaultProjectPathMappings,
		slugToID: map[string]pgtype.UUID{
			"riftwing": riftwingID,
			"agent-os": agentOSID,
		},
	}

	cases := []struct {
		name   string
		path   string
		want   pgtype.UUID
		wantOK bool // want.Valid
	}{
		// ── Happy paths: known prefix → known slug → cached UUID ──────────
		{
			name:   "projects/riftwing nested file",
			path:   "projects/riftwing/notes/design.md",
			want:   riftwingID,
			wantOK: true,
		},
		{
			name:   "Riftwing capitalised prefix",
			path:   "Riftwing/daily/2024-01-01.md",
			want:   riftwingID,
			wantOK: true,
		},
		{
			name:   "projects/agent-os nested file",
			path:   "projects/agent-os/architecture/adr-007.md",
			want:   agentOSID,
			wantOK: true,
		},
		// ── Exact prefix match (relPath == prefix, no trailing slash) ──────
		{
			name:   "exact prefix equals path",
			path:   "projects/riftwing",
			want:   riftwingID,
			wantOK: true,
		},
		// ── Negative: unknown prefix → zero UUID (Valid=false) ────────────
		{
			name:   "unknown prefix maps to nil",
			path:   "random/scratchpad.md",
			want:   pgtype.UUID{},
			wantOK: false,
		},
		{
			name:   "root-level file with no project prefix",
			path:   "inbox.md",
			want:   pgtype.UUID{},
			wantOK: false,
		},
		// ── Negative: sibling-prefix must NOT match ───────────────────────
		// "projects/riftwing-x" shares the textual prefix "projects/riftwing"
		// but is a DIFFERENT directory — must not be attributed to riftwing.
		{
			name:   "sibling prefix riftwing-x is not riftwing",
			path:   "projects/riftwing-x/notes.md",
			want:   pgtype.UUID{},
			wantOK: false,
		},
		{
			name:   "sibling prefix Riftwinger is not Riftwing",
			path:   "Riftwinger/notes.md",
			want:   pgtype.UUID{},
			wantOK: false,
		},
		// ── Windows-style backslash paths are normalised ──────────────────
		{
			name:   "backslash path projects\\riftwing",
			path:   "projects\\riftwing\\notes.md",
			want:   riftwingID,
			wantOK: true,
		},
		{
			name:   "mixed separators path",
			path:   "projects/agent-os\\sub/deep.md",
			want:   agentOSID,
			wantOK: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mi.DeriveProjectID(tc.path)
			if got.Valid != tc.wantOK {
				t.Fatalf("DeriveProjectID(%q).Valid = %v, want %v", tc.path, got.Valid, tc.wantOK)
			}
			if got.Valid && got != tc.want {
				t.Fatalf("DeriveProjectID(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestDeriveProjectID_EmptyCache verifies that when the slug cache is empty
// (e.g. before the first refreshProjectCache call), every path returns a
// zero-value UUID rather than panicking.
func TestDeriveProjectID_EmptyCache(t *testing.T) {
	mi := &MemoryIndexer{
		projectPathMappings: DefaultProjectPathMappings,
		slugToID:            map[string]pgtype.UUID{}, // empty — no projects loaded
	}

	for _, path := range []string{
		"projects/riftwing/note.md",
		"Riftwing/x.md",
		"projects/agent-os/y.md",
	} {
		got := mi.DeriveProjectID(path)
		if got.Valid {
			t.Fatalf("DeriveProjectID(%q) returned %v; expected zero UUID from empty cache", path, got)
		}
	}
}

// TestDeriveProjectID_CustomMappings proves WithProjectPathMappings overrides
// the defaults, so callers can configure arbitrary vault folder layouts.
func TestDeriveProjectID_CustomMappings(t *testing.T) {
	dayjobID := mustUUIDStr(t, "33333333-3333-3333-3333-333333333333")

	mi := (&MemoryIndexer{
		slugToID: map[string]pgtype.UUID{
			"dayjob": dayjobID,
		},
	}).WithProjectPathMappings([]ProjectPathMapping{
		{PathPrefix: "work/employer", Slug: "dayjob"},
	})

	got := mi.DeriveProjectID("work/employer/meeting-notes.md")
	if !got.Valid || got != dayjobID {
		t.Fatalf("custom mapping: DeriveProjectID = %v, want %v", got, dayjobID)
	}

	// Old default mappings must no longer apply.
	got = mi.DeriveProjectID("projects/riftwing/note.md")
	if got.Valid {
		t.Fatalf("custom mappings should replace defaults; got %v for old prefix", got)
	}
}

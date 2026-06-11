package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestDeriveProjectID(t *testing.T) {
	// Set up an indexer with test mappings and a slug→id cache.
	mi := &MemoryIndexer{
		projectPathMappings: []ProjectPathMapping{
			{PathPrefix: "projects/riftwing", Slug: "riftwing"},
			{PathPrefix: "Riftwing", Slug: "riftwing"},
			{PathPrefix: "projects/agent-os", Slug: "agent-os"},
		},
		slugToID: map[string]pgtype.UUID{
			"riftwing":  {Valid: true, Bytes: [16]byte{1}},
			"agent-os":  {Valid: true, Bytes: [16]byte{2}},
		},
	}

	tests := []struct {
		name    string
		relPath string
		want    string // expected slug prefix, or "" for no match
	}{
		{
			name:    "projects/riftwing subfolder file",
			relPath: "projects/riftwing/notes/design.md",
			want:    "riftwing",
		},
		{
			name:    "Riftwing top-level folder",
			relPath: "Riftwing/architecture.md",
			want:    "riftwing",
		},
		{
			name:    "Riftwing nested file",
			relPath: "Riftwing/sub/deep/file.md",
			want:    "riftwing",
		},
		{
			name:    "projects/agent-os file",
			relPath: "projects/agent-os/README.md",
			want:    "agent-os",
		},
		{
			name:    "personal folder — no project",
			relPath: "personal/journal.md",
			want:    "",
		},
		{
			name:    "root-level file — no project",
			relPath: "index.md",
			want:    "",
		},
		{
			name:    "agents folder — no project",
			relPath: "agents/roux/config.md",
			want:    "",
		},
		{
			name:    "exact prefix match (no trailing slash)",
			relPath: "Riftwing",
			want:    "riftwing",
		},
		{
			name:    "partial prefix mismatch — RiftwingX",
			relPath: "RiftwingX/notes.md",
			want:    "",
		},
		{
			name:    "Windows-style path separators",
			relPath: "projects\\riftwing\\notes.md",
			want:    "riftwing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mi.DeriveProjectID(tt.relPath)
			if tt.want == "" {
				if got.Valid {
					t.Errorf("DeriveProjectID(%q) = valid UUID, want invalid (no match)", tt.relPath)
				}
			} else {
				if !got.Valid {
					t.Errorf("DeriveProjectID(%q) = invalid UUID, want match for slug %q", tt.relPath, tt.want)
				} else {
					expected := mi.slugToID[tt.want]
					if got != expected {
						t.Errorf("DeriveProjectID(%q) = UUID %v, want UUID for slug %q", tt.relPath, got, tt.want)
					}
				}
			}
		})
	}
}

func TestDeriveProjectID_EmptyMappings(t *testing.T) {
	mi := &MemoryIndexer{
		projectPathMappings: nil,
		slugToID: map[string]pgtype.UUID{
			"riftwing": {Valid: true, Bytes: [16]byte{1}},
		},
	}

	got := mi.DeriveProjectID("projects/riftwing/file.md")
	if got.Valid {
		t.Error("expected no match with empty mappings, got a valid UUID")
	}
}

func TestDeriveProjectID_SlugNotInCache(t *testing.T) {
	mi := &MemoryIndexer{
		projectPathMappings: []ProjectPathMapping{
			{PathPrefix: "projects/unknown", Slug: "unknown-project"},
		},
		slugToID: map[string]pgtype.UUID{
			// slug "unknown-project" is NOT in the cache
		},
	}

	got := mi.DeriveProjectID("projects/unknown/file.md")
	if got.Valid {
		t.Error("expected no match when slug is not in cache, got a valid UUID")
	}
}

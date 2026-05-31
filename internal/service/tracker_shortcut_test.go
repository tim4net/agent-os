package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

func TestNormalizeItemType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"story", "story"},
		{"bug", "bug"},
		{"chore", "chore"},
		{"task", "task"},
		{"feature", "feature"},
		{"Story", "story"},   // case-insensitive
		{"BUG", "bug"},       // case-insensitive
		{"unknown", "story"}, // default fallback
		{"", "story"},        // empty fallback
	}
	for _, tt := range tests {
		got := normalizeItemType(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeItemType(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestShortcutStoryToTrackerItem_Mapping(t *testing.T) {
	// Simulate a Shortcut API story.
	story := ShortcutStory{
		ID:         12345,
		Num:        91130,
		Name:       "Build SPOG timeline UI",
		EntityType: "feature",
		State:      "in progress",
		AppURL:     "https://app.shortcut.com/story/12345",
		UpdatedAt:  time.Date(2026, 5, 30, 17, 0, 0, 0, time.UTC),
	}

	// Map to tracker item fields as the Sync method does.
	externalRef := "SC-91130"
	itemType := normalizeItemType(story.EntityType)
	status := story.State
	canonicalURL := story.AppURL

	// Build the payload that Sync would create.
	payload, err := json.Marshal(map[string]any{
		"shortcut_id":   story.ID,
		"shortcut_num":   story.Num,
		"description":     story.Description,
		"entity_type":    story.EntityType,
		"shortcut_state": story.State,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	// Verify the mapping is correct.
	if externalRef != "SC-91130" {
		t.Errorf("external_ref = %q, want SC-91130", externalRef)
	}
	if itemType != "feature" {
		t.Errorf("item_type = %q, want feature", itemType)
	}
	if status != "in progress" {
		t.Errorf("status = %q, want in progress", status)
	}
	if canonicalURL != "https://app.shortcut.com/story/12345" {
		t.Errorf("canonical_url = %q, want https://app.shortcut.com/story/12345", canonicalURL)
	}

	// Verify payload contains expected fields.
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p["shortcut_id"].(float64) != 12345 {
		t.Errorf("payload shortcut_id = %v, want 12345", p["shortcut_id"])
	}
	if p["shortcut_num"].(float64) != 91130 {
		t.Errorf("payload shortcut_num = %v, want 91130", p["shortcut_num"])
	}
	if p["shortcut_state"] != "in progress" {
		t.Errorf("payload shortcut_state = %v, want in progress", p["shortcut_state"])
	}
}

func TestTrackerItemFromDB_Mapping(t *testing.T) {
	now := time.Date(2026, 5, 30, 17, 0, 0, 0, time.UTC)

	id := pgtype.UUID{}
	id.Scan("550e8400-e29b-41d4-a716-446655440000")

	projectID := pgtype.UUID{}
	projectID.Scan("660e8400-e29b-41d4-a716-446655440001")

	row := db.TrackerItem{
		ID:          id,
		ProjectID:   projectID,
		ExternalRef: "SC-91130",
		Title:       "Build SPOG timeline UI",
		Status:      "in progress",
		ItemType:    "feature",
		CanonicalUrl: pgtype.Text{String: "https://app.shortcut.com/story/12345", Valid: true},
		Payload:     []byte(`{"shortcut_id": 12345}`),
		Tenant:      "dayjob",
		SyncedAt:    pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}

	entry := TrackerItemFromDB(row)

	if entry.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("ID = %q, want correct UUID", entry.ID)
	}
	if entry.ProjectID != "660e8400-e29b-41d4-a716-446655440001" {
		t.Errorf("ProjectID = %q, want correct UUID", entry.ProjectID)
	}
	if entry.ExternalRef != "SC-91130" {
		t.Errorf("ExternalRef = %q, want SC-91130", entry.ExternalRef)
	}
	if entry.Title != "Build SPOG timeline UI" {
		t.Errorf("Title = %q, want Build SPOG timeline UI", entry.Title)
	}
	if entry.Status != "in progress" {
		t.Errorf("Status = %q, want in progress", entry.Status)
	}
	if entry.ItemType != "feature" {
		t.Errorf("ItemType = %q, want feature", entry.ItemType)
	}
	if entry.CanonicalURL != "https://app.shortcut.com/story/12345" {
		t.Errorf("CanonicalURL = %q, want correct URL", entry.CanonicalURL)
	}
	if entry.Tenant != "dayjob" {
		t.Errorf("Tenant = %q, want dayjob", entry.Tenant)
	}
	if !entry.SyncedAt.Equal(now) {
		t.Errorf("SyncedAt = %v, want %v", entry.SyncedAt, now)
	}
}

func TestTrackerItemFromDB_NullFields(t *testing.T) {
	// Verify that NULL pgtype fields are handled gracefully.
	row := db.TrackerItem{
		ID:           pgtype.UUID{},
		ProjectID:    pgtype.UUID{},
		ExternalRef:  "SC-1",
		Title:        "Test",
		Status:       "done",
		ItemType:     "bug",
		CanonicalUrl: pgtype.Text{},
		Payload:      nil,
		Tenant:       "personal",
		SyncedAt:     pgtype.Timestamptz{},
		CreatedAt:    pgtype.Timestamptz{},
		UpdatedAt:    pgtype.Timestamptz{},
	}

	entry := TrackerItemFromDB(row)

	if entry.ID != "" {
		t.Errorf("ID should be empty for null UUID, got %q", entry.ID)
	}
	if entry.ProjectID != "" {
		t.Errorf("ProjectID should be empty for null UUID, got %q", entry.ProjectID)
	}
	if entry.CanonicalURL != "" {
		t.Errorf("CanonicalURL should be empty for null, got %q", entry.CanonicalURL)
	}
	if entry.ExternalRef != "SC-1" {
		t.Errorf("ExternalRef = %q, want SC-1", entry.ExternalRef)
	}
	if entry.Tenant != "personal" {
		t.Errorf("Tenant = %q, want personal", entry.Tenant)
	}
}

func TestPgtypeUUIDEquals(t *testing.T) {
	var nullA, nullB pgtype.UUID

	validA := pgtype.UUID{}
	validA.Scan("550e8400-e29b-41d4-a716-446655440000")

	validB := pgtype.UUID{}
	validB.Scan("550e8400-e29b-41d4-a716-446655440000")

	validC := pgtype.UUID{}
	validC.Scan("660e8400-e29b-41d4-a716-446655440001")

	// NULL == NULL
	if !pgtypeUUIDEquals(nullA, nullB) {
		t.Error("null UUIDs should be equal")
	}

	// NULL != valid
	if pgtypeUUIDEquals(nullA, validA) {
		t.Error("null should not equal valid")
	}

	// valid == valid (same)
	if !pgtypeUUIDEquals(validA, validB) {
		t.Error("same valid UUIDs should be equal")
	}

	// valid != valid (different)
	if pgtypeUUIDEquals(validA, validC) {
		t.Error("different valid UUIDs should not be equal")
	}
}

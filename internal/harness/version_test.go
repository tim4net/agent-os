package harness

import (
	"encoding/json"
	"testing"
	"time"
)

func TestVersionInfoJSONShape(t *testing.T) {
	checkedAt := time.Date(2026, 6, 5, 12, 34, 56, 0, time.UTC)
	info := VersionInfo{Current: "1.2.3", Source: "http", CheckedAt: checkedAt}

	b, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	want := `{"current":"1.2.3","source":"http","checked_at":"2026-06-05T12:34:56Z"}`
	if string(b) != want {
		t.Fatalf("VersionInfo JSON = %s, want %s", string(b), want)
	}
}

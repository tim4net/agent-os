package harness

import (
	"context"
	"time"
)

// VersionInfo is the upstream version a harness reports for its backing service.
type VersionInfo struct {
	Current   string    `json:"current"` // upstream-reported version; "" if unknown
	Source    string    `json:"source"`  // provenance: "hello-ok" | "health" | "cli" | "http" | "unknown"
	CheckedAt time.Time `json:"checked_at"`
}

// VersionProber is an OPTIONAL capability. Harnesses that can report the
// version of their backing service implement it. Callers MUST type-assert and
// fall back to an "unknown" VersionInfo when the assertion fails.
type VersionProber interface {
	VersionInfo(ctx context.Context) (*VersionInfo, error)
}

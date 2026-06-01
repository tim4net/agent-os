package service

import (
	"time"
)

// EmitterStatus represents the derived liveness status of an emitter session.
// Values match the contract §4 liveness derivation.
type EmitterStatus string

const (
	EmitterStatusRunning EmitterStatus = "running"
	EmitterStatusStale   EmitterStatus = "stale"
	EmitterStatusDone    EmitterStatus = "done"
	EmitterStatusFailed  EmitterStatus = "failed"
	EmitterStatusCancelled EmitterStatus = "cancelled"
)

// EmitterSession represents the health state of a single emitter session.
type EmitterSession struct {
	Harness      string        `json:"harness"`
	SessionID    string        `json:"session_id"`
	Host         string        `json:"host"`
	LivenessMode string        `json:"liveness_mode"`
	PID          int           `json:"pid"`
	Status       EmitterStatus `json:"status"`
	// LastEventReceivedAt is the most recent received_at across all events
	// for this session — the single clock source for liveness (contract §4).
	LastEventReceivedAt *time.Time `json:"last_event_received_at,omitempty"`
	// LastHeartbeat is the latest heartbeat/start received_at (supervised only).
	LastHeartbeat *time.Time `json:"last_heartbeat,omitempty"`
	// FirstSeen is the earliest received_at (session start).
	FirstSeen *time.Time `json:"first_seen,omitempty"`
}

// EmitterHealthResponse is the paginated API response for emitter health.
type EmitterHealthResponse struct {
	Emitters []EmitterSession `json:"emitters"`
	Total    int64            `json:"total"`
	Limit    int              `json:"limit"`
	Offset   int              `json:"offset"`
}

// DefaultSupervisedStaleWindow is the liveness timeout for supervised emitters.
// A supervised session with no heartbeat in this window is considered stale.
// Contract §4: default is 5 minutes.
const DefaultSupervisedStaleWindow = 5 * time.Minute

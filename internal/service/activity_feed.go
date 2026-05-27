package service

import (
	"log/slog"
	"sync"
	"time"
)

// ActivityEntry represents a single event stored in the activity feed ring buffer.
type ActivityEntry struct {
	EventType string `json:"event_type"`
	Timestamp string `json:"timestamp"`
	AgentID   string `json:"agent_id,omitempty"`
	Summary   string `json:"summary"`
}

// ActivityFeed maintains an in-memory ring buffer of the last N events from the event bus.
type ActivityFeed struct {
	mu      sync.RWMutex
	buf     []ActivityEntry
	size    int
	head    int // next write position
	count   int // total entries written (up to size)
}

// NewActivityFeed creates a new ActivityFeed that listens to the event bus and maintains
// a ring buffer of the last size events.
func NewActivityFeed(bus *EventBus, size int) *ActivityFeed {
	if size <= 0 {
		size = 200
	}
	af := &ActivityFeed{
		buf:   make([]ActivityEntry, size),
		size:  size,
		head:  0,
		count: 0,
	}

	// Subscribe to event bus
	sub := bus.Subscribe()
	go af.consume(sub)

	slog.Info("activity-feed: started", "buffer_size", size)
	return af
}

func (af *ActivityFeed) consume(sub <-chan Event) {
	// Events to skip — noisy background processes that aren't user-visible
	skipTypes := map[string]bool{
		"memory_indexed": true,
	}
	for event := range sub {
		if skipTypes[event.Type] {
			continue
		}
		// Skip litellm agent status changes — it's infrastructure, not a user-visible agent
		if event.Type == EventAgentStatusChanged {
			if name, ok := event.Payload["agent_name"].(string); ok && name == "litellm" {
				continue
			}
		}
		entry := af.toEntry(event)
		af.add(entry)
	}
}

func (af *ActivityFeed) toEntry(event Event) ActivityEntry {
	entry := ActivityEntry{
		EventType: event.Type,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// Extract agent_id from payload if present
	if agentID, ok := event.Payload["agent_id"].(string); ok {
		entry.AgentID = agentID
	}

	// Build summary based on event type
	entry.Summary = af.summarize(event)

	return entry
}

func (af *ActivityFeed) summarize(event Event) string {
	payload := event.Payload
	switch event.Type {
	case EventAgentStatusChanged:
		name, _ := payload["agent_name"].(string)
		old, _ := payload["old_status"].(string)
		newS, _ := payload["new_status"].(string)
		return "Agent " + name + " status changed: " + old + " → " + newS
	case EventChatChunk:
		agentID, _ := payload["agent_id"].(string)
		return "Chat chunk received for agent " + agentID
	case EventNewArtifact:
		title, _ := payload["title"].(string)
		artType, _ := payload["type"].(string)
		return "New artifact: " + title + " (" + artType + ")"
	case EventTaskUpdated:
		taskID, _ := payload["task_id"].(string)
		status, _ := payload["status"].(string)
		return "Task " + taskID + " updated to " + status
	case "memory_indexed":
		filePath, _ := payload["file_path"].(string)
		return "Memory indexed: " + filePath
	case "delegation_created":
		child, _ := payload["child_agent_name"].(string)
		goal, _ := payload["task_goal"].(string)
		return "Delegated to " + child + ": " + goal
	case "delegation_updated":
		child, _ := payload["child_agent_name"].(string)
		status, _ := payload["status"].(string)
		goal, _ := payload["task_goal"].(string)
		return "Delegation to " + child + " " + status + ": " + goal
	default:
		return "Event: " + event.Type
	}
}

func (af *ActivityFeed) add(entry ActivityEntry) {
	af.mu.Lock()
	defer af.mu.Unlock()

	af.buf[af.head] = entry
	af.head = (af.head + 1) % af.size
	if af.count < af.size {
		af.count++
	}
}

// List returns the last n events in reverse chronological order (newest first),
// with pagination support via offset.
func (af *ActivityFeed) List(limit, offset int) []ActivityEntry {
	af.mu.RLock()
	defer af.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	if limit > af.count {
		limit = af.count
	}
	if offset < 0 {
		offset = 0
	}
	if offset > af.count {
		return []ActivityEntry{}
	}

	// Build ordered slice (newest first)
	total := af.count
	entries := make([]ActivityEntry, 0, total)
	for i := 0; i < total; i++ {
		// newest is at (head - 1 - i + size) % size
		idx := (af.head - 1 - i + af.size) % af.size
		entries = append(entries, af.buf[idx])
	}

	// Apply offset and limit
	end := offset + limit
	if end > total {
		end = total
	}
	if offset >= total {
		return []ActivityEntry{}
	}

	return entries[offset:end]
}

// Total returns the number of events currently in the buffer.
func (af *ActivityFeed) Total() int {
	af.mu.RLock()
	defer af.mu.RUnlock()
	return af.count
}



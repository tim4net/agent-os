package service

import (
	"encoding/json"
	"sync"
	"time"
)

// Event represents a published event on the bus.
type Event struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

// Event type constants.
const (
	EventAgentStatusChanged = "agent_status_changed"
	EventChatChunk          = "chat_chunk"
	EventNewArtifact        = "new_artifact"
	EventTaskUpdated        = "task_updated"
)

// EventBus provides a simple pub/sub mechanism using Go channels.
type EventBus struct {
	mu            sync.RWMutex
	subscribers   []chan Event
	throttleMu    sync.Mutex
	lastPublished map[string]time.Time
}

// NewEventBus creates a new EventBus.
func NewEventBus() *EventBus {
	return &EventBus{
		lastPublished: make(map[string]time.Time),
	}
}

// Subscribe returns a channel that receives all published events.
// The caller must consume from the channel to avoid blocking.
func (eb *EventBus) Subscribe() <-chan Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	ch := make(chan Event, 64)
	eb.subscribers = append(eb.subscribers, ch)
	return ch
}

// Unsubscribe removes a subscriber channel and closes it.
func (eb *EventBus) Unsubscribe(ch <-chan Event) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	for i, sub := range eb.subscribers {
		if sub == ch {
			eb.subscribers = append(eb.subscribers[:i], eb.subscribers[i+1:]...)
			close(sub)
			return
		}
	}
}

// Publish sends an event to all subscribers.
// Events are dropped for slow consumers (non-blocking send).
// Same event type is throttled: if published within the last 2 seconds, it is skipped.
func (eb *EventBus) Publish(event Event) {
	// Throttle check: skip if same event type was published within the last 2 seconds
	eb.throttleMu.Lock()
	now := time.Now()
	if last, ok := eb.lastPublished[event.Type]; ok && now.Sub(last) < 2*time.Second {
		eb.throttleMu.Unlock()
		return
	}
	eb.lastPublished[event.Type] = now
	eb.throttleMu.Unlock()

	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for _, sub := range eb.subscribers {
		select {
		case sub <- event:
		default:
			// Drop event for slow consumers
		}
	}
}

// PublishTyped is a convenience to publish an event with a type and payload.
func (eb *EventBus) PublishTyped(eventType string, payload map[string]any) {
	eb.Publish(Event{Type: eventType, Payload: payload})
}

// ToJSON converts the event to JSON bytes.
func (e Event) ToJSON() []byte {
	data, err := json.Marshal(e)
	if err != nil {
		return []byte(`{"type":"error","payload":{"error":"marshal failed"}}`)
	}
	return data
}

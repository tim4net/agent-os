package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// StreamEvents implements the SSE endpoint for real-time event streaming.
func (a *API) StreamEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, canFlush := w.(http.Flusher)

	// Send initial connection event (unnamed so onmessage catches it)
	fmt.Fprintf(w, "data: {\"type\":\"connected\",\"payload\":{\"message\":\"connected\"}}\n\n")
	if canFlush {
		flusher.Flush()
	}

	// Subscribe to events
	sub := a.bus.Subscribe()
	defer a.bus.Unsubscribe(sub)

	// Keep-alive ticker
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-sub:
			if !ok {
				return
			}
			data := event.ToJSON()
			fmt.Fprintf(w, "data: %s\n\n", data)
			if canFlush {
				flusher.Flush()
			}
		case <-ticker.C:
			// Send keep-alive comment
			fmt.Fprintf(w, ": keepalive\n\n")
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

// ensure json import is used
var _ = json.Marshal

package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/tim4net/agent-os/internal/service"
)

// ActivityHandler returns the activity feed endpoint.
func (a *API) GetActivity(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	entries := a.feed.List(limit, offset)
	total := a.feed.Total()

	if entries == nil {
		entries = []service.ActivityEntry{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"events": entries,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

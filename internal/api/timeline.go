package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// TimelineEvent represents a single event in the unified timeline.
type TimelineEvent struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"` // conversation | task_completed | artifact_created | workflow_run
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Timestamp   string          `json:"timestamp"`
	AgentID     string          `json:"agent_id,omitempty"`
	AgentName   string          `json:"agent_name,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// TimelineResponse is the API response for the timeline endpoint.
type TimelineResponse struct {
	Events []TimelineEvent `json:"events"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

// TimelineRoutes returns a Chi router with timeline routes.
func (a *API) TimelineRoutes() http.Handler {
	r := chi.NewRouter()
	r.Get("/", a.GetTimeline)
	return r
}

// GetTimeline handles GET /api/timeline?limit=50&offset=0
func (a *API) GetTimeline(w http.ResponseWriter, r *http.Request) {
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

	ctx := r.Context()

	// Build a map of agent names for enrichment
	agents, err := a.queries.ListAgents(ctx)
	if err != nil {
		http.Error(w, "failed to list agents: "+err.Error(), http.StatusInternalServerError)
		return
	}
	agentNames := make(map[string]string, len(agents))
	for _, ag := range agents {
		agentNames[ag.ID.String()] = ag.DisplayName
	}

	events := make([]TimelineEvent, 0)

	// 1. Conversations
	convs, _ := a.queries.ListConversations(ctx, pgtype.UUID{})
	for _, c := range convs {
		// Use saved summary if available, otherwise "New conversation"
		title := "New conversation"
		if c.Summary.Valid && c.Summary.String != "" {
			title = c.Summary.String
		}
		agentName := agentNames[c.AgentID.String()]
		desc := "Conversation started"
		if agentName != "" {
			desc = "Conversation with " + agentName
		}
		meta, _ := json.Marshal(map[string]string{"conversation_id": c.ID.String()})
		events = append(events, TimelineEvent{
			ID:          c.ID.String(),
			Type:        "conversation",
			Title:       title,
			Description: desc,
			Timestamp:   c.CreatedAt.Time.UTC().Format(time.RFC3339),
			AgentID:     c.AgentID.String(),
			AgentName:   agentName,
			Metadata:    meta,
		})
	}

	// 2. Tasks completed
	tasks, _ := a.queries.ListTasks(ctx, db.ListTasksParams{
		Column1: "done",
		Column2: pgtype.UUID{},
	})
	for _, t := range tasks {
		desc := "Task completed"
		if t.Description.Valid && t.Description.String != "" {
			desc = t.Description.String
		}
		meta, _ := json.Marshal(map[string]string{"task_id": t.ID.String(), "status": "done"})
		events = append(events, TimelineEvent{
			ID:          t.ID.String(),
			Type:        "task_completed",
			Title:       t.Title,
			Description: desc,
			Timestamp:   t.UpdatedAt.Time.UTC().Format(time.RFC3339),
			AgentID:     t.AgentID.String(),
			AgentName:   agentNames[t.AgentID.String()],
			Metadata:    meta,
		})
	}

	// 3. Artifacts
	artifacts, _ := a.queries.ListArtifacts(ctx, db.ListArtifactsParams{
		Column1: "",
		Column2: pgtype.UUID{},
		Limit:   1000,
		Offset:  0,
	})
	for _, art := range artifacts {
		title := art.FilePath.String
		if art.Title.Valid && art.Title.String != "" {
			title = art.Title.String
		}
		desc := "Artifact created"
		if art.Description.Valid && art.Description.String != "" {
			desc = art.Description.String
		}
		meta, _ := json.Marshal(map[string]string{"artifact_id": art.ID.String(), "type": art.Type})
		events = append(events, TimelineEvent{
			ID:          art.ID.String(),
			Type:        "artifact_created",
			Title:       title,
			Description: desc,
			Timestamp:   art.CreatedAt.Time.UTC().Format(time.RFC3339),
			AgentID:     art.AgentID.String(),
			AgentName:   agentNames[art.AgentID.String()],
			Metadata:    meta,
		})
	}

	// 4. Workflow runs (completed) — iterate workflows and their runs
	workflows, _ := a.queries.ListWorkflows(ctx)
	for _, wf := range workflows {
		runs, err := a.queries.ListWorkflowRuns(ctx, wf.ID)
		if err != nil {
			continue
		}
		for _, wr := range runs {
			if !wr.Status.Valid || wr.Status.String != "completed" {
				continue
			}
			agentID := wf.AgentID.String()
			desc := "Workflow run completed"
			meta, _ := json.Marshal(map[string]string{
				"workflow_id":  wf.ID.String(),
				"workflow_run": wr.ID.String(),
			})
			events = append(events, TimelineEvent{
				ID:          wr.ID.String(),
				Type:        "workflow_run",
				Title:       wf.Name,
				Description: desc,
				Timestamp:   wr.CreatedAt.Time.UTC().Format(time.RFC3339),
				AgentID:     agentID,
				AgentName:   agentNames[agentID],
				Metadata:    meta,
			})
		}
	}

	// 5. Delegations
	delegations, _ := a.queries.ListDelegations(ctx, db.ListDelegationsParams{
		Column1: pgtype.UUID{},
		Column2: "",
		Limit:   1000,
		Offset:  0,
	})
	for _, d := range delegations {
		title := "Delegated to " + d.ChildAgentName
		desc := d.TaskGoal
		if d.ResultSummary.Valid && d.ResultSummary.String != "" {
			desc = d.ResultSummary.String
		}
		statusLabel := d.Status
		if d.Status == "running" {
			statusLabel = "🔄 Running"
		} else if d.Status == "completed" {
			statusLabel = "✅ Completed"
		} else if d.Status == "failed" {
			statusLabel = "❌ Failed"
		}
		meta, _ := json.Marshal(map[string]string{
			"delegation_id":    d.ID.String(),
			"child_agent_name": d.ChildAgentName,
			"status":           d.Status,
		})
		ts := d.CreatedAt.Time
		if d.CompletedAt.Valid && d.Status != "running" && d.Status != "pending" {
			ts = d.CompletedAt.Time
		}
		events = append(events, TimelineEvent{
			ID:          d.ID.String(),
			Type:        "delegation",
			Title:       title,
			Description: statusLabel + " — " + desc,
			Timestamp:   ts.UTC().Format(time.RFC3339),
			AgentID:     d.ParentAgentID.String(),
			AgentName:   agentNames[d.ParentAgentID.String()],
			Metadata:    meta,
		})
	}

	// Sort by timestamp descending
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp > events[j].Timestamp
	})

	// Apply total count before pagination
	total := len(events)

	// Apply pagination
	if offset > len(events) {
		events = []TimelineEvent{}
	} else {
		end := offset + limit
		if end > len(events) {
			end = len(events)
		}
		events = events[offset:end]
	}

	if events == nil {
		events = []TimelineEvent{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TimelineResponse{
		Events: events,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

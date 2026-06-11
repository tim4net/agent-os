package api

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// TitleWorker periodically re-summarizes titles for active conversations.
type TitleWorker struct {
	api    *API
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewTitleWorker creates a new TitleWorker.
func NewTitleWorker(api *API) *TitleWorker {
	return &TitleWorker{
		api:    api,
		stopCh: make(chan struct{}),
	}
}

// Start begins the hourly title re-summarization loop.
func (tw *TitleWorker) Start(ctx context.Context) {
	tw.wg.Add(1)
	go func() {
		defer tw.wg.Done()
		tw.run(ctx)
	}()
}

// Stop signals the worker to stop and waits for it to finish.
func (tw *TitleWorker) Stop() {
	close(tw.stopCh)
	tw.wg.Wait()
}

func (tw *TitleWorker) run(ctx context.Context) {
	// Run once at startup after a 2-minute delay (let the system warm up)
	select {
	case <-time.After(2 * time.Minute):
		tw.resummarizeActive(ctx)
	case <-tw.stopCh:
		return
	case <-ctx.Done():
		return
	}

	// Then every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tw.resummarizeActive(ctx)
		case <-tw.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// resummarizeActive queries conversations with messages in the last 24h
// and re-generates their titles via LLM.
func (tw *TitleWorker) resummarizeActive(ctx context.Context) {
	if tw.api.litellmURL == "" && tw.api.openrouterAPIKey == "" {
		return // no LLM provider configured
	}

	// Find conversations that have had messages in the last 24 hours
	rows, err := tw.api.pool.Query(ctx,
		`SELECT DISTINCT c.id, c.agent_id
		FROM conversations c
		JOIN messages m ON m.conversation_id = c.id
		WHERE m.created_at > NOW() - INTERVAL '24 hours'
		ORDER BY c.id`,
	)
	if err != nil {
		slog.Warn("title worker: failed to query active conversations", "error", err)
		return
	}
	defer rows.Close()

	type convRef struct {
		ID      pgtype.UUID
		AgentID pgtype.UUID
	}
	var convs []convRef
	for rows.Next() {
		var c convRef
		if err := rows.Scan(&c.ID, &c.AgentID); err != nil {
			slog.Warn("title worker: scan error", "error", err)
			continue
		}
		convs = append(convs, c)
	}

	if len(convs) == 0 {
		return
	}

	slog.Info("title worker: re-summarizing active conversations", "count", len(convs))

	// Process with limited concurrency (3 at a time)
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup

	for _, c := range convs {
		wg.Add(1)
		go func(id, agentID pgtype.UUID) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			tw.resummarizeOne(ctx, id, agentID)
		}(c.ID, c.AgentID)
	}

	wg.Wait()
}

func (tw *TitleWorker) resummarizeOne(ctx context.Context, convID pgtype.UUID, agentID pgtype.UUID) {
	// Use a separate context with timeout to avoid blocking
	jobCtx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	msgs, err := tw.api.queries.ListMessages(jobCtx, convID)
	if err != nil || len(msgs) == 0 {
		return
	}

	// Build compact text representation
	limit := len(msgs)
	if limit > 10 {
		limit = 10
	}
	var sb strings.Builder
	for i := 0; i < limit; i++ {
		m := msgs[i]
		role := m.Role
		if role == "user" {
			role = "User"
		} else if role == "assistant" {
			role = "Assistant"
		} else {
			continue
		}
		content := m.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		sb.WriteString(role + ": " + content + "\n")
	}

	summary, err := tw.api.generateSummary(jobCtx, sb.String())
	if err != nil {
		slog.Warn("title worker: LLM summary failed", "conversation_id", convID.String(), "error", err)
		return
	}

	// Make unique per agent
	uniqueTitle := tw.api.makeUniqueTitle(jobCtx, agentID, convID, summary)

	var titleText pgtype.Text
	titleText.String = uniqueTitle
	titleText.Valid = true
	if _, updateErr := tw.api.queries.UpdateConversation(jobCtx, db.UpdateConversationParams{
		ID:    convID,
		Title: titleText,
	}); updateErr != nil {
		slog.Warn("title worker: failed to update title", "conversation_id", convID.String(), "error", updateErr)
	} else {
		slog.Debug("title worker: updated conversation title", "conversation_id", convID.String(), "title", uniqueTitle)
	}
}

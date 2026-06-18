package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tim4net/agent-os/internal/db"
)

// ---------------------------------------------------------------------------
// Agent-to-agent messaging (WP-101, issue #112).
//
// Asynchronous mailbox layer that extends the synchronous delegation path
// (POST /agents/{id}/delegate) into full persistent messaging. All inbox reads
// are scoped by recipient_id — an agent can only ever read its own mail.
// ---------------------------------------------------------------------------

// maxMailBodyBytes caps the message body size (issue constraint: 64KB).
const maxMailBodyBytes = 64 * 1024

// mailRateLimitPerMin is the per-sender send budget within a rolling minute.
const mailRateLimitPerMin = 60

// mailLimiter is a simple in-memory, per-sender sliding-window rate limiter.
// It guards the send path against mail-spam. It is intentionally process-local:
// the orchestrator API is a single process, and a degraded limit under a future
// multi-replica deploy is a safe failure (sends are still bounded, just less
// strictly). Tested for correctness (over-limit returns 429).
type mailLimiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time // sender uuid string -> send timestamps
}

func newMailLimiter() *mailLimiter {
	return &mailLimiter{seen: make(map[string][]time.Time)}
}

// allow records a send for the sender and reports whether it is within budget.
func (ml *mailLimiter) allow(sender string, now time.Time) bool {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	cutoff := now.Add(-time.Minute)
	hits := ml.seen[sender]
	// Drop timestamps older than the 1-minute window.
	keep := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= mailRateLimitPerMin {
		ml.seen[sender] = keep
		return false
	}
	keep = append(keep, now)
	ml.seen[sender] = keep
	return true
}

// SendMailRequest is the body for POST /api/agents/{id}/mail/send.
type SendMailRequest struct {
	RecipientID string          `json:"recipient_id"`
	Subject     string          `json:"subject"`
	Body        string          `json:"body"`
	Priority    string          `json:"priority,omitempty"`
	ReplyToID   *int64          `json:"reply_to_id,omitempty"`
	ExpiresAt   *time.Time      `json:"expires_at,omitempty"`
	ContentType string          `json:"content_type,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// MailResponse is the API representation of an agent_mail row.
type MailResponse struct {
	ID          int64  `json:"id"`
	SenderID    string `json:"sender_id"`
	RecipientID string `json:"recipient_id"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	Priority    string `json:"priority"`
	Status      string `json:"status"`
	ReplyToID   *int64 `json:"reply_to_id,omitempty"`
	ContentType string `json:"content_type"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	ReadAt      string `json:"read_at,omitempty"`
	CreatedAt   string `json:"created_at"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// tsFmt is the RFC3339-ish format used elsewhere in the API for timestamps.
const tsFmt = "2006-01-02T15:04:05Z07:00"

// mailToResponse converts a db row to the API response shape.
func mailToResponse(m db.AgentMail) MailResponse {
	resp := MailResponse{
		ID:          m.ID,
		SenderID:    m.SenderID.String(),
		RecipientID: m.RecipientID.String(),
		Subject:     m.Subject,
		Body:        m.Body,
		Priority:    string(m.Priority),
		Status:      string(m.Status),
		ContentType: m.ContentType,
		CreatedAt:   m.CreatedAt.Time.Format(tsFmt),
		Metadata:    json.RawMessage(m.Metadata),
	}
	if m.ReplyToID.Valid {
		rid := m.ReplyToID.Int64
		resp.ReplyToID = &rid
	}
	if m.ExpiresAt.Valid {
		resp.ExpiresAt = m.ExpiresAt.Time.Format(tsFmt)
	}
	if m.ReadAt.Valid {
		resp.ReadAt = m.ReadAt.Time.Format(tsFmt)
	}
	return resp
}

// parsePriority validates and normalizes a priority string. Empty → "normal".
func parsePriority(s string) (db.MailPriority, bool) {
	switch s {
	case "", "normal":
		return db.MailPriorityNormal, true
	case "low":
		return db.MailPriorityLow, true
	case "high":
		return db.MailPriorityHigh, true
	case "urgent":
		return db.MailPriorityUrgent, true
	default:
		return "", false
	}
}

// SendMail handles POST /api/agents/{id}/mail/send.
// The {id} path agent is the sender.
func (a *API) SendMail(w http.ResponseWriter, r *http.Request) {
	senderID, ok := agentIDFromCtx(w, r)
	if !ok {
		return
	}
	ownerID := resolveOwnerID(r.Context())

	// Cap request size so an oversized body is rejected before full decode.
	r.Body = http.MaxBytesReader(w, r.Body, maxMailBodyBytes+4096)
	var req SendMailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.RecipientID == "" {
		http.Error(w, "recipient_id is required", http.StatusBadRequest)
		return
	}
	if req.Body == "" {
		http.Error(w, "body is required", http.StatusBadRequest)
		return
	}
	if len([]byte(req.Body)) > maxMailBodyBytes {
		http.Error(w, "body exceeds 64KB limit", http.StatusRequestEntityTooLarge)
		return
	}

	var recipientID pgtype.UUID
	if err := recipientID.Scan(req.RecipientID); err != nil {
		http.Error(w, "invalid recipient_id", http.StatusBadRequest)
		return
	}

	// No self-mail.
	if senderID == recipientID {
		http.Error(w, "sender and recipient must differ", http.StatusBadRequest)
		return
	}

	// Validate recipient exists (404 per the verification spec).
	if _, err := a.queries.GetAgent(r.Context(), db.GetAgentParams{ID: recipientID, OwnerID: ownerID}); err != nil {
		http.Error(w, "recipient agent not found", http.StatusNotFound)
		return
	}

	priority, ok := parsePriority(req.Priority)
	if !ok {
		http.Error(w, "invalid priority; must be low|normal|high|urgent", http.StatusBadRequest)
		return
	}

	// Rate limit per sender (prevent spam).
	if a.mailLimiter == nil {
		a.mailLimiter = newMailLimiter()
	}
	if !a.mailLimiter.allow(senderID.String(), time.Now()) {
		http.Error(w, "rate limit exceeded: max 60 messages/minute per sender", http.StatusTooManyRequests)
		return
	}

	meta := []byte("{}")
	if len(req.Metadata) > 0 {
		meta = req.Metadata
	}

	contentType := req.ContentType
	if contentType == "" {
		contentType = "notification"
	}

	var replyTo pgtype.Int8
	if req.ReplyToID != nil {
		replyTo = pgtype.Int8{Int64: *req.ReplyToID, Valid: true}
	}
	var expiresAt pgtype.Timestamptz
	if req.ExpiresAt != nil {
		expiresAt = pgtype.Timestamptz{Time: *req.ExpiresAt, Valid: true}
	}

	mail, err := a.queries.SendMail(r.Context(), db.SendMailParams{
		SenderID:    senderID,
		RecipientID: recipientID,
		Subject:     req.Subject,
		Body:        req.Body,
		Priority:    priority,
		Status:      db.MailStatusQueued,
		ReplyToID:   replyTo,
		Metadata:    meta,
		ContentType: contentType,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Broadcast SSE mail_received to the recipient channel.
	a.bus.PublishTyped("mail_received", map[string]any{
		"mail_id":      mail.ID,
		"recipient_id": mail.RecipientID.String(),
		"sender_id":    mail.SenderID.String(),
		"subject":      mail.Subject,
		"priority":     string(mail.Priority),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(mailToResponse(mail))
}

// ListInbox handles GET /api/agents/{id}/mail/inbox.
func (a *API) ListInbox(w http.ResponseWriter, r *http.Request) {
	recipientID, ok := agentIDFromCtx(w, r)
	if !ok {
		return
	}

	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 32)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 32)
	if offset < 0 {
		offset = 0
	}

	params := db.GetMailboxParams{
		RecipientID: recipientID,
		SkipRows:    int32(offset),
		MaxRows:     int32(limit),
	}
	if s := r.URL.Query().Get("status"); s != "" {
		params.Status = db.NullMailStatus{MailStatus: db.MailStatus(s), Valid: true}
	}
	if p := r.URL.Query().Get("priority"); p != "" {
		params.Priority = db.NullMailPriority{MailPriority: db.MailPriority(p), Valid: true}
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			params.Since = pgtype.Timestamptz{Time: t, Valid: true}
		}
	}

	messages, err := a.queries.GetMailbox(r.Context(), params)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Total + unread use the same filters (minus paging) / no filters.
	countParams := db.CountMailboxParams{
		RecipientID: recipientID,
		Status:      params.Status,
		Priority:    params.Priority,
		Since:       params.Since,
	}
	total, err := a.queries.CountMailbox(r.Context(), countParams)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	unread, err := a.queries.CountUnread(r.Context(), recipientID)
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := make([]MailResponse, len(messages))
	for i, m := range messages {
		resp[i] = mailToResponse(m)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"messages":     resp,
		"unread_count": unread,
		"total":        total,
	})
}

// GetInboxMessage handles GET /api/agents/{id}/mail/inbox/{mailId}.
func (a *API) GetInboxMessage(w http.ResponseWriter, r *http.Request) {
	recipientID, ok := agentIDFromCtx(w, r)
	if !ok {
		return
	}
	mailID, ok := mailIDFromCtx(w, r)
	if !ok {
		return
	}

	mail, err := a.queries.GetMail(r.Context(), db.GetMailParams{ID: mailID, RecipientID: recipientID})
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mailToResponse(mail))
}

// MarkInboxRead handles POST /api/agents/{id}/mail/inbox/{mailId}/read.
// Idempotent: an already-read message returns 200 with its current state.
func (a *API) MarkInboxRead(w http.ResponseWriter, r *http.Request) {
	recipientID, ok := agentIDFromCtx(w, r)
	if !ok {
		return
	}
	mailID, ok := mailIDFromCtx(w, r)
	if !ok {
		return
	}

	mail, err := a.queries.MarkMailRead(r.Context(), db.MarkMailReadParams{ID: mailID, RecipientID: recipientID})
	if err != nil {
		if err == pgx.ErrNoRows {
			// Either not found, or already read/expired. Fetch to distinguish.
			existing, gerr := a.queries.GetMail(r.Context(), db.GetMailParams{ID: mailID, RecipientID: recipientID})
			if gerr != nil {
				http.Error(w, "message not found", http.StatusNotFound)
				return
			}
			mail = existing
		} else {
			http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Delivery receipt: notify the sender that the message was read.
	a.bus.PublishTyped("mail_read", map[string]any{
		"mail_id":      mail.ID,
		"recipient_id": mail.RecipientID.String(),
		"sender_id":    mail.SenderID.String(),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mailToResponse(mail))
}

// ReplyInboxMessage handles POST /api/agents/{id}/mail/inbox/{mailId}/reply.
// The {id} agent (the replier) sends a new message to the original sender.
func (a *API) ReplyInboxMessage(w http.ResponseWriter, r *http.Request) {
	replierID, ok := agentIDFromCtx(w, r)
	if !ok {
		return
	}
	mailID, ok := mailIDFromCtx(w, r)
	if !ok {
		return
	}

	// The message being replied to must be in the replier's own inbox.
	original, err := a.queries.GetMail(r.Context(), db.GetMailParams{ID: mailID, RecipientID: replierID})
	if err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxMailBodyBytes+4096)
	var req struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.Body == "" {
		http.Error(w, "body is required", http.StatusBadRequest)
		return
	}
	if len([]byte(req.Body)) > maxMailBodyBytes {
		http.Error(w, "body exceeds 64KB limit", http.StatusRequestEntityTooLarge)
		return
	}

	// Recipient of the reply is the original sender.
	replyTo := original.ID
	mail, err := a.queries.SendMail(r.Context(), db.SendMailParams{
		SenderID:    replierID,
		RecipientID: original.SenderID,
		Subject:     req.Subject,
		Body:        req.Body,
		Priority:    db.MailPriorityNormal,
		Status:      db.MailStatusQueued,
		ReplyToID:   pgtype.Int8{Int64: replyTo, Valid: true},
		Metadata:    []byte("{}"),
		ContentType: original.ContentType,
	})
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	a.bus.PublishTyped("mail_replied", map[string]any{
		"mail_id":      mail.ID,
		"reply_to_id":  mail.ReplyToID.Int64,
		"sender_id":    mail.SenderID.String(),
		"recipient_id": mail.RecipientID.String(),
	})
	a.bus.PublishTyped("mail_received", map[string]any{
		"mail_id":      mail.ID,
		"recipient_id": mail.RecipientID.String(),
		"sender_id":    mail.SenderID.String(),
		"subject":      mail.Subject,
		"priority":     string(mail.Priority),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(mailToResponse(mail))
}

// DeleteInboxMessage handles DELETE /api/agents/{id}/mail/inbox/{mailId}.
// Soft-delete: status is set to expired, no row is removed.
func (a *API) DeleteInboxMessage(w http.ResponseWriter, r *http.Request) {
	recipientID, ok := agentIDFromCtx(w, r)
	if !ok {
		return
	}
	mailID, ok := mailIDFromCtx(w, r)
	if !ok {
		return
	}

	if _, err := a.queries.ExpireMailByID(r.Context(), db.ExpireMailByIDParams{ID: mailID, RecipientID: recipientID}); err != nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// agentIDFromCtx parses the {id} path param into a UUID and writes the error
// response on failure. Returns ok=false if the caller should abort.
func agentIDFromCtx(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	idStr := chi.URLParam(r, "id")
	var id pgtype.UUID
	if err := id.Scan(idStr); err != nil {
		http.Error(w, "invalid agent ID", http.StatusBadRequest)
		return pgtype.UUID{}, false
	}
	return id, true
}

// mailIDFromCtx parses the {mailId} path param into an int64.
func mailIDFromCtx(w http.ResponseWriter, r *http.Request) (int64, bool) {
	idStr := chi.URLParam(r, "mailId")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid mail ID", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// MailRoutes returns a router mounted under /api/agents/{id}/mail.
func (a *API) MailRoutes() http.Handler {
	if a.mailLimiter == nil {
		a.mailLimiter = newMailLimiter()
	}
	r := chi.NewRouter()
	r.Post("/send", a.SendMail)
	r.Get("/inbox", a.ListInbox)
	r.Route("/inbox/{mailId}", func(r chi.Router) {
		r.Get("/", a.GetInboxMessage)
		r.Post("/read", a.MarkInboxRead)
		r.Post("/reply", a.ReplyInboxMessage)
		r.Delete("/", a.DeleteInboxMessage)
	})
	return r
}

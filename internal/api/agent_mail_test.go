package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tim4net/agent-os/internal/db"
)

// mailTestSeed inserts two agents and returns their UUID strings.
func mailTestSeed(t *testing.T, pool *pgxpool.Pool) (sender, recipient string) {
	t.Helper()
	ctx := context.Background()
	sender = uuid.NewString()
	recipient = uuid.NewString()
	for i, id := range []string{sender, recipient} {
		name := fmt.Sprintf("mail-agent-%d-%s", i, id[:8])
		if _, err := pool.Exec(ctx,
			"INSERT INTO agents (id, name, display_name, harness, base_url) VALUES ($1,$2,$3,$4,$5)",
			id, name, name, "test", "http://localhost",
		); err != nil {
			t.Fatalf("seed agent %d: %v", i, err)
		}
	}
	return sender, recipient
}

// mailDo issues a request against the full API router (paths are relative to
// /api, e.g. "/agents/{id}/mail/send") and returns the recorder.
func mailDo(t *testing.T, a *API, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.Router().ServeHTTP(rec, req)
	return rec
}

func TestMail_SendThenInboxUnreadCount(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, recipient := mailTestSeed(t, pool)

	// POST /send → 201
	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": recipient,
		"subject":      "hello",
		"body":         "do the thing",
		"priority":     "high",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var sent MailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sent); err != nil {
		t.Fatalf("send: decode: %v", err)
	}
	if sent.Status != "queued" || sent.Priority != "high" {
		t.Fatalf("send: unexpected status=%s priority=%s", sent.Status, sent.Priority)
	}

	// GET /inbox → message present, unread_count = 1
	rec = mailDo(t, a, "GET", fmt.Sprintf("/agents/%s/mail/inbox", recipient), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("inbox: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var inbox struct {
		Messages    []MailResponse `json:"messages"`
		UnreadCount int64          `json:"unread_count"`
		Total       int64          `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &inbox); err != nil {
		t.Fatalf("inbox: decode: %v", err)
	}
	if inbox.UnreadCount != 1 {
		t.Fatalf("inbox: expected unread_count=1, got %d", inbox.UnreadCount)
	}
	if inbox.Total != 1 || len(inbox.Messages) != 1 {
		t.Fatalf("inbox: expected total=1 and 1 message, got total=%d len=%d", inbox.Total, len(inbox.Messages))
	}
	if inbox.Messages[0].Body != "do the thing" {
		t.Fatalf("inbox: unexpected body %q", inbox.Messages[0].Body)
	}

	// POST /inbox/{id}/read → 200, status=read
	rec = mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/inbox/%d/read", recipient, sent.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var read MailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("read: decode: %v", err)
	}
	if read.Status != "read" || read.ReadAt == "" {
		t.Fatalf("read: expected status=read with read_at set, got status=%s read_at=%q", read.Status, read.ReadAt)
	}

	// Re-check inbox: unread_count = 0, message status = read
	rec = mailDo(t, a, "GET", fmt.Sprintf("/agents/%s/mail/inbox", recipient), nil)
	var after struct {
		UnreadCount int64          `json:"unread_count"`
		Messages    []MailResponse `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("inbox recheck: decode: %v", err)
	}
	if after.UnreadCount != 0 {
		t.Fatalf("expected unread_count=0 after read, got %d", after.UnreadCount)
	}
	if len(after.Messages) != 1 || after.Messages[0].Status != "read" {
		t.Fatalf("expected message status=read, got %+v", after.Messages)
	}
}

func TestMail_ReplyFlipsSenderRecipient(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, recipient := mailTestSeed(t, pool)

	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": recipient,
		"body":         "original",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}
	var orig MailResponse
	json.Unmarshal(rec.Body.Bytes(), &orig)

	// Reply from recipient → new message whose recipient is the original sender.
	rec = mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/inbox/%d/reply", recipient, orig.ID), map[string]any{
		"subject": "re",
		"body":    "answer",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var reply MailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("reply decode: %v", err)
	}
	if reply.RecipientID != sender {
		t.Fatalf("reply recipient should be original sender %s, got %s", sender, reply.RecipientID)
	}
	if reply.SenderID != recipient {
		t.Fatalf("reply sender should be original recipient %s, got %s", recipient, reply.SenderID)
	}
	if reply.ReplyToID == nil || *reply.ReplyToID != orig.ID {
		t.Fatalf("reply reply_to_id should point at original %d, got %v", orig.ID, reply.ReplyToID)
	}

	// Original sender now has the reply in their inbox.
	rec = mailDo(t, a, "GET", fmt.Sprintf("/agents/%s/mail/inbox", sender), nil)
	var inbox struct {
		Messages []MailResponse `json:"messages"`
	}
	json.Unmarshal(rec.Body.Bytes(), &inbox)
	if len(inbox.Messages) != 1 || inbox.Messages[0].Body != "answer" {
		t.Fatalf("sender inbox should contain the reply, got %+v", inbox.Messages)
	}
}

func TestMail_PriorityOrdering(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, recipient := mailTestSeed(t, pool)

	// Send normal first, then low, then urgent, then high. Inbox must be ordered
	// urgent, high, normal, low (priority DESC) regardless of insert order.
	for _, p := range []string{"normal", "low", "urgent", "high"} {
		rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
			"recipient_id": recipient,
			"body":         "msg-" + p,
			"priority":     p,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("send %s: %d %s", p, rec.Code, rec.Body.String())
		}
		// Space inserts so created_at differs (created_at has microsecond
		// resolution; a tiny sleep guarantees strict ordering on equal priority).
		time.Sleep(2 * time.Millisecond)
	}

	rec := mailDo(t, a, "GET", fmt.Sprintf("/agents/%s/mail/inbox", recipient), nil)
	var inbox struct {
		Messages []MailResponse `json:"messages"`
	}
	json.Unmarshal(rec.Body.Bytes(), &inbox)
	if len(inbox.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(inbox.Messages))
	}
	want := []string{"urgent", "high", "normal", "low"}
	for i, w := range want {
		if inbox.Messages[i].Priority != w {
			ord := make([]string, len(inbox.Messages))
			for j, m := range inbox.Messages {
				ord[j] = m.Priority
			}
			t.Fatalf("position %d: expected %s, order was %v", i, w, ord)
		}
	}
}

func TestMail_ExpiredNotInInbox(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, recipient := mailTestSeed(t, pool)

	// Message with expires_at in the past.
	past := time.Now().Add(-1 * time.Hour).UTC()
	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": recipient,
		"body":         "should-not-appear",
		"expires_at":   past.Format(time.RFC3339Nano),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send expired: %d %s", rec.Code, rec.Body.String())
	}

	// A normal (non-expiring) message so the inbox is not empty by accident.
	rec = mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": recipient,
		"body":         "should-appear",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send live: %d %s", rec.Code, rec.Body.String())
	}

	rec = mailDo(t, a, "GET", fmt.Sprintf("/agents/%s/mail/inbox", recipient), nil)
	var inbox struct {
		Messages    []MailResponse `json:"messages"`
		UnreadCount int64          `json:"unread_count"`
	}
	json.Unmarshal(rec.Body.Bytes(), &inbox)
	if len(inbox.Messages) != 1 || inbox.Messages[0].Body != "should-appear" {
		t.Fatalf("expired message should be hidden; got %+v", inbox.Messages)
	}
	if inbox.UnreadCount != 1 {
		t.Fatalf("expired mail must not count as unread; got %d", inbox.UnreadCount)
	}
}

func TestMail_InvalidRecipientReturns404(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, _ := mailTestSeed(t, pool)
	bogusRecipient := uuid.NewString() // never inserted

	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": bogusRecipient,
		"body":         "nope",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown recipient, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMail_EmptyBodyReturns400(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, recipient := mailTestSeed(t, pool)

	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": recipient,
		"body":         "",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty body, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMail_SelfMailRejected(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, _ := mailTestSeed(t, pool)

	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": sender, // self
		"body":         "to myself",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for self-mail, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMail_InvalidPriorityReturns400(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, recipient := mailTestSeed(t, pool)

	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": recipient,
		"body":         "x",
		"priority":     "ridiculous",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad priority, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMail_InboxScopedToRecipient(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, recipient := mailTestSeed(t, pool)

	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": recipient,
		"body":         "private to recipient",
	})
	var sent MailResponse
	json.Unmarshal(rec.Body.Bytes(), &sent)

	// Sender (not the recipient) must NOT see the message in their inbox.
	rec = mailDo(t, a, "GET", fmt.Sprintf("/agents/%s/mail/inbox", sender), nil)
	var inbox struct {
		Messages []MailResponse `json:"messages"`
	}
	json.Unmarshal(rec.Body.Bytes(), &inbox)
	if len(inbox.Messages) != 0 {
		t.Fatalf("sender must not see recipient's mail; got %d", len(inbox.Messages))
	}

	// Direct GET of another agent's mail by the wrong agent → 404.
	rec = mailDo(t, a, "GET", fmt.Sprintf("/agents/%s/mail/inbox/%d", sender, sent.ID), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-agent read should be 404, got %d", rec.Code)
	}
}

func TestMail_DeleteSoftDeletes(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, recipient := mailTestSeed(t, pool)

	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": recipient,
		"body":         "delete me",
	})
	var sent MailResponse
	json.Unmarshal(rec.Body.Bytes(), &sent)

	rec = mailDo(t, a, "DELETE", fmt.Sprintf("/agents/%s/mail/inbox/%d", recipient, sent.ID), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Inbox now empty (expired mail is excluded from listings).
	rec = mailDo(t, a, "GET", fmt.Sprintf("/agents/%s/mail/inbox", recipient), nil)
	var inbox struct {
		Messages    []MailResponse `json:"messages"`
		UnreadCount int64          `json:"unread_count"`
	}
	json.Unmarshal(rec.Body.Bytes(), &inbox)
	if len(inbox.Messages) != 0 || inbox.UnreadCount != 0 {
		t.Fatalf("after delete inbox should be empty; got msgs=%d unread=%d", len(inbox.Messages), inbox.UnreadCount)
	}
}

func TestMail_GetSingleMessage(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, recipient := mailTestSeed(t, pool)

	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": recipient,
		"subject":      "detail",
		"body":         "fetch me",
		"priority":     "urgent",
	})
	var sent MailResponse
	json.Unmarshal(rec.Body.Bytes(), &sent)

	rec = mailDo(t, a, "GET", fmt.Sprintf("/agents/%s/mail/inbox/%d", recipient, sent.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get single: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got MailResponse
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ID != sent.ID || got.Subject != "detail" || got.Priority != "urgent" {
		t.Fatalf("get single mismatch: %+v", got)
	}
}

func TestMail_RateLimitEnforced(t *testing.T) {
	a, pool, _ := newTestAPIWithDB(t)
	defer pool.Close()
	sender, recipient := mailTestSeed(t, pool)

	// Negative test: prove the send guard actually throttles. Send one more than
	// the budget (61) and assert the 61st is rejected with 429.
	for i := 0; i < mailRateLimitPerMin; i++ {
		rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
			"recipient_id": recipient,
			"body":         fmt.Sprintf("msg-%d", i),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("send %d within budget should be 201, got %d: %s", i, rec.Code, rec.Body.String())
		}
	}
	// 61st → rejected.
	rec := mailDo(t, a, "POST", fmt.Sprintf("/agents/%s/mail/send", sender), map[string]any{
		"recipient_id": recipient,
		"body":         "one-too-many",
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-budget send should be 429, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestMailLimiter_Window ensures the limiter's sliding window logic drops stale
// hits and re-admits after the window passes (pure unit test, no DB).
func TestMailLimiter_Window(t *testing.T) {
	ml := newMailLimiter()
	now := time.Now()
	for i := 0; i < mailRateLimitPerMin; i++ {
		if !ml.allow("alice", now) {
			t.Fatalf("hit %d within window should be allowed", i)
		}
	}
	if ml.allow("alice", now) {
		t.Fatal("over-budget hit should be denied")
	}
	// One minute later, all prior hits are stale → re-admitted.
	if !ml.allow("alice", now.Add(time.Minute+time.Second)) {
		t.Fatal("after window expiry a new hit should be allowed")
	}
}

// Ensure the db package is referenced (guards against import pruning in builds
// that compile only this file's symbols).
var _ = db.MailStatusQueued

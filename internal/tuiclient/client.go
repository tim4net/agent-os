package tuiclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Agent represents an agent returned by the API.
type Agent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Harness     string `json:"harness"`
	Status      string `json:"status"`
}

// Client interacts with the Agent OS API.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient creates a new API client.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{},
	}
}

// ListAgents fetches the list of available agents.
func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/agents", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var agents []Agent
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		return nil, err
	}

	return agents, nil
}

// ChatRequest represents the payload for sending a chat message.
type ChatRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// Conversation represents a chat conversation as returned by the API.
type Conversation struct {
	ID        string  `json:"id"`
	AgentID   string  `json:"agent_id"`
	Title     string  `json:"title"`
	Summary   *string `json:"summary"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// Message represents a single stored message in a conversation.
type Message struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	Role           string `json:"role"`
	Content        string `json:"content"`
	CreatedAt      string `json:"created_at"`
}

// ListConversations fetches conversations for a given agent, newest first.
func (c *Client) ListConversations(ctx context.Context, agentID string) ([]Conversation, error) {
	u := fmt.Sprintf("%s/api/conversations?agent_id=%s", c.BaseURL, url.QueryEscape(agentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	var convs []Conversation
	if err := json.NewDecoder(resp.Body).Decode(&convs); err != nil {
		return nil, err
	}
	return convs, nil
}

// ListMessages fetches all messages in a conversation, oldest first.
func (c *Client) ListMessages(ctx context.Context, conversationID string) ([]Message, error) {
	u := fmt.Sprintf("%s/api/conversations/%s/messages", c.BaseURL, url.PathEscape(conversationID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	var msgs []Message
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// ChatEvent represents a single event from the chat stream.
type ChatEvent struct {
	Type               string // "info", "chunk", "tool", "error", "done"
	UserMessageID      string
	AssistantMessageID string
	ConversationID     string
	Content            string
	ToolName           string
	ToolStatus         string
	Error              string
	Done               bool
}

// StreamChat sends a chat message and returns a channel that yields events.
func (c *Client) StreamChat(ctx context.Context, agentID string, reqPayload ChatRequest) (<-chan ChatEvent, error) {
	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/agents/%s/chat", c.BaseURL, agentID), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	events := make(chan ChatEvent)
	go func() {
		defer resp.Body.Close()
		defer close(events)

		scanner := bufio.NewScanner(resp.Body)
		var currentEvent string

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimPrefix(line, "event: ")
				continue
			}

			if strings.HasPrefix(line, "data: ") {
				dataStr := strings.TrimPrefix(line, "data: ")

				var evt ChatEvent
				evt.Type = currentEvent

				switch currentEvent {
				case "info":
					var payload struct {
						UserMessageID string `json:"user_message_id"`
					}
					if err := json.Unmarshal([]byte(dataStr), &payload); err == nil {
						evt.UserMessageID = payload.UserMessageID
					}
				case "chunk":
					var payload struct {
						Content string `json:"content"`
					}
					if err := json.Unmarshal([]byte(dataStr), &payload); err == nil {
						evt.Content = payload.Content
					}
				case "tool":
					var payload struct {
						ToolName   string `json:"tool_name"`
						ToolStatus string `json:"tool_status"`
					}
					if err := json.Unmarshal([]byte(dataStr), &payload); err == nil {
						evt.ToolName = payload.ToolName
						evt.ToolStatus = payload.ToolStatus
					}
				case "error":
					var payload struct {
						Error string `json:"error"`
					}
					if err := json.Unmarshal([]byte(dataStr), &payload); err == nil {
						evt.Error = payload.Error
					}
				case "done":
					var payload struct {
						UserMessageID      string `json:"user_message_id"`
						AssistantMessageID string `json:"assistant_message_id"`
						ConversationID     string `json:"conversation_id"`
						Done               bool   `json:"done"`
					}
					if err := json.Unmarshal([]byte(dataStr), &payload); err == nil {
						evt.UserMessageID = payload.UserMessageID
						evt.AssistantMessageID = payload.AssistantMessageID
						evt.ConversationID = payload.ConversationID
						evt.Done = payload.Done
					}
				}

				select {
				case events <- evt:
				case <-ctx.Done():
					return
				}

				// Reset currentEvent for next potential frame
				currentEvent = ""
			}
		}

		if err := scanner.Err(); err != nil {
			select {
			case events <- ChatEvent{Type: "error", Error: err.Error()}:
			case <-ctx.Done():
			}
		}
	}()

	return events, nil
}

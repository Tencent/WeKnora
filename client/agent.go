// Package client provides the implementation for interacting with the WeKnora API
// The Agent related interfaces are used to manage agent-based question-answering
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MentionedItem represents a mentioned item in the request
type MentionedItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`    // "kb" for knowledge base, "file" for file
	KBType string `json:"kb_type"` // "document" or "faq" (only for kb type)
}

// AgentQARequest agent Q&A request payload.
type AgentQARequest struct {
	Query            string            `json:"query"`                        // Required query text
	KnowledgeBaseIDs []string          `json:"knowledge_base_ids,omitempty"` // Optional KBs for this query
	KnowledgeIDs     []string          `json:"knowledge_ids,omitempty"`      // Optional specific knowledge IDs for this query
	AgentEnabled     bool              `json:"agent_enabled"`                // Whether to run in agent mode
	AgentID          string            `json:"agent_id,omitempty"`           // Optional custom agent ID
	WebSearchEnabled bool              `json:"web_search_enabled"`           // Whether to enable web search
	SummaryModelID   string            `json:"summary_model_id,omitempty"`   // Optional summary model override
	MentionedItems   []MentionedItem   `json:"mentioned_items,omitempty"`    // @mentioned knowledge bases and files
	DisableTitle     bool              `json:"disable_title,omitempty"`      // Whether to disable auto title generation
	MCPServiceIDs    []string          `json:"mcp_service_ids,omitempty"`    // Optional MCP service allow list (deprecated)
	Images           []ImageAttachment `json:"images,omitempty"`             // Attached images for multimodal chat
	Channel          string            `json:"channel,omitempty"`            // Source channel: "web", "api", "im", etc.
}

// AgentResponseType defines the type of agent response
type AgentResponseType string

const (
	AgentResponseTypeThinking   AgentResponseType = "thinking"
	AgentResponseTypeToolCall   AgentResponseType = "tool_call"
	AgentResponseTypeToolResult AgentResponseType = "tool_result"
	AgentResponseTypeReferences AgentResponseType = "references"
	AgentResponseTypeAnswer     AgentResponseType = "answer"
	AgentResponseTypeReflection AgentResponseType = "reflection"
	AgentResponseTypeError      AgentResponseType = "error"
)

// AgentStreamResponse agent streaming response
type AgentStreamResponse struct {
	ID                  string                 `json:"id"`                   // Unique identifier
	ResponseType        AgentResponseType      `json:"response_type"`        // Response type
	Content             string                 `json:"content,omitempty"`    // Current content fragment
	Done                bool                   `json:"done"`                 // Whether completed
	KnowledgeReferences []*SearchResult        `json:"knowledge_references"` // Knowledge references
	Data                map[string]interface{} `json:"data,omitempty"`       // Additional event data
}

// AgentEventCallback is called for each streaming event
// Return error to stop processing the stream
type AgentEventCallback func(*AgentStreamResponse) error

// AgentQAStream performs agent-based Q&A with SSE streaming using default agent settings.
// Deprecated: prefer AgentQAStreamWithRequest to customize agent behavior.
func (c *Client) AgentQAStream(ctx context.Context, sessionID string, query string, callback AgentEventCallback) error {
	req := &AgentQARequest{
		Query:        query,
		AgentEnabled: true,
	}
	return c.AgentQAStreamWithRequest(ctx, sessionID, req, callback)
}

// AgentQAStreamWithRequest performs agent-based Q&A with SSE streaming using the full request payload.
func (c *Client) AgentQAStreamWithRequest(ctx context.Context,
	sessionID string, request *AgentQARequest, callback AgentEventCallback,
) error {
	if request == nil {
		return fmt.Errorf("agent QA request cannot be nil")
	}
	if strings.TrimSpace(request.Query) == "" {
		return fmt.Errorf("agent QA query cannot be empty")
	}

	path := fmt.Sprintf("/api/v1/agent-chat/%s", sessionID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, request, nil)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	// Process SSE stream
	return c.processAgentSSEStream(resp.Body, callback)
}

// processAgentSSEStream processes the SSE stream and invokes callback for each event
func (c *Client) processAgentSSEStream(reader io.Reader, callback AgentEventCallback) error {
	scanner := bufio.NewScanner(reader)
	var dataBuffer string

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line indicates the end of an event
		if line == "" {
			if dataBuffer != "" {
				var streamResponse AgentStreamResponse
				if err := json.Unmarshal([]byte(dataBuffer), &streamResponse); err != nil {
					return fmt.Errorf("failed to parse SSE data: %w", err)
				}

				if err := callback(&streamResponse); err != nil {
					return err
				}
				dataBuffer = ""
			}
			continue
		}

		// Process lines with event: prefix (for future use)
		if strings.HasPrefix(line, "event:") {
			// Event type is available but not currently used
			// eventType := strings.TrimSpace(line[6:])
			continue
		}

		// Process lines with data: prefix
		if strings.HasPrefix(line, "data:") {
			dataBuffer = strings.TrimSpace(line[5:]) // Remove "data:" prefix
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read SSE stream: %w", err)
	}

	return nil
}

// AgentSession is a wrapper for agent-based interactions
type AgentSession struct {
	client    *Client
	sessionID string
}

// NewAgentSession creates a new agent session wrapper
func (c *Client) NewAgentSession(sessionID string) *AgentSession {
	return &AgentSession{
		client:    c,
		sessionID: sessionID,
	}
}

// Ask sends a query to the agent with default agent-enabled behavior.
func (as *AgentSession) Ask(ctx context.Context, query string, callback AgentEventCallback) error {
	return as.client.AgentQAStream(ctx, as.sessionID, query, callback)
}

// AskWithRequest sends a customized agent request for this session.
func (as *AgentSession) AskWithRequest(
	ctx context.Context,
	request *AgentQARequest,
	callback AgentEventCallback,
) error {
	return as.client.AgentQAStreamWithRequest(ctx, as.sessionID, request, callback)
}

// GetSessionID returns the session ID
func (as *AgentSession) GetSessionID() string {
	return as.sessionID
}

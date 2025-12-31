// Package client provides the implementation for interacting with the WeKnora API
// The Session related interfaces are used to manage sessions for question-answering
// Sessions can be created, retrieved, updated, deleted, and queried
// They can also be used to generate titles for sessions
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SummaryConfig defines summary configuration
type SummaryConfig struct {
	MaxTokens           int     `json:"max_tokens"`
	TopP                float64 `json:"top_p"`
	TopK                int     `json:"top_k"`
	FrequencyPenalty    float64 `json:"frequency_penalty"`
	PresencePenalty     float64 `json:"presence_penalty"`
	RepeatPenalty       float64 `json:"repeat_penalty"`
	Prompt              string  `json:"prompt"`
	ContextTemplate     string  `json:"context_template"`
	NoMatchPrefix       string  `json:"no_match_prefix"`
	Temperature         float64 `json:"temperature"`
	Seed                int     `json:"seed"`
	MaxCompletionTokens int     `json:"max_completion_tokens"`
	Thinking            *bool   `json:"thinking"`
}

// CreateSessionRequest session creation request
// Sessions are now knowledge-base-independent and serve as conversation containers.
// All configuration comes from custom agent at query time.
type CreateSessionRequest struct {
	Title       string `json:"title"`       // Session title (optional)
	Description string `json:"description"` // Session description (optional)
}

// Session session information
type Session struct {
	ID          string `json:"id"`
	TenantID    uint64 `json:"tenant_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// SessionResponse session response
type SessionResponse struct {
	Success bool    `json:"success"`
	Data    Session `json:"data"`
}

// SessionListResponse session list response
type SessionListResponse struct {
	Success  bool      `json:"success"`
	Data     []Session `json:"data"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}

// CreateSession creates a session
func (c *Client) CreateSession(ctx context.Context, request *CreateSessionRequest) (*Session, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/sessions", request, nil)
	if err != nil {
		return nil, err
	}

	var response SessionResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// GetSession gets a session
func (c *Client) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	path := fmt.Sprintf("/api/v1/sessions/%s", sessionID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response SessionResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// GetSessionsByTenant gets all sessions for a tenant
func (c *Client) GetSessionsByTenant(ctx context.Context, page int, pageSize int) ([]Session, int, error) {
	queryParams := url.Values{}
	queryParams.Add("page", strconv.Itoa(page))
	queryParams.Add("page_size", strconv.Itoa(pageSize))
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/sessions", nil, queryParams)
	if err != nil {
		return nil, 0, err
	}

	var response SessionListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, 0, err
	}

	return response.Data, response.Total, nil
}

// UpdateSession updates a session
func (c *Client) UpdateSession(ctx context.Context, sessionID string, request *CreateSessionRequest) (*Session, error) {
	path := fmt.Sprintf("/api/v1/sessions/%s", sessionID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, request, nil)
	if err != nil {
		return nil, err
	}

	var response SessionResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// DeleteSession deletes a session
func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	path := fmt.Sprintf("/api/v1/sessions/%s", sessionID)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}

	return parseResponse(resp, &response)
}

// GenerateTitleRequest title generation request
type GenerateTitleRequest struct {
	Messages []Message `json:"messages"`
}

// GenerateTitleResponse title generation response
type GenerateTitleResponse struct {
	Success bool   `json:"success"`
	Data    string `json:"data"`
}

// StopSessionRequest stop generation payload.
type StopSessionRequest struct {
	MessageID string `json:"message_id"`
}

// GenerateTitle generates a session title
func (c *Client) GenerateTitle(ctx context.Context, sessionID string, request *GenerateTitleRequest) (string, error) {
	path := fmt.Sprintf("/api/v1/sessions/%s/generate_title", sessionID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, request, nil)
	if err != nil {
		return "", err
	}

	var response GenerateTitleResponse
	if err := parseResponse(resp, &response); err != nil {
		return "", err
	}

	return response.Data, nil
}

// KnowledgeQARequest knowledge Q&A request
type KnowledgeQARequest struct {
	Query            string   `json:"query"`              // Query text for knowledge base search
	KnowledgeBaseIDs []string `json:"knowledge_base_ids"` // Selected knowledge base IDs for this request
	KnowledgeIDs     []string `json:"knowledge_ids"`      // Selected knowledge IDs for this request
	AgentEnabled     bool     `json:"agent_enabled"`      // Whether agent mode is enabled for this request
	AgentID          string   `json:"agent_id"`           // Selected custom agent ID for this request
	WebSearchEnabled bool     `json:"web_search_enabled"` // Whether web search is enabled for this request
	SummaryModelID   string   `json:"summary_model_id"`   // Optional summary model ID (overrides session default)
	DisableTitle     bool     `json:"disable_title"`      // Whether to disable auto title generation
}

// LLMToolCall represents a function/tool call from the LLM
type LLMToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall represents the function details
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type ResponseType string

const (
	ResponseTypeAnswer       ResponseType = "answer"
	ResponseTypeReferences   ResponseType = "references"
	ResponseTypeThinking     ResponseType = "thinking"
	ResponseTypeToolCall     ResponseType = "tool_call"
	ResponseTypeToolResult   ResponseType = "tool_result"
	ResponseTypeError        ResponseType = "error"
	ResponseTypeReflection   ResponseType = "reflection"
	ResponseTypeSessionTitle ResponseType = "session_title"
	ResponseTypeAgentQuery   ResponseType = "agent_query"
	ResponseTypeComplete     ResponseType = "complete"
)

// StreamResponse streaming response
type StreamResponse struct {
	ID                  string                 `json:"id"`                             // Unique identifier
	ResponseType        ResponseType           `json:"response_type"`                  // Response type
	Content             string                 `json:"content"`                        // Current content fragment
	Done                bool                   `json:"done"`                           // Whether completed
	KnowledgeReferences []*SearchResult        `json:"knowledge_references,omitempty"` // Knowledge references
	SessionID           string                 `json:"session_id,omitempty"`           // Session ID (for agent_query event)
	AssistantMessageID  string                 `json:"assistant_message_id,omitempty"` // Assistant Message ID (for agent_query event)
	ToolCalls           []LLMToolCall          `json:"tool_calls,omitempty"`           // Tool calls for streaming (partial)
	Data                map[string]interface{} `json:"data,omitempty"`                 // Additional metadata for enhanced display
}

// KnowledgeQAStream knowledge Q&A streaming API
func (c *Client) KnowledgeQAStream(
	ctx context.Context,
	sessionID string,
	request *KnowledgeQARequest,
	callback func(*StreamResponse) error,
) error {
	path := fmt.Sprintf("/api/v1/knowledge-chat/%s", sessionID)
	fmt.Printf("Starting KnowledgeQAStream request, session ID: %s, query: %s\n", sessionID, request.Query)

	resp, err := c.doRequest(ctx, http.MethodPost, path, request, nil)
	if err != nil {
		fmt.Printf("Request failed: %v\n", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
		fmt.Printf("Request returned error status: %v\n", err)
		return err
	}

	fmt.Println("Successfully established SSE connection, processing data stream")

	// Use bufio to read SSE data line by line
	scanner := bufio.NewScanner(resp.Body)
	var dataBuffer string
	var eventType string
	messageCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("Received SSE line: %s\n", line)

		// Empty line indicates the end of an event
		if line == "" {
			if dataBuffer != "" {
				fmt.Printf("Processing data: %s, event type: %s\n", dataBuffer, eventType)
				var streamResponse StreamResponse
				if err := json.Unmarshal([]byte(dataBuffer), &streamResponse); err != nil {
					fmt.Printf("Failed to parse SSE data: %v\n", err)
					return fmt.Errorf("failed to parse SSE data: %w", err)
				}

				messageCount++
				fmt.Printf("Parsed message #%d, done status: %v\n", messageCount, streamResponse.Done)

				if err := callback(&streamResponse); err != nil {
					fmt.Printf("Callback processing failed: %v\n", err)
					return err
				}
				dataBuffer = ""
				eventType = ""
			}
			continue
		}

		// Process lines with event: prefix
		if strings.HasPrefix(line, "event:") {
			eventType = line[6:] // Remove "event:" prefix
			fmt.Printf("Set event type: %s\n", eventType)
		}

		// Process lines with data: prefix
		if strings.HasPrefix(line, "data:") {
			dataBuffer = line[5:] // Remove "data:" prefix
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Failed to read SSE stream: %v\n", err)
		return fmt.Errorf("failed to read SSE stream: %w", err)
	}

	fmt.Printf("KnowledgeQAStream completed, processed %d messages\n", messageCount)
	return nil
}

// ContinueStream continues to receive an active stream for a session
func (c *Client) ContinueStream(
	ctx context.Context,
	sessionID string,
	messageID string,
	callback func(*StreamResponse) error,
) error {
	path := fmt.Sprintf("/api/v1/sessions/continue-stream/%s", sessionID)

	queryParams := url.Values{}
	queryParams.Add("message_id", messageID)

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, queryParams)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	// Use bufio to read SSE data line by line
	scanner := bufio.NewScanner(resp.Body)
	var dataBuffer string
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line indicates the end of an event
		if line == "" {
			if dataBuffer != "" && eventType == "message" {
				var streamResponse StreamResponse
				if err := json.Unmarshal([]byte(dataBuffer), &streamResponse); err != nil {
					return fmt.Errorf("failed to parse SSE data: %w", err)
				}

				if err := callback(&streamResponse); err != nil {
					return err
				}
				dataBuffer = ""
				eventType = ""
			}
			continue
		}

		// Process lines with event: prefix
		if strings.HasPrefix(line, "event:") {
			eventType = line[6:] // Remove "event:" prefix
		}

		// Process lines with data: prefix
		if strings.HasPrefix(line, "data:") {
			dataBuffer = line[5:] // Remove "data:" prefix
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read SSE stream: %w", err)
	}

	return nil
}

// StopSession stops the generation for a specific assistant message under a session.
func (c *Client) StopSession(ctx context.Context, sessionID string, messageID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("sessionID cannot be empty")
	}
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("messageID cannot be empty")
	}

	path := fmt.Sprintf("/api/v1/sessions/%s/stop", sessionID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, &StopSessionRequest{
		MessageID: messageID,
	}, nil)
	if err != nil {
		return err
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}

	return parseResponse(resp, &response)
}

// SearchKnowledgeRequest knowledge search request
type SearchKnowledgeRequest struct {
	Query            string   `json:"query"`                        // Query content
	KnowledgeBaseID  string   `json:"knowledge_base_id,omitempty"`  // Single knowledge base ID (for backward compatibility)
	KnowledgeBaseIDs []string `json:"knowledge_base_ids,omitempty"` // Knowledge base IDs (multi-KB support)
	KnowledgeIDs     []string `json:"knowledge_ids,omitempty"`      // Specific knowledge (file) IDs
}

// SearchKnowledgeResponse search results response
type SearchKnowledgeResponse struct {
	Success bool            `json:"success"`
	Data    []*SearchResult `json:"data"`
}

// SearchKnowledge performs knowledge base search without LLM summarization
func (c *Client) SearchKnowledge(ctx context.Context, request *SearchKnowledgeRequest) ([]*SearchResult, error) {
	fmt.Printf("Starting SearchKnowledge request, knowledge base IDs: %v, knowledge IDs: %v, query: %s\n",
		request.KnowledgeBaseIDs, request.KnowledgeIDs, request.Query)

	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/knowledge-search", request, nil)
	if err != nil {
		fmt.Printf("Request failed: %v\n", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
		fmt.Printf("Request returned error status: %v\n", err)
		return nil, err
	}

	var response SearchKnowledgeResponse
	if err := parseResponse(resp, &response); err != nil {
		fmt.Printf("Failed to parse response: %v\n", err)
		return nil, err
	}

	fmt.Printf("SearchKnowledge completed, found %d results\n", len(response.Data))
	return response.Data, nil
}

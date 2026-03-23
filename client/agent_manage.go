// Package client provides the implementation for interacting with the WeKnora API
// The Agent management interfaces are used to manage custom agents (CRUD operations)
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Agent represents an agent entity
type Agent struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Avatar      string       `json:"avatar"`
	IsBuiltin   bool         `json:"is_builtin"`
	TenantID    uint64       `json:"tenant_id"`
	CreatedBy   string       `json:"created_by"`
	Config      *AgentConfig `json:"config"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// AgentConfig represents the configuration for an agent
type AgentConfig struct {
	AgentMode                string   `json:"agent_mode"` // "quick-answer" or "smart-reasoning"
	SystemPrompt             string   `json:"system_prompt,omitempty"`
	ContextTemplate          string   `json:"context_template,omitempty"`
	ModelID                  string   `json:"model_id,omitempty"`
	RerankModelID            string   `json:"rerank_model_id,omitempty"`
	Temperature              float64  `json:"temperature,omitempty"`
	MaxCompletionTokens      int      `json:"max_completion_tokens,omitempty"`
	MaxIterations            int      `json:"max_iterations,omitempty"`
	AllowedTools             []string `json:"allowed_tools,omitempty"`
	MCPSelectionMode         string   `json:"mcp_selection_mode,omitempty"` // "all", "selected", "none"
	MCPServices              []string `json:"mcp_services,omitempty"`
	KBSelectionMode          string   `json:"kb_selection_mode,omitempty"` // "all", "selected", "none"
	KnowledgeBases           []string `json:"knowledge_bases,omitempty"`
	SupportedFileTypes       []string `json:"supported_file_types,omitempty"`
	FAQPriorityEnabled       bool     `json:"faq_priority_enabled,omitempty"`
	FAQDirectAnswerThreshold float64  `json:"faq_direct_answer_threshold,omitempty"`
	FAQScoreBoost            float64  `json:"faq_score_boost,omitempty"`
	WebSearchEnabled         bool     `json:"web_search_enabled,omitempty"`
	WebSearchMaxResults      int      `json:"web_search_max_results,omitempty"`
	MultiTurnEnabled         bool     `json:"multi_turn_enabled,omitempty"`
	HistoryTurns             int      `json:"history_turns,omitempty"`
	EmbeddingTopK            int      `json:"embedding_top_k,omitempty"`
	KeywordThreshold         float64  `json:"keyword_threshold,omitempty"`
	VectorThreshold          float64  `json:"vector_threshold,omitempty"`
	RerankTopK               int      `json:"rerank_top_k,omitempty"`
	RerankThreshold          float64  `json:"rerank_threshold,omitempty"`
	EnableQueryExpansion     bool     `json:"enable_query_expansion,omitempty"`
	EnableRewrite            bool     `json:"enable_rewrite,omitempty"`
	RewritePromptSystem      string   `json:"rewrite_prompt_system,omitempty"`
	RewritePromptUser        string   `json:"rewrite_prompt_user,omitempty"`
	FallbackStrategy         string   `json:"fallback_strategy,omitempty"` // "fixed" or "model"
	FallbackResponse         string   `json:"fallback_response,omitempty"`
	FallbackPrompt           string   `json:"fallback_prompt,omitempty"`
}

// CreateAgentRequest represents the request to create an agent
type CreateAgentRequest struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Avatar      string       `json:"avatar,omitempty"`
	Config      *AgentConfig `json:"config,omitempty"`
}

// UpdateAgentRequest represents the request to update an agent
type UpdateAgentRequest struct {
	Name        string       `json:"name,omitempty"`
	Description string       `json:"description,omitempty"`
	Avatar      string       `json:"avatar,omitempty"`
	Config      *AgentConfig `json:"config,omitempty"`
}

// AgentResponse represents the API response containing a single agent
type AgentResponse struct {
	Success bool  `json:"success"`
	Data    Agent `json:"data"`
}

// AgentListResponse represents the API response containing a list of agents
type AgentListResponse struct {
	Success bool    `json:"success"`
	Data    []Agent `json:"data"`
}

// AgentPlaceholdersResponse represents the API response for placeholder definitions
type AgentPlaceholdersResponse struct {
	Success bool                       `json:"success"`
	Data    map[string]json.RawMessage `json:"data"`
}

// CreateAgent creates a new custom agent
func (c *Client) CreateAgent(ctx context.Context, request *CreateAgentRequest) (*Agent, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/agents", request, nil)
	if err != nil {
		return nil, err
	}

	var response AgentResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// ListAgents retrieves all agents for the current tenant
func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/agents", nil, nil)
	if err != nil {
		return nil, err
	}

	var response AgentListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

// GetAgent retrieves an agent by its ID
func (c *Client) GetAgent(ctx context.Context, agentID string) (*Agent, error) {
	path := fmt.Sprintf("/api/v1/agents/%s", agentID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response AgentResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// UpdateAgent updates an existing agent
func (c *Client) UpdateAgent(ctx context.Context, agentID string, request *UpdateAgentRequest) (*Agent, error) {
	path := fmt.Sprintf("/api/v1/agents/%s", agentID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, request, nil)
	if err != nil {
		return nil, err
	}

	var response AgentResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// DeleteAgent deletes a custom agent by its ID
func (c *Client) DeleteAgent(ctx context.Context, agentID string) error {
	path := fmt.Sprintf("/api/v1/agents/%s", agentID)
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

// CopyAgent creates a copy of an existing agent
func (c *Client) CopyAgent(ctx context.Context, agentID string) (*Agent, error) {
	path := fmt.Sprintf("/api/v1/agents/%s/copy", agentID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response AgentResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// GetAgentPlaceholders retrieves all available prompt placeholder definitions
func (c *Client) GetAgentPlaceholders(ctx context.Context) (map[string]json.RawMessage, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/agents/placeholders", nil, nil)
	if err != nil {
		return nil, err
	}

	var response AgentPlaceholdersResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

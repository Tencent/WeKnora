// Package client provides the implementation for interacting with the WeKnora API
// The Agent management interfaces are used to manage custom agents (CRUD operations)
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	CreatorName string       `json:"creator_name,omitempty"`
}

// AgentConfig represents the configuration for an agent.
// Field names and JSON tags mirror internal/types.CustomAgentConfig.
type AgentConfig struct {
	AgentMode                   string   `json:"agent_mode"`
	AgentType                   string   `json:"agent_type,omitempty"`
	SystemPrompt                string   `json:"system_prompt"`
	SystemPromptID              string   `json:"system_prompt_id,omitempty"`
	ContextTemplate             string   `json:"context_template"`
	ContextTemplateID           string   `json:"context_template_id,omitempty"`
	ModelID                     string   `json:"model_id"`
	RerankModelID               string   `json:"rerank_model_id"`
	Temperature                 float64  `json:"temperature"`
	MaxCompletionTokens         int      `json:"max_completion_tokens"`
	Thinking                    *bool    `json:"thinking"`
	MaxIterations               int      `json:"max_iterations"`
	LLMCallTimeout              int      `json:"llm_call_timeout,omitempty"`
	AllowedTools                []string `json:"allowed_tools"`
	MCPSelectionMode            string   `json:"mcp_selection_mode"`
	MCPServices                 []string `json:"mcp_services"`
	SkillsSelectionMode         string   `json:"skills_selection_mode"`
	SelectedSkills              []string `json:"selected_skills"`
	KBSelectionMode             string   `json:"kb_selection_mode"`
	KnowledgeBases              []string `json:"knowledge_bases"`
	RetrieveKBOnlyWhenMentioned bool     `json:"retrieve_kb_only_when_mentioned"`
	RetainRetrievalHistory      bool     `json:"retain_retrieval_history"`
	ImageUploadEnabled          bool     `json:"image_upload_enabled"`
	VLMModelID                  string   `json:"vlm_model_id"`
	AudioUploadEnabled          bool     `json:"audio_upload_enabled"`
	ASRModelID                  string   `json:"asr_model_id"`
	ImageStorageProvider        string   `json:"image_storage_provider"`
	SupportedFileTypes          []string `json:"supported_file_types"`
	DataAnalysisEnabled         bool     `json:"data_analysis_enabled"`
	FAQPriorityEnabled          bool     `json:"faq_priority_enabled"`
	FAQDirectAnswerThreshold    float64  `json:"faq_direct_answer_threshold"`
	FAQScoreBoost               float64  `json:"faq_score_boost"`
	WebSearchEnabled            bool     `json:"web_search_enabled"`
	WebSearchMaxResults         int      `json:"web_search_max_results"`
	WebSearchProviderID         string   `json:"web_search_provider_id,omitempty"`
	WebFetchEnabled             bool     `json:"web_fetch_enabled"`
	WebFetchTopN                int      `json:"web_fetch_top_n,omitempty"`
	MultiTurnEnabled            bool     `json:"multi_turn_enabled"`
	HistoryTurns                int      `json:"history_turns"`
	EmbeddingTopK               int      `json:"embedding_top_k"`
	KeywordThreshold            float64  `json:"keyword_threshold"`
	VectorThreshold             float64  `json:"vector_threshold"`
	RerankTopK                  int      `json:"rerank_top_k"`
	RerankThreshold             float64  `json:"rerank_threshold"`
	EnableQueryExpansion        bool     `json:"enable_query_expansion"`
	EnableRewrite               bool     `json:"enable_rewrite"`
	RewritePromptSystem         string   `json:"rewrite_prompt_system"`
	RewritePromptUser           string   `json:"rewrite_prompt_user"`
	QueryUnderstandModelID      string   `json:"query_understand_model_id,omitempty"`
	FallbackStrategy            string   `json:"fallback_strategy"`
	FallbackResponse            string   `json:"fallback_response"`
	FallbackPrompt              string   `json:"fallback_prompt"`
	SuggestedPrompts            []string `json:"suggested_prompts,omitempty"`
}

// CreateAgentRequest represents the request to create an agent.
// JSON field names mirror internal/handler.CreateAgentRequest.
type CreateAgentRequest struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Avatar      string       `json:"avatar"`
	Config      *AgentConfig `json:"config"`
}

// UpdateAgentRequest represents the request to update an agent.
// JSON field names mirror internal/handler.UpdateAgentRequest.
type UpdateAgentRequest struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Avatar      string       `json:"avatar"`
	Config      *AgentConfig `json:"config"`
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

// SuggestedQuestion represents a suggested question for an agent
type SuggestedQuestion struct {
	Question        string `json:"question"`                    // Question text
	Source          string `json:"source"`                      // Source: "faq", "document", or "agent_config"
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"` // Source knowledge base ID
}

// SuggestedQuestionsRequest represents the options for getting suggested questions
type SuggestedQuestionsRequest struct {
	KnowledgeBaseIDs []string // Optional: override agent's KB scope
	KnowledgeIDs     []string // Optional: limit to specific knowledge items
	TagIDs           []string // Optional: limit to knowledge items under these tags
	Limit            int      // Optional: max questions to return (default 6)
}

// SuggestedQuestionsResponse represents the API response for suggested questions
type SuggestedQuestionsResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Questions []SuggestedQuestion `json:"questions"`
	} `json:"data"`
}

// GetSuggestedQuestions retrieves suggested questions for the given agent,
// based on its associated knowledge bases. The returned questions can be
// displayed as quick-start prompts in the chat UI.
//
// When request is nil, uses the agent's default knowledge base configuration.
func (c *Client) GetSuggestedQuestions(ctx context.Context, agentID string, request *SuggestedQuestionsRequest) ([]SuggestedQuestion, error) {
	path := fmt.Sprintf("/api/v1/agents/%s/suggested-questions", agentID)

	query := url.Values{}
	if request != nil {
		if len(request.KnowledgeBaseIDs) > 0 {
			query.Set("knowledge_base_ids", strings.Join(request.KnowledgeBaseIDs, ","))
		}
		if len(request.KnowledgeIDs) > 0 {
			query.Set("knowledge_ids", strings.Join(request.KnowledgeIDs, ","))
		}
		if len(request.TagIDs) > 0 {
			query.Set("tag_ids", strings.Join(request.TagIDs, ","))
		}
		if request.Limit > 0 {
			query.Set("limit", strconv.Itoa(request.Limit))
		}
	}

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, query)
	if err != nil {
		return nil, err
	}

	var response SuggestedQuestionsResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data.Questions, nil
}

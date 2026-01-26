package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// BuiltinAgentID constants for built-in agents
const (
	// BuiltinQuickAnswerID is the ID for the built-in quick answer (RAG) agent
	BuiltinQuickAnswerID = "builtin-quick-answer"
	// BuiltinSmartReasoningID is the ID for the built-in smart reasoning (ReAct) agent
	BuiltinSmartReasoningID = "builtin-smart-reasoning"
	// BuiltinDeepResearcherID is the ID for the built-in deep researcher agent
	BuiltinDeepResearcherID = "builtin-deep-researcher"
	// BuiltinDataAnalystID is the ID for the built-in data analyst agent
	BuiltinDataAnalystID = "builtin-data-analyst"
	// BuiltinKnowledgeGraphExpertID is the ID for the built-in knowledge graph expert agent
	BuiltinKnowledgeGraphExpertID = "builtin-knowledge-graph-expert"
	// BuiltinDocumentAssistantID is the ID for the built-in document assistant agent
	BuiltinDocumentAssistantID = "builtin-document-assistant"
)

// AgentMode constants for agent running mode
const (
	// AgentModeQuickAnswer is the RAG mode for quick Q&A
	AgentModeQuickAnswer = "quick-answer"
	// AgentModeSmartReasoning is the ReAct mode for multi-step reasoning
	AgentModeSmartReasoning = "smart-reasoning"
)

// CustomAgent represents a configurable AI agent (similar to GPTs)
type CustomAgent struct {
	// Unique identifier of the agent (composite primary key with TenantID)
	// For built-in agents, this is 'builtin-quick-answer' or 'builtin-smart-reasoning'
	// For custom agents, this is a UUID
	ID string `yaml:"id" json:"id" gorm:"type:varchar(36);primaryKey"`
	// Name of the agent
	Name string `yaml:"name" json:"name" gorm:"type:varchar(255);not null"`
	// Description of the agent
	Description string `yaml:"description" json:"description" gorm:"type:text"`
	// Avatar/Icon of the agent (emoji or icon name)
	Avatar string `yaml:"avatar" json:"avatar" gorm:"type:varchar(64)"`
	// Whether this is a built-in agent (normal mode / agent mode)
	IsBuiltin bool `yaml:"is_builtin" json:"is_builtin" gorm:"default:false"`
	// Tenant ID (composite primary key with ID)
	TenantID uint64 `yaml:"tenant_id" json:"tenant_id" gorm:"primaryKey"`
	// Created by user ID
	CreatedBy string `yaml:"created_by" json:"created_by" gorm:"type:varchar(36)"`

	// Agent configuration
	Config CustomAgentConfig `yaml:"config" json:"config" gorm:"type:json"`

	// Timestamps
	CreatedAt time.Time      `yaml:"created_at" json:"created_at"`
	UpdatedAt time.Time      `yaml:"updated_at" json:"updated_at"`
	DeletedAt gorm.DeletedAt `yaml:"deleted_at" json:"deleted_at" gorm:"index"`
}

// CustomAgentConfig represents the configuration of a custom agent
type CustomAgentConfig struct {
	// ===== Basic Settings =====
	// Agent mode: "quick-answer" for RAG mode, "smart-reasoning" for ReAct agent mode
	AgentMode string `yaml:"agent_mode" json:"agent_mode"`
	// System prompt for the agent (unified prompt, uses {{web_search_status}} placeholder for dynamic behavior)
	SystemPrompt string `yaml:"system_prompt" json:"system_prompt"`
	// Context template for normal mode (how to format retrieved chunks)
	ContextTemplate string `yaml:"context_template" json:"context_template"`

	// ===== Model Settings =====
	// Model ID to use for conversations
	ModelID string `yaml:"model_id" json:"model_id"`
	// ReRank model ID for retrieval
	RerankModelID string `yaml:"rerank_model_id" json:"rerank_model_id"`
	// Temperature for LLM (0-1)
	Temperature float64 `yaml:"temperature" json:"temperature"`
	// Maximum completion tokens (only for normal mode)
	MaxCompletionTokens int `yaml:"max_completion_tokens" json:"max_completion_tokens"`
	// Whether to enable thinking mode (for models that support extended thinking)
	Thinking *bool `yaml:"thinking" json:"thinking"`

	// ===== Agent Mode Settings =====
	// Maximum iterations for ReAct loop (only for agent type)
	MaxIterations int `yaml:"max_iterations" json:"max_iterations"`
	// Allowed tools (only for agent type)
	AllowedTools []string `yaml:"allowed_tools" json:"allowed_tools"`
	// Whether reflection is enabled (only for agent type)
	ReflectionEnabled bool `yaml:"reflection_enabled" json:"reflection_enabled"`
	// MCP service selection mode: "all" = all enabled MCP services, "selected" = specific services, "none" = no MCP
	MCPSelectionMode string `yaml:"mcp_selection_mode" json:"mcp_selection_mode"`
	// Selected MCP service IDs (only used when MCPSelectionMode is "selected")
	MCPServices []string `yaml:"mcp_services" json:"mcp_services"`

	// ===== Knowledge Base Settings =====
	// Knowledge base selection mode: "all" = all KBs, "selected" = specific KBs, "none" = no KB
	KBSelectionMode string `yaml:"kb_selection_mode" json:"kb_selection_mode"`
	// Associated knowledge base IDs (only used when KBSelectionMode is "selected")
	KnowledgeBases []string `yaml:"knowledge_bases" json:"knowledge_bases"`
	// Whether to retrieve knowledge base only when explicitly mentioned with @ (default: false)
	// When true, knowledge base retrieval only happens if user explicitly mentions KB/files with @
	// When false, knowledge base retrieval happens according to KBSelectionMode
	RetrieveKBOnlyWhenMentioned bool `yaml:"retrieve_kb_only_when_mentioned" json:"retrieve_kb_only_when_mentioned"`

	// ===== File Type Restriction Settings =====
	// Supported file types for this agent (e.g., ["csv", "xlsx", "xls"])
	// Empty means all file types are supported
	// When set, only files with matching extensions can be used with this agent
	SupportedFileTypes []string `yaml:"supported_file_types" json:"supported_file_types"`

	// ===== FAQ Strategy Settings =====
	// Whether FAQ priority strategy is enabled (FAQ answers prioritized over document chunks)
	FAQPriorityEnabled bool `yaml:"faq_priority_enabled" json:"faq_priority_enabled"`
	// FAQ direct answer threshold - if similarity > this value, use FAQ answer directly
	FAQDirectAnswerThreshold float64 `yaml:"faq_direct_answer_threshold" json:"faq_direct_answer_threshold"`
	// FAQ score boost multiplier - FAQ results score multiplied by this factor
	FAQScoreBoost float64 `yaml:"faq_score_boost" json:"faq_score_boost"`

	// ===== Web Search Settings =====
	// Whether web search is enabled
	WebSearchEnabled bool `yaml:"web_search_enabled" json:"web_search_enabled"`
	// Maximum web search results
	WebSearchMaxResults int `yaml:"web_search_max_results" json:"web_search_max_results"`

	// ===== Multi-turn Conversation Settings =====
	// Whether multi-turn conversation is enabled
	MultiTurnEnabled bool `yaml:"multi_turn_enabled" json:"multi_turn_enabled"`
	// Number of history turns to keep in context
	HistoryTurns int `yaml:"history_turns" json:"history_turns"`

	// ===== Retrieval Strategy Settings (for both modes) =====
	// Embedding/Vector retrieval top K
	EmbeddingTopK int `yaml:"embedding_top_k" json:"embedding_top_k"`
	// Keyword retrieval threshold
	KeywordThreshold float64 `yaml:"keyword_threshold" json:"keyword_threshold"`
	// Vector retrieval threshold
	VectorThreshold float64 `yaml:"vector_threshold" json:"vector_threshold"`
	// Rerank top K
	RerankTopK int `yaml:"rerank_top_k" json:"rerank_top_k"`
	// Rerank threshold
	RerankThreshold float64 `yaml:"rerank_threshold" json:"rerank_threshold"`

	// ===== Advanced Settings (mainly for normal mode) =====
	// Whether to enable query expansion
	EnableQueryExpansion bool `yaml:"enable_query_expansion" json:"enable_query_expansion"`
	// Whether to enable query rewrite for multi-turn conversations
	EnableRewrite bool `yaml:"enable_rewrite" json:"enable_rewrite"`
	// Rewrite prompt system message
	RewritePromptSystem string `yaml:"rewrite_prompt_system" json:"rewrite_prompt_system"`
	// Rewrite prompt user message template
	RewritePromptUser string `yaml:"rewrite_prompt_user" json:"rewrite_prompt_user"`
	// Fallback strategy: "fixed" for fixed response, "model" for model generation
	FallbackStrategy string `yaml:"fallback_strategy" json:"fallback_strategy"`
	// Fixed fallback response (when FallbackStrategy is "fixed")
	FallbackResponse string `yaml:"fallback_response" json:"fallback_response"`
	// Fallback prompt (when FallbackStrategy is "model")
	FallbackPrompt string `yaml:"fallback_prompt" json:"fallback_prompt"`
}

// Value implements driver.Valuer interface for CustomAgentConfig
func (c CustomAgentConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements sql.Scanner interface for CustomAgentConfig
func (c *CustomAgentConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// TableName returns the table name for CustomAgent
func (CustomAgent) TableName() string {
	return "custom_agents"
}

// EnsureDefaults sets default values for the agent
func (a *CustomAgent) EnsureDefaults() {
	if a == nil {
		return
	}
	if a.Config.Temperature == 0 {
		a.Config.Temperature = 0.7
	}
	if a.Config.MaxIterations == 0 {
		a.Config.MaxIterations = 10
	}
	if a.Config.WebSearchMaxResults == 0 {
		a.Config.WebSearchMaxResults = 5
	}
	if a.Config.HistoryTurns == 0 {
		a.Config.HistoryTurns = 5
	}
	// Retrieval strategy defaults
	if a.Config.EmbeddingTopK == 0 {
		a.Config.EmbeddingTopK = 10
	}
	if a.Config.KeywordThreshold == 0 {
		a.Config.KeywordThreshold = 0.3
	}
	if a.Config.VectorThreshold == 0 {
		a.Config.VectorThreshold = 0.5
	}
	if a.Config.RerankTopK == 0 {
		a.Config.RerankTopK = 5
	}
	if a.Config.RerankThreshold == 0 {
		a.Config.RerankThreshold = 0.5
	}
	// Advanced settings defaults
	if a.Config.FallbackStrategy == "" {
		a.Config.FallbackStrategy = "model"
	}
	if a.Config.MaxCompletionTokens == 0 {
		a.Config.MaxCompletionTokens = 2048
	}
	// Agent mode should always enable multi-turn conversation
	if a.Config.AgentMode == AgentModeSmartReasoning {
		a.Config.MultiTurnEnabled = true
	}
}

// IsAgentMode returns true if this agent uses ReAct agent mode
func (a *CustomAgent) IsAgentMode() bool {
	return a.Config.AgentMode == AgentModeSmartReasoning
}

// GetBuiltinQuickAnswerAgent returns the built-in quick answer (RAG) mode agent
func GetBuiltinQuickAnswerAgent(tenantID uint64) *CustomAgent {
	return &CustomAgent{
		ID:          BuiltinQuickAnswerID,
		Name:        "快速问答",
		Description: "基于知识库的 RAG 问答，快速准确地回答问题",
		IsBuiltin:   true,
		TenantID:    tenantID,
		Config: CustomAgentConfig{
			AgentMode:    AgentModeQuickAnswer,
			SystemPrompt: "",
			ContextTemplate: `请根据以下参考资料回答用户问题。

参考资料：
{{contexts}}

用户问题：{{query}}`,
			Temperature:                 0.7,
			MaxCompletionTokens:         2048,
			WebSearchEnabled:            true,
			WebSearchMaxResults:         5,
			MultiTurnEnabled:            true,
			HistoryTurns:                5,
			KBSelectionMode:             "all",
			RetrieveKBOnlyWhenMentioned: false, // Default: retrieve KB based on KBSelectionMode
			// FAQ strategy
			FAQPriorityEnabled:       true,
			FAQDirectAnswerThreshold: 0.9,
			FAQScoreBoost:            1.2,
			// Retrieval strategy
			EmbeddingTopK:    10,
			KeywordThreshold: 0.3,
			VectorThreshold:  0.5,
			RerankTopK:       10,
			RerankThreshold:  0.3,
			// Advanced settings
			EnableQueryExpansion: true,
			EnableRewrite:        true,
			FallbackStrategy:     "model",
		},
	}
}

// GetBuiltinSmartReasoningAgent returns the built-in smart reasoning (ReAct) mode agent
func GetBuiltinSmartReasoningAgent(tenantID uint64) *CustomAgent {
	return &CustomAgent{
		ID:          BuiltinSmartReasoningID,
		Name:        "智能推理",
		Description: "ReAct 推理框架，支持多步思考和工具调用",
		IsBuiltin:   true,
		TenantID:    tenantID,
		Config: CustomAgentConfig{
			AgentMode:                   AgentModeSmartReasoning,
			SystemPrompt:                "",
			Temperature:                 0.7,
			MaxCompletionTokens:         2048,
			MaxIterations:               50,
			KBSelectionMode:             "all",
			RetrieveKBOnlyWhenMentioned: false, // Default: retrieve KB based on KBSelectionMode
			AllowedTools:                []string{"thinking", "todo_write", "knowledge_search", "grep_chunks", "list_knowledge_chunks", "query_knowledge_graph", "get_document_info"},
			WebSearchEnabled:            true,
			WebSearchMaxResults:         5,
			ReflectionEnabled:           false,
			MultiTurnEnabled:            true,
			HistoryTurns:                5,
			// FAQ strategy
			FAQPriorityEnabled:       true,
			FAQDirectAnswerThreshold: 0.9,
			FAQScoreBoost:            1.2,
			// Retrieval strategy
			EmbeddingTopK:    10,
			KeywordThreshold: 0.3,
			VectorThreshold:  0.5,
			RerankTopK:       10,
			RerankThreshold:  0.3,
		},
	}
}

// GetBuiltinDataAnalystAgent returns the built-in data analyst agent
// This agent specializes in analyzing CSV/Excel data using SQL queries via DuckDB
func GetBuiltinDataAnalystAgent(tenantID uint64) *CustomAgent {
	return &CustomAgent{
		ID:          BuiltinDataAnalystID,
		Name:        "数据分析师",
		Description: "专业数据分析智能体，支持 CSV/Excel 文件的 SQL 查询与统计分析",
		Avatar:      "📊",
		IsBuiltin:   true,
		TenantID:    tenantID,
		Config: CustomAgentConfig{
			AgentMode:    AgentModeSmartReasoning,
			SystemPrompt: `### Role
You are WeKnora Data Analyst, an intelligent data analysis assistant powered by DuckDB. You specialize in analyzing structured data from CSV and Excel files using SQL queries.

### Mission
Help users explore, analyze, and derive insights from their tabular data through intelligent SQL query generation and execution.

### Critical Constraints
1. **Schema First:** ALWAYS call data_schema before writing any SQL query to understand the table structure.
2. **Read-Only:** Only SELECT queries allowed. INSERT, UPDATE, DELETE, CREATE, DROP are forbidden.
3. **Iterative Refinement:** If a query fails, analyze the error and refine your approach.

### Workflow
1. **Understand:** Call data_schema to get table name, columns, types, and row count.
2. **Plan:** For complex questions, use todo_write to break into sub-queries.
3. **Query:** Call data_analysis with the knowledge_id and SQL query.
4. **Analyze:** Interpret results and provide insights.

### SQL Best Practices for DuckDB
- Use double quotes for identifiers: SELECT "Column Name" FROM "table_name"
- Aggregate functions: COUNT(*), SUM(), AVG(), MIN(), MAX(), MEDIAN(), STDDEV()
- String matching: LIKE, ILIKE (case-insensitive), REGEXP
- Use LIMIT to prevent overwhelming output (default to 100 rows max)

### Tool Guidelines
- **data_schema:** ALWAYS use first. Required before any query.
- **data_analysis:** Execute SQL queries. Only SELECT queries allowed.
- **thinking:** Plan complex analyses, debug query issues.
- **todo_write:** Track multi-step analysis tasks.

### Output Standards
- Present results in well-formatted tables or summaries
- Provide actionable insights, not just raw numbers
- Relate findings back to the user's original question

Current Time: {{current_time}}
`,
			Temperature:                 0.3, // Lower temperature for precise SQL generation
			MaxCompletionTokens:         4096,
			MaxIterations:               30,
			KBSelectionMode:             "all",
			RetrieveKBOnlyWhenMentioned: false, // Default: retrieve KB based on KBSelectionMode
			// Only support CSV and Excel files for data analysis
			// Use standard values (xlsx), backend will auto-include xls via alias
			SupportedFileTypes: []string{"csv", "xlsx"},
			// Core tools for data analysis
			AllowedTools: []string{
				"thinking",
				"todo_write",
				"data_schema",   // Get table schema information
				"data_analysis", // Execute SQL queries on data
			},
			WebSearchEnabled:    false, // Data analysis doesn't need web search
			WebSearchMaxResults: 0,
			ReflectionEnabled:   true, // Enable reflection for query optimization
			MultiTurnEnabled:    true,
			HistoryTurns:        10, // More history for iterative analysis
			// Retrieval strategy (minimal, as we focus on data tools)
			EmbeddingTopK:    5,
			KeywordThreshold: 0.3,
			VectorThreshold:  0.5,
			RerankTopK:       5,
			RerankThreshold:  0.3,
		},
	}
}

// Deprecated: Use GetBuiltinQuickAnswerAgent instead
func GetBuiltinNormalAgent(tenantID uint64) *CustomAgent {
	return GetBuiltinQuickAnswerAgent(tenantID)
}

// Deprecated: Use GetBuiltinSmartReasoningAgent instead
func GetBuiltinAgentAgent(tenantID uint64) *CustomAgent {
	return GetBuiltinSmartReasoningAgent(tenantID)
}

// BuiltinAgentRegistry provides a registry of all built-in agents for easy extension
var BuiltinAgentRegistry = map[string]func(uint64) *CustomAgent{
	BuiltinQuickAnswerID:    GetBuiltinQuickAnswerAgent,
	BuiltinSmartReasoningID: GetBuiltinSmartReasoningAgent,
	BuiltinDataAnalystID:    GetBuiltinDataAnalystAgent,
}

// builtinAgentIDsOrdered defines the fixed display order of built-in agents
var builtinAgentIDsOrdered = []string{
	BuiltinQuickAnswerID,
	BuiltinSmartReasoningID,
	BuiltinDeepResearcherID,
	BuiltinDataAnalystID,
	BuiltinKnowledgeGraphExpertID,
	BuiltinDocumentAssistantID,
}

// GetBuiltinAgentIDs returns all built-in agent IDs in fixed order
func GetBuiltinAgentIDs() []string {
	return builtinAgentIDsOrdered
}

// IsBuiltinAgentID checks if the given ID is a built-in agent ID
func IsBuiltinAgentID(id string) bool {
	_, exists := BuiltinAgentRegistry[id]
	return exists
}

// GetBuiltinAgent returns a built-in agent by ID, or nil if not found
func GetBuiltinAgent(id string, tenantID uint64) *CustomAgent {
	if factory, exists := BuiltinAgentRegistry[id]; exists {
		return factory(tenantID)
	}
	return nil
}

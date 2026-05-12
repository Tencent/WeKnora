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
	// BuiltinWikiResearcherID is the ID for the built-in wiki researcher agent
	BuiltinWikiResearcherID = "builtin-wiki-researcher"
	// BuiltinWikiFixerID is the ID for the built-in wiki fixer agent
	BuiltinWikiFixerID = "builtin-wiki-fixer"
)

// AgentMode constants for agent running mode
const (
	// AgentModeQuickAnswer is the RAG mode for quick Q&A
	AgentModeQuickAnswer = "quick-answer"
	// AgentModeSmartReasoning is the ReAct mode for multi-step reasoning
	AgentModeSmartReasoning = "smart-reasoning"
)

// AgentType constants for Smart-Reasoning agent presets.
// These presets bundle a recommended system prompt template,
// tool allowlist, KB compatibility hint, and other defaults so users
// don't have to configure everything from scratch.
// AgentTypeCustom means the user wants full control and we won't
// auto-fill anything based on the preset.
const (
	// AgentTypeRAGQA prefers vector/keyword chunk retrieval on document KBs.
	AgentTypeRAGQA = "rag-qa"
	// AgentTypeWikiQA prefers wiki-page navigation on wiki-enabled KBs.
	AgentTypeWikiQA = "wiki-qa"
	// AgentTypeHybridRAGWiki orchestrates Wiki + RAG on KBs where both are enabled.
	AgentTypeHybridRAGWiki = "hybrid-rag-wiki"
	// AgentTypeDataAnalysis runs SQL / statistics over tabular files (CSV, Excel)
	// uploaded into the KB. Retrieval semantics (vector/wiki/…) are largely
	// irrelevant — this type is about data_schema + data_analysis tools.
	AgentTypeDataAnalysis = "data-analysis"
	// AgentTypeCustom is the "no preset" option; user-configured end to end.
	AgentTypeCustom = "custom"
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
	// AgentType is a preset category under smart-reasoning mode that pre-fills
	// system prompt, allowed tools and recommended KB compatibility.
	// Valid values: "rag-qa", "wiki-qa", "hybrid-rag-wiki", "custom".
	// Empty / unknown values are treated as "custom" (no preset applied).
	// Ignored for quick-answer mode.
	AgentType string `yaml:"agent_type" json:"agent_type,omitempty"`
	// System prompt for the agent (unified prompt, uses web_search_status placeholder for dynamic behavior)
	SystemPrompt string `yaml:"system_prompt" json:"system_prompt"`
	// SystemPromptID references a template ID in prompt_templates/ YAML files.
	// If set and SystemPrompt is empty, the template content will be resolved at startup.
	SystemPromptID string `yaml:"system_prompt_id" json:"system_prompt_id,omitempty"`
	// Context template for normal mode (how to format retrieved chunks)
	ContextTemplate string `yaml:"context_template" json:"context_template"`
	// ContextTemplateID references a template ID in prompt_templates/ YAML files.
	// If set and ContextTemplate is empty, the template content will be resolved at startup.
	ContextTemplateID string `yaml:"context_template_id" json:"context_template_id,omitempty"`

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
	// Timeout for a single LLM call in seconds (0 = use global default)
	LLMCallTimeout int `yaml:"llm_call_timeout" json:"llm_call_timeout,omitempty"`
	// Allowed tools (only for agent type)
	AllowedTools []string `yaml:"allowed_tools" json:"allowed_tools"`
	// MCP service selection mode: "all" = all enabled MCP services, "selected" = specific services, "none" = no MCP
	MCPSelectionMode string `yaml:"mcp_selection_mode" json:"mcp_selection_mode"`
	// Selected MCP service IDs (only used when MCPSelectionMode is "selected")
	MCPServices []string `yaml:"mcp_services" json:"mcp_services"`

	// ===== Skills Settings (only for smart-reasoning mode) =====
	// Skills selection mode: "all" = all preloaded skills, "selected" = specific skills, "none" = no skills
	SkillsSelectionMode string `yaml:"skills_selection_mode" json:"skills_selection_mode"`
	// Selected skill names (only used when SkillsSelectionMode is "selected")
	SelectedSkills []string `yaml:"selected_skills" json:"selected_skills"`
	// ===== Knowledge Base Settings =====
	// Knowledge base selection mode: "all" = all KBs, "selected" = specific KBs, "none" = no KB
	KBSelectionMode string `yaml:"kb_selection_mode" json:"kb_selection_mode"`
	// Associated knowledge base IDs (only used when KBSelectionMode is "selected")
	KnowledgeBases []string `yaml:"knowledge_bases" json:"knowledge_bases"`
	// Whether to retrieve knowledge base only when explicitly mentioned with @ (default: false)
	// When true, knowledge base retrieval only happens if user explicitly mentions KB/files with @
	// When false, knowledge base retrieval happens according to KBSelectionMode
	RetrieveKBOnlyWhenMentioned bool `yaml:"retrieve_kb_only_when_mentioned" json:"retrieve_kb_only_when_mentioned"`

	// Whether to retain retrieval history across turns
	RetainRetrievalHistory bool `yaml:"retain_retrieval_history" json:"retain_retrieval_history"`

	// ===== Image Upload / Multimodal Settings =====
	// Whether image upload is enabled for this agent (default: false)
	ImageUploadEnabled bool `yaml:"image_upload_enabled" json:"image_upload_enabled"`
	// VLM model ID for image analysis (optional, falls back to tenant-level VLM)
	VLMModelID string `yaml:"vlm_model_id" json:"vlm_model_id"`
	// Whether audio upload (ASR transcription) is enabled for this agent (default: false)
	AudioUploadEnabled bool `yaml:"audio_upload_enabled" json:"audio_upload_enabled"`
	// ASR model ID for audio transcription (optional)
	ASRModelID string `yaml:"asr_model_id" json:"asr_model_id"`
	// Storage provider for image uploads: "local", "minio", "cos", "tos", "s3", "oss", "ks3".
	// Empty means use the global/tenant default provider.
	ImageStorageProvider string `yaml:"image_storage_provider" json:"image_storage_provider"`

	// ===== File Type Restriction Settings =====
	// Supported file types for this agent (e.g., ["csv", "xlsx", "xls"])
	// Empty means all file types are supported
	// When set, only files with matching extensions can be used with this agent
	SupportedFileTypes []string `yaml:"supported_file_types" json:"supported_file_types"`

	// ===== Data Analysis Settings =====
	// Whether to run the legacy in-pipeline DuckDB SQL data-analysis stage when
	// the retrieved chunks include CSV/Excel files. This issues an extra LLM
	// call to generate a SQL query and is disabled by default because most
	// quick-answer / RAG-style agents do not want the added latency.
	DataAnalysisEnabled bool `yaml:"data_analysis_enabled" json:"data_analysis_enabled"`

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
	// WebSearchProviderID references a specific WebSearchProviderEntity.
	// If empty, the tenant's default provider (is_default=true) is used.
	WebSearchProviderID string `yaml:"web_search_provider_id" json:"web_search_provider_id,omitempty"`
	// Whether to auto-fetch full page content for reranked web search results
	WebFetchEnabled bool `yaml:"web_fetch_enabled" json:"web_fetch_enabled"`
	// Max number of pages to fetch after rerank (default: 3)
	WebFetchTopN int `yaml:"web_fetch_top_n" json:"web_fetch_top_n,omitempty"`

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
	// Dedicated chat model ID for the query-understanding (rewrite + intent) step.
	// When empty, the main conversation ModelID is used as a fallback.
	QueryUnderstandModelID string `yaml:"query_understand_model_id" json:"query_understand_model_id,omitempty"`
	// Fallback strategy: "fixed" for fixed response, "model" for model generation
	FallbackStrategy string `yaml:"fallback_strategy" json:"fallback_strategy"`
	// Fixed fallback response (when FallbackStrategy is "fixed")
	FallbackResponse string `yaml:"fallback_response" json:"fallback_response"`
	// Fallback prompt (when FallbackStrategy is "model")
	FallbackPrompt string `yaml:"fallback_prompt" json:"fallback_prompt"`

	// ===== Suggested Prompts =====
	// 推荐问题列表，用于在前端对话面板展示快捷提问
	SuggestedPrompts []string `yaml:"suggested_prompts" json:"suggested_prompts,omitempty"`
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
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
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
	if a.Config.Temperature < 0 {
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

// SuggestedQuestion 推荐问题
type SuggestedQuestion struct {
	// 问题文本
	Question string `json:"question"`
	// 来源类型: "agent_config", "faq", "document", "wiki"
	Source string `json:"source"`
	// 来源知识库ID（仅 faq/document/wiki 来源时有值）
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
}

// BuiltinAgentRegistry provides a registry of all built-in agents.
// It is initialised empty and populated by LoadBuiltinAgentsConfig from
// config/builtin_agents.yaml at startup via rebuildRegistryFromConfig.
var BuiltinAgentRegistry = map[string]func(uint64) *CustomAgent{}

// builtinAgentIDsOrdered defines the fixed display order of built-in agents
// that are exposed in the user-facing agent list (ListAgents).
//
// NOTE: BuiltinWikiFixerID is intentionally excluded here. The wiki fixer is
// an internal agent invoked programmatically from the Wiki editor
// (see frontend WikiBrowser.vue) and should not clutter the tenant's agent
// picker. It remains fully usable via GetAgentByID because the YAML entry
// still registers it in BuiltinAgentRegistry.
var builtinAgentIDsOrdered = []string{
	BuiltinQuickAnswerID,
	BuiltinSmartReasoningID,
	BuiltinWikiResearcherID,
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

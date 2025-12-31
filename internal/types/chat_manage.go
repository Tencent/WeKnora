package types

// ChatManage represents the configuration and state for a chat session
// including query processing, search parameters, and model configurations
type ChatManage struct {
	SessionID    string     `json:"session_id"`              // Unique identifier for the chat session
	Query        string     `json:"query,omitempty"`         // Original user query
	RewriteQuery string     `json:"rewrite_query,omitempty"` // Query after rewriting for better retrieval
	History      []*History `json:"history,omitempty"`       // Chat history for context

	KnowledgeBaseIDs []string `json:"knowledge_base_ids"`      // IDs of knowledge bases to search (multi-KB support)
	KnowledgeIDs     []string `json:"knowledge_ids,omitempty"` // IDs of specific files to search (optional)
	// SearchTargets is the pre-computed unified search targets
	// Computed once at request entry point, used throughout the pipeline
	SearchTargets    SearchTargets `json:"-"`
	VectorThreshold  float64       `json:"vector_threshold"`  // Minimum score threshold for vector search results
	KeywordThreshold float64       `json:"keyword_threshold"` // Minimum score threshold for keyword search results
	EmbeddingTopK    int           `json:"embedding_top_k"`   // Number of top results to retrieve from embedding search
	VectorDatabase   string        `json:"vector_database"`   // Vector database type/name to use

	RerankModelID   string  `json:"rerank_model_id"`  // Model ID for reranking search results
	RerankTopK      int     `json:"rerank_top_k"`     // Number of top results after reranking
	RerankThreshold float64 `json:"rerank_threshold"` // Minimum score threshold for reranked results

	MaxRounds int `json:"max_rounds"` // Maximum history rounds used for rewrite/context

	ChatModelID      string           `json:"chat_model_id"`     // ID of the chat model to use
	SummaryConfig    SummaryConfig    `json:"summary_config"`    // Configuration for summary generation
	FallbackStrategy FallbackStrategy `json:"fallback_strategy"` // Strategy when no relevant results are found
	FallbackResponse string           `json:"fallback_response"` // Default response when fallback occurs
	FallbackPrompt   string           `json:"fallback_prompt"`   // Prompt for model-based fallback response

	EnableRewrite        bool   `json:"enable_rewrite"`         // Whether to enable rewrite
	EnableQueryExpansion bool   `json:"enable_query_expansion"` // Whether to enable query expansion with LLM
	RewritePromptSystem  string `json:"rewrite_prompt_system"`  // Custom system prompt for rewrite stage
	RewritePromptUser    string `json:"rewrite_prompt_user"`    // Custom user prompt for rewrite stage

	// Internal fields for pipeline data processing
	SearchResult    []*SearchResult   `json:"-"` // Results from search phase
	RerankResult    []*SearchResult   `json:"-"` // Results after reranking
	MergeResult     []*SearchResult   `json:"-"` // Final merged results after all processing
	Entity          []string          `json:"-"` // List of identified entities
	EntityKBIDs     []string          `json:"-"` // Knowledge base IDs with ExtractConfig enabled
	EntityKnowledge map[string]string `json:"-"` // KnowledgeID -> KnowledgeBaseID mapping for graph-enabled files
	GraphResult     *GraphData        `json:"-"` // Graph data from search phase
	UserContent     string            `json:"-"` // Processed user content
	ChatResponse    *ChatResponse     `json:"-"` // Final response from chat model

	// Event system for streaming responses
	EventBus  EventBusInterface `json:"-"` // EventBus for emitting streaming events
	MessageID string            `json:"-"` // Assistant message ID for event emission

	// Web search configuration (internal use)
	TenantID         uint64 `json:"-"` // Tenant ID for retrieving web search config
	WebSearchEnabled bool   `json:"-"` // Whether web search is enabled for this request

	// FAQ Strategy Settings
	FAQPriorityEnabled       bool    `json:"-"` // Whether FAQ priority strategy is enabled
	FAQDirectAnswerThreshold float64 `json:"-"` // Threshold for direct FAQ answer (similarity > this value)
	FAQScoreBoost            float64 `json:"-"` // Score multiplier for FAQ results
}

// Clone creates a deep copy of the ChatManage object
func (c *ChatManage) Clone() *ChatManage {
	// Deep copy knowledge base IDs slice
	knowledgeBaseIDs := make([]string, len(c.KnowledgeBaseIDs))
	copy(knowledgeBaseIDs, c.KnowledgeBaseIDs)

	// Deep copy knowledge IDs slice
	knowledgeIDs := make([]string, len(c.KnowledgeIDs))
	copy(knowledgeIDs, c.KnowledgeIDs)

	// Deep copy search targets slice
	searchTargets := make(SearchTargets, len(c.SearchTargets))
	for i, t := range c.SearchTargets {
		if t != nil {
			kidsCopy := make([]string, len(t.KnowledgeIDs))
			copy(kidsCopy, t.KnowledgeIDs)
			searchTargets[i] = &SearchTarget{
				Type:            t.Type,
				KnowledgeBaseID: t.KnowledgeBaseID,
				KnowledgeIDs:    kidsCopy,
			}
		}
	}

	return &ChatManage{
		Query:            c.Query,
		RewriteQuery:     c.RewriteQuery,
		SessionID:        c.SessionID,
		KnowledgeBaseIDs: knowledgeBaseIDs,
		KnowledgeIDs:     knowledgeIDs,
		SearchTargets:    searchTargets,
		VectorThreshold:  c.VectorThreshold,
		KeywordThreshold: c.KeywordThreshold,
		EmbeddingTopK:    c.EmbeddingTopK,
		MaxRounds:        c.MaxRounds,
		VectorDatabase:   c.VectorDatabase,
		RerankModelID:    c.RerankModelID,
		RerankTopK:       c.RerankTopK,
		RerankThreshold:  c.RerankThreshold,
		ChatModelID:      c.ChatModelID,
		SummaryConfig: SummaryConfig{
			MaxTokens:           c.SummaryConfig.MaxTokens,
			RepeatPenalty:       c.SummaryConfig.RepeatPenalty,
			TopK:                c.SummaryConfig.TopK,
			TopP:                c.SummaryConfig.TopP,
			FrequencyPenalty:    c.SummaryConfig.FrequencyPenalty,
			PresencePenalty:     c.SummaryConfig.PresencePenalty,
			Prompt:              c.SummaryConfig.Prompt,
			ContextTemplate:     c.SummaryConfig.ContextTemplate,
			NoMatchPrefix:       c.SummaryConfig.NoMatchPrefix,
			Temperature:         c.SummaryConfig.Temperature,
			Seed:                c.SummaryConfig.Seed,
			MaxCompletionTokens: c.SummaryConfig.MaxCompletionTokens,
			Thinking:            c.SummaryConfig.Thinking,
		},
		FallbackStrategy:     c.FallbackStrategy,
		FallbackResponse:     c.FallbackResponse,
		FallbackPrompt:       c.FallbackPrompt,
		RewritePromptSystem:  c.RewritePromptSystem,
		RewritePromptUser:    c.RewritePromptUser,
		EnableRewrite:        c.EnableRewrite,
		EnableQueryExpansion: c.EnableQueryExpansion,
		TenantID:             c.TenantID,
		// FAQ Strategy Settings
		FAQPriorityEnabled:       c.FAQPriorityEnabled,
		FAQDirectAnswerThreshold: c.FAQDirectAnswerThreshold,
		FAQScoreBoost:            c.FAQScoreBoost,
	}
}

// EventType represents different stages in the RAG (Retrieval Augmented Generation) pipeline
type EventType string

const (
	LOAD_HISTORY           EventType = "load_history"           // Load conversation history without rewriting
	REWRITE_QUERY          EventType = "rewrite_query"          // Query rewriting for better retrieval
	CHUNK_SEARCH           EventType = "chunk_search"           // Search for relevant chunks
	CHUNK_SEARCH_PARALLEL  EventType = "chunk_search_parallel"  // Parallel search: chunks + entities
	ENTITY_SEARCH          EventType = "entity_search"          // Search for relevant entities
	CHUNK_RERANK           EventType = "chunk_rerank"           // Rerank search results
	CHUNK_MERGE            EventType = "chunk_merge"            // Merge similar chunks
	DATA_ANALYSIS          EventType = "data_analysis"          // Data analysis for CSV/Excel files
	INTO_CHAT_MESSAGE      EventType = "into_chat_message"      // Convert chunks into chat messages
	CHAT_COMPLETION        EventType = "chat_completion"        // Generate chat completion
	CHAT_COMPLETION_STREAM EventType = "chat_completion_stream" // Stream chat completion
	STREAM_FILTER          EventType = "stream_filter"          // Filter streaming output
	FILTER_TOP_K           EventType = "filter_top_k"           // Keep only top K results
)

// Pipline defines the sequence of events for different chat modes
var Pipline = map[string][]EventType{
	"chat": { // Simple chat without retrieval
		CHAT_COMPLETION,
	},
	"chat_stream": { // Streaming chat without retrieval (no history)
		CHAT_COMPLETION_STREAM,
		STREAM_FILTER,
	},
	"chat_history_stream": { // Streaming chat with conversation history
		LOAD_HISTORY,
		CHAT_COMPLETION_STREAM,
		STREAM_FILTER,
	},
	"rag": { // Retrieval Augmented Generation
		CHUNK_SEARCH,
		CHUNK_RERANK,
		CHUNK_MERGE,
		INTO_CHAT_MESSAGE,
		CHAT_COMPLETION,
	},
	"rag_stream": { // Streaming Retrieval Augmented Generation
		REWRITE_QUERY,
		CHUNK_SEARCH_PARALLEL, // Parallel: CHUNK_SEARCH + ENTITY_SEARCH
		CHUNK_RERANK,
		CHUNK_MERGE,
		FILTER_TOP_K,
		DATA_ANALYSIS,
		INTO_CHAT_MESSAGE,
		CHAT_COMPLETION_STREAM,
		STREAM_FILTER,
	},
}

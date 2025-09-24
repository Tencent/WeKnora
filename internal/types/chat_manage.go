package types

// ChatManage represents the configuration and state for a chat session
// including query processing, search parameters, and model configurations
type ChatManage struct {
	SessionID      string     `json:"session_id"`                // Unique identifier for the chat session
	Query          string     `json:"query,omitempty"`           // Original user query
	ProcessedQuery string     `json:"processed_query,omitempty"` // Query after preprocessing
	RewriteQuery   string     `json:"rewrite_query,omitempty"`   // Query after rewriting for better retrieval
	History        []*History `json:"history,omitempty"`         // Chat history for context

	KnowledgeBaseID  string  `json:"knowledge_base_id"` // ID of the knowledge base to search against
	VectorThreshold  float64 `json:"vector_threshold"`  // Minimum score threshold for vector search results
	KeywordThreshold float64 `json:"keyword_threshold"` // Minimum score threshold for keyword search results
	EmbeddingTopK    int     `json:"embedding_top_k"`   // Number of top results to retrieve from embedding search
	VectorDatabase   string  `json:"vector_database"`   // Vector database type/name to use

	RerankModelID   string  `json:"rerank_model_id"`  // Model ID for reranking search results
	RerankTopK      int     `json:"rerank_top_k"`     // Number of top results after reranking
	RerankThreshold float64 `json:"rerank_threshold"` // Minimum score threshold for reranked results

	ChatModelID      string           `json:"chat_model_id"`     // Model ID for chat completion
	SummaryConfig    SummaryConfig    `json:"summary_config"`    // Configuration for summary generation
	FallbackStrategy FallbackStrategy `json:"fallback_strategy"` // Strategy when no relevant results are found
	FallbackResponse string           `json:"fallback_response"` // Default response when fallback occurs

	// Internal fields for pipeline data processing
	SearchResult []*SearchResult       `json:"-"` // Results from search phase
	RerankResult []*SearchResult       `json:"-"` // Results after reranking
	MergeResult  []*SearchResult       `json:"-"` // Final merged results after all processing
	Entity       []string              `json:"-"` // List of identified entities
	GraphResult  *GraphData            `json:"-"` // Graph data from search phase
	UserContent  string                `json:"-"` // Processed user content
	ChatResponse *ChatResponse         `json:"-"` // Final response from chat model
	ResponseChan <-chan StreamResponse `json:"-"` // Channel for streaming responses
}

// Clone creates a deep copy of the ChatManage object
func (c *ChatManage) Clone() *ChatManage {
	return &ChatManage{
		Query:            c.Query,
		ProcessedQuery:   c.ProcessedQuery,
		RewriteQuery:     c.RewriteQuery,
		SessionID:        c.SessionID,
		KnowledgeBaseID:  c.KnowledgeBaseID,
		VectorThreshold:  c.VectorThreshold,
		KeywordThreshold: c.KeywordThreshold,
		EmbeddingTopK:    c.EmbeddingTopK,
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
		},
		FallbackStrategy: c.FallbackStrategy,
		FallbackResponse: c.FallbackResponse,
	}
}

// EventType represents different stages in the RAG (Retrieval Augmented Generation) pipeline
type EventType string

const (
	PREPROCESS_QUERY       EventType = "preprocess_query"       // Query preprocessing stage
	REWRITE_QUERY          EventType = "rewrite_query"          // Query rewriting for better retrieval
	CHUNK_SEARCH           EventType = "chunk_search"           // Search for relevant chunks
	ENTITY_SEARCH          EventType = "entity_search"          // Search for relevant entities
	CHUNK_RERANK           EventType = "chunk_rerank"           // Rerank search results
	CHUNK_MERGE            EventType = "chunk_merge"            // Merge similar chunks
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
	"chat_stream": { // Streaming chat without retrieval
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
		PREPROCESS_QUERY,
		CHUNK_SEARCH,
		ENTITY_SEARCH,
		CHUNK_RERANK,
		CHUNK_MERGE,
		FILTER_TOP_K,
		INTO_CHAT_MESSAGE,
		CHAT_COMPLETION_STREAM,
		STREAM_FILTER,
	},
}

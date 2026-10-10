package types

import (
	"maps"
	"slices"
	"strings"
)

// PipelineRequest holds immutable configuration set once at the request entry point.
type PipelineRequest struct {
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	Query     string `json:"query,omitempty"`
	MaxRounds int    `json:"max_rounds"`

	// Knowledge base retrieval parameters
	KnowledgeBaseIDs []string      `json:"knowledge_base_ids"`
	KnowledgeIDs     []string      `json:"knowledge_ids,omitempty"`
	SearchTargets    SearchTargets `json:"-"`
	VectorThreshold  float64       `json:"vector_threshold"`
	KeywordThreshold float64       `json:"keyword_threshold"`
	EmbeddingTopK    int           `json:"embedding_top_k"`
	VectorDatabase   string        `json:"vector_database"`
	// DisableVectorMatch / DisableKeywordsMatch turn off one recall path for
	// every search target. Set by the knowledge-search API.
	DisableVectorMatch   bool `json:"disable_vector_match,omitempty"`
	DisableKeywordsMatch bool `json:"disable_keywords_match,omitempty"`

	// Rerank parameters
	RerankModelID   string  `json:"rerank_model_id"`
	RerankTopK      int     `json:"rerank_top_k"`
	RerankThreshold float64 `json:"rerank_threshold"`

	// Chat model parameters
	ChatModelID      string           `json:"chat_model_id"`
	SummaryConfig    SummaryConfig    `json:"summary_config"`
	FallbackStrategy FallbackStrategy `json:"fallback_strategy"`
	FallbackResponse string           `json:"fallback_response"`
	FallbackPrompt   string           `json:"fallback_prompt"`
	// CitationEnabled controls only final knowledge/web source citations. Nil
	// defaults to true for requests and agents created before this option existed.
	CitationEnabled *bool `json:"citation_enabled,omitempty"`

	// Rewrite parameters
	EnableRewrite        bool   `json:"enable_rewrite"`
	EnableQueryExpansion bool   `json:"enable_query_expansion"`
	RewritePromptSystem  string `json:"rewrite_prompt_system"`
	RewritePromptUser    string `json:"rewrite_prompt_user"`
	// QueryUnderstandModelID, when set, overrides the chat model used for
	// the query-understanding (rewrite + intent classification) stage only.
	// Empty means fall back to ChatModelID.
	QueryUnderstandModelID string `json:"query_understand_model_id,omitempty"`

	// FAQ strategy
	FAQPriorityEnabled       bool    `json:"-"`
	FAQDirectAnswerThreshold float64 `json:"-"`
	FAQScoreBoost            float64 `json:"-"`

	// DataAnalysisEnabled controls whether the in-pipeline DuckDB SQL
	// data-analysis stage runs. Off by default to avoid an extra LLM call on
	// every RAG request that happens to retrieve CSV/Excel chunks.
	DataAnalysisEnabled bool `json:"-"`

	// Image / multimodal support
	Images                  []string `json:"-"`
	VLMModelID              string   `json:"-"`
	ChatModelSupportsVision bool     `json:"-"`

	// File attachments support
	Attachments MessageAttachments `json:"-"`

	// IntentPromptOverrides holds agent-level intent prompt overrides for the
	// query-understanding stage. Empty values fall back to tenant/global defaults.
	IntentPromptOverrides map[string]string `json:"-"`

	// Misc request-scoped config
	TenantID            uint64 `json:"-"`
	WebSearchEnabled    bool   `json:"-"`
	WebSearchProviderID string `json:"-"` // Resolved from agent config or tenant default
	WebSearchMaxResults int    `json:"-"` // Resolved from agent config or tenant default
	WebFetchEnabled     bool   `json:"-"` // Auto-fetch full page content for web search results after rerank
	WebFetchTopN        int    `json:"-"` // Max pages to fetch (default 3)
	Language            string `json:"-"`
}

// CitationsEnabled returns the effective citation setting for this request.
func (c *PipelineRequest) CitationsEnabled() bool {
	return c == nil || c.CitationEnabled == nil || *c.CitationEnabled
}

// QueryIntent represents the classified intent of a user query.
type QueryIntent string

const (
	IntentKBSearch      QueryIntent = "kb_search"
	IntentWebSearch     QueryIntent = "web_search"
	IntentGreeting      QueryIntent = "greeting"
	IntentChitchat      QueryIntent = "chitchat"
	IntentFollowUp      QueryIntent = "follow_up"
	IntentImageOnly     QueryIntent = "image_only"
	IntentDocOnly       QueryIntent = "doc_only"
	IntentSummarize     QueryIntent = "summarize"
	IntentClarification QueryIntent = "clarification"
)

// NeedsKBRetrieval returns true when the intent requires knowledge base search.
// The zero value (empty string) is treated as needing retrieval for safety.
// Note: IntentWebSearch is NOT included — use ChatManage.NeedsRetrieval()
// which also considers the WebSearchEnabled flag.
func (i QueryIntent) NeedsKBRetrieval() bool {
	switch i {
	case IntentKBSearch, IntentClarification, "":
		return true
	default:
		return false
	}
}

// Retrieval stage names recorded on RetrievalTruncation.Stage, in pipeline
// order.
const (
	// RetrievalStageRerank is the CHUNK_RERANK stage, which drops candidates
	// through threshold rejection, its candidate cap or its MMR top-k.
	RetrievalStageRerank = "rerank"
	// RetrievalStageMerge is the CHUNK_MERGE fallback cut to RerankTopK, taken
	// when rerank produced nothing to merge.
	RetrievalStageMerge = "merge"
	// RetrievalStageFilterTopK is the FILTER_TOP_K stage.
	RetrievalStageFilterTopK = "filter_top_k"
)

// RetrievalTruncation records that the passages in the model context are a
// subset of this turn's ranked candidates.
//
// Stage is the first pipeline stage that dropped candidates and Candidates is
// the number of candidate passages that stage received, so the pair always
// describes one population: the retrieval set that entered the ranked filter
// chain (rerank → merge → FILTER_TOP_K). Candidates is a pipeline-local count
// of retrieved passages — never the number of matches in the knowledge base,
// which retrieval never computes — and a later, narrower cut never replaces
// it. How many of those candidates the prompt actually shows is counted where
// the prompt is built, so the caveat is only emitted when the context really
// is a subset.
type RetrievalTruncation struct {
	Stage      string `json:"stage"`
	Candidates int    `json:"candidates"`
}

// RecordRetrievalCut notes that a ranked-list stage dropped candidate passages
// before the model context was built. candidates is the population that stage
// received, read before the cut; it must never be a knowledge-base match count,
// which retrieval does not compute at this depth.
//
// The first cut wins. Later stages only see an equal or narrower population
// (FILTER_TOP_K cuts merge output, merge's fallback cuts the search output), so
// letting them overwrite the record would restate the candidate pool as
// something smaller than what retrieval produced and hide how much was dropped.
func (c *ChatManage) RecordRetrievalCut(stage string, candidates int) {
	if c == nil || c.Truncation != nil || candidates <= 0 {
		return
	}
	c.Truncation = &RetrievalTruncation{Stage: stage, Candidates: candidates}
}

// PipelineState holds mutable intermediate data that plugins read and write
// as the pipeline progresses.
type PipelineState struct {
	RewriteQuery string      `json:"rewrite_query,omitempty"`
	Intent       QueryIntent `json:"intent,omitempty"`
	History      []*History  `json:"history,omitempty"`
	// HistoryLoaded records that History was fetched this turn, so an empty
	// History (a first turn) is not fetched again by a later stage.
	HistoryLoaded bool `json:"-"`

	SearchResult     []*SearchResult   `json:"-"`
	RerankResult     []*SearchResult   `json:"-"`
	MergeResult      []*SearchResult   `json:"-"`
	Entity           []string          `json:"-"`
	EntityKBIDs      []string          `json:"-"`
	EntityKnowledge  map[string]string `json:"-"`
	GraphResult      *GraphData        `json:"-"`
	UserContent      string            `json:"-"`
	RenderedContexts string            `json:"-"`
	// ContextImages are retrieved images, as data URIs, shown to a vision
	// chat model beside the contexts they belong to; see INTO_CHAT_MESSAGE.
	ContextImages []string `json:"-"`
	// ContextImageChunkIDs identifies the source of each image for final model handle rendering.
	ContextImageChunkIDs []string      `json:"-"`
	ChatResponse         *ChatResponse `json:"-"`
	ImageDescription     string        `json:"-"`
	QuotedContext        string        `json:"-"` // Quoted message text, injected at LLM prompt stage
	SystemPromptOverride string        `json:"-"`
	// Truncation records the first ranked-list cut this turn: the stage that
	// dropped candidates and the size of the candidate pool it cut. Nil when
	// no stage dropped anything, so a prompt only carries the caveat when its
	// context really is a subset.
	Truncation *RetrievalTruncation `json:"-"`

	// MemoryPrompt is the long-term memory envelope appended to the system
	// prompt for this turn, empty when memory is off or nothing matched.
	MemoryPrompt string `json:"-"`
	// UsedMemories mirrors MemoryPrompt in structured form so the answer can
	// tell the user which memories it saw.
	UsedMemories UsedMemories `json:"-"`
	// RerankDiagnostics records what the rerank stage did this turn.
	RerankDiagnostics *RerankDiagnostics `json:"-"`
}

// PipelineContext holds runtime context for the current pipeline execution.
type PipelineContext struct {
	EventBus      EventBusInterface `json:"-"`
	MessageID     string            `json:"-"`
	UserMessageID string            `json:"-"`
}

// ChatManage represents the full configuration, state and runtime context
// for a chat pipeline execution. It embeds PipelineRequest (immutable config),
// PipelineState (mutable intermediate data), and PipelineContext (runtime handles).
type ChatManage struct {
	PipelineRequest
	PipelineState
	PipelineContext
}

// NeedsRetrieval returns true when the current pipeline execution should
// run the retrieval stages (search, rerank, merge, etc.).
// For IntentWebSearch, retrieval is only needed if web search is enabled;
// otherwise the intent prompt (intent_prompts.yaml "web_search") tells the
// user web search is unavailable. All other intents delegate to
// QueryIntent.NeedsKBRetrieval().
func (c *ChatManage) NeedsRetrieval() bool {
	if c.Intent == IntentWebSearch {
		return c.WebSearchEnabled
	}
	return c.Intent.NeedsKBRetrieval()
}

// NormalizeQueryIntent maps a model-produced intent label onto a known
// intent. Case and separators are forgiven ("KB-Search" → kb_search); an
// unknown label becomes the empty intent, which retrieves. Taking the label
// verbatim turned any unexpected value into "no retrieval", so the answer
// was generated without the knowledge base and without an error.
func NormalizeQueryIntent(raw string) QueryIntent {
	label := strings.ToLower(strings.TrimSpace(raw))
	label = strings.NewReplacer("-", "_", " ", "_").Replace(label)
	switch intent := QueryIntent(label); intent {
	case IntentKBSearch, IntentWebSearch, IntentGreeting, IntentChitchat, IntentFollowUp,
		IntentImageOnly, IntentDocOnly, IntentSummarize, IntentClarification:
		return intent
	default:
		return ""
	}
}

// Clone creates a deep copy of the ChatManage object.
// PipelineContext fields (EventBus, MessageID, etc.) are NOT copied because they
// are per-execution handles that should not be shared across clones.
func (c *ChatManage) Clone() *ChatManage {
	knowledgeBaseIDs := make([]string, len(c.KnowledgeBaseIDs))
	copy(knowledgeBaseIDs, c.KnowledgeBaseIDs)

	knowledgeIDs := make([]string, len(c.KnowledgeIDs))
	copy(knowledgeIDs, c.KnowledgeIDs)

	searchTargets := make(SearchTargets, len(c.SearchTargets))
	for i, t := range c.SearchTargets {
		if t != nil {
			kidsCopy := make([]string, len(t.KnowledgeIDs))
			copy(kidsCopy, t.KnowledgeIDs)
			tagIDsCopy := make([]string, len(t.TagIDs))
			copy(tagIDsCopy, t.TagIDs)
			scopeTagIDsCopy := make([]string, len(t.ScopeTagIDs))
			copy(scopeTagIDsCopy, t.ScopeTagIDs)
			searchTargets[i] = &SearchTarget{
				Type:                    t.Type,
				KnowledgeBaseID:         t.KnowledgeBaseID,
				TenantID:                t.TenantID,
				KnowledgeIDs:            kidsCopy,
				TagIDs:                  tagIDsCopy,
				ScopeTagIDs:             scopeTagIDsCopy,
				DisableRecallThresholds: t.DisableRecallThresholds,
			}
		}
	}

	// Deep copy Entity using in search entity plugin
	entity := make([]string, len(c.Entity))
	copy(entity, c.Entity)

	entityKBIDs := make([]string, len(c.EntityKBIDs))
	copy(entityKBIDs, c.EntityKBIDs)

	entityKnowledge := make(map[string]string)
	maps.Copy(entityKnowledge, c.EntityKnowledge)

	return &ChatManage{
		PipelineRequest: PipelineRequest{
			Query:                    c.Query,
			SessionID:                c.SessionID,
			UserID:                   c.UserID,
			MaxRounds:                c.MaxRounds,
			KnowledgeBaseIDs:         knowledgeBaseIDs,
			KnowledgeIDs:             knowledgeIDs,
			SearchTargets:            searchTargets,
			VectorThreshold:          c.VectorThreshold,
			KeywordThreshold:         c.KeywordThreshold,
			EmbeddingTopK:            c.EmbeddingTopK,
			VectorDatabase:           c.VectorDatabase,
			DisableVectorMatch:       c.DisableVectorMatch,
			DisableKeywordsMatch:     c.DisableKeywordsMatch,
			RerankModelID:            c.RerankModelID,
			RerankTopK:               c.RerankTopK,
			RerankThreshold:          c.RerankThreshold,
			ChatModelID:              c.ChatModelID,
			SummaryConfig:            c.SummaryConfig,
			FallbackStrategy:         c.FallbackStrategy,
			FallbackResponse:         c.FallbackResponse,
			FallbackPrompt:           c.FallbackPrompt,
			CitationEnabled:          c.CitationEnabled,
			EnableRewrite:            c.EnableRewrite,
			EnableQueryExpansion:     c.EnableQueryExpansion,
			RewritePromptSystem:      c.RewritePromptSystem,
			RewritePromptUser:        c.RewritePromptUser,
			QueryUnderstandModelID:   c.QueryUnderstandModelID,
			FAQPriorityEnabled:       c.FAQPriorityEnabled,
			FAQDirectAnswerThreshold: c.FAQDirectAnswerThreshold,
			FAQScoreBoost:            c.FAQScoreBoost,
			DataAnalysisEnabled:      c.DataAnalysisEnabled,
			Images:                   append([]string(nil), c.Images...),
			VLMModelID:               c.VLMModelID,
			ChatModelSupportsVision:  c.ChatModelSupportsVision,
			Attachments:              append(MessageAttachments(nil), c.Attachments...),
			TenantID:                 c.TenantID,
			WebSearchEnabled:         c.WebSearchEnabled,
			WebSearchProviderID:      c.WebSearchProviderID,
			WebSearchMaxResults:      c.WebSearchMaxResults,
			WebFetchEnabled:          c.WebFetchEnabled,
			WebFetchTopN:             c.WebFetchTopN,
			Language:                 c.Language,
			IntentPromptOverrides:    maps.Clone(c.IntentPromptOverrides),
		},
		PipelineState: PipelineState{
			RewriteQuery:         c.RewriteQuery,
			Intent:               c.Intent,
			ImageDescription:     c.ImageDescription,
			QuotedContext:        c.QuotedContext,
			SystemPromptOverride: c.SystemPromptOverride,
			MemoryPrompt:         c.MemoryPrompt,
			UsedMemories:         append(UsedMemories(nil), c.UsedMemories...),
			RenderedContexts:     c.RenderedContexts,
			ContextImages:        slices.Clone(c.ContextImages),
			ContextImageChunkIDs: slices.Clone(c.ContextImageChunkIDs),
			Entity:               entity,
			EntityKBIDs:          entityKBIDs,
			EntityKnowledge:      entityKnowledge,
		},
	}
}

// EventType represents different stages in the RAG (Retrieval Augmented Generation) pipeline
type EventType string

const (
	LOAD_HISTORY           EventType = "load_history"
	MEMORY_RECALL          EventType = "memory_recall"
	QUERY_UNDERSTAND       EventType = "query_understand"
	CHUNK_SEARCH           EventType = "chunk_search"
	CHUNK_SEARCH_PARALLEL  EventType = "chunk_search_parallel"
	ENTITY_SEARCH          EventType = "entity_search"
	CHUNK_RERANK           EventType = "chunk_rerank"
	WEB_FETCH              EventType = "web_fetch"
	CHUNK_MERGE            EventType = "chunk_merge"
	DATA_ANALYSIS          EventType = "data_analysis"
	INTO_CHAT_MESSAGE      EventType = "into_chat_message"
	CHAT_COMPLETION        EventType = "chat_completion"
	CHAT_COMPLETION_STREAM EventType = "chat_completion_stream"
	FILTER_TOP_K           EventType = "filter_top_k"
)

// PipelineBuilder dynamically assembles a pipeline as an ordered list of EventTypes.
type PipelineBuilder struct {
	stages []EventType
}

// NewPipelineBuilder returns an empty builder.
func NewPipelineBuilder() *PipelineBuilder {
	return &PipelineBuilder{}
}

// Add appends one or more stages unconditionally.
func (b *PipelineBuilder) Add(stages ...EventType) *PipelineBuilder {
	b.stages = append(b.stages, stages...)
	return b
}

// AddIf appends stages only when the condition is true.
func (b *PipelineBuilder) AddIf(cond bool, stages ...EventType) *PipelineBuilder {
	if cond {
		b.stages = append(b.stages, stages...)
	}
	return b
}

// Build returns the final event list.  The builder must not be reused.
func (b *PipelineBuilder) Build() []EventType {
	out := make([]EventType, len(b.stages))
	copy(out, b.stages)
	return out
}

// Pipeline defines the sequence of events for different chat modes.
// Kept as a convenience lookup for callers that don't need dynamic composition.
var Pipeline = map[string][]EventType{
	"chat": {
		CHAT_COMPLETION,
	},
	"chat_stream": {
		CHAT_COMPLETION_STREAM,
	},
	"chat_history_stream": {
		LOAD_HISTORY,
		CHAT_COMPLETION_STREAM,
	},
	"rag": {
		CHUNK_SEARCH,
		CHUNK_RERANK,
		CHUNK_MERGE,
		INTO_CHAT_MESSAGE,
		CHAT_COMPLETION,
	},
	"rag_stream": {
		LOAD_HISTORY,
		QUERY_UNDERSTAND,
		CHUNK_SEARCH_PARALLEL,
		CHUNK_RERANK,
		CHUNK_MERGE,
		FILTER_TOP_K,
		DATA_ANALYSIS,
		INTO_CHAT_MESSAGE,
		CHAT_COMPLETION_STREAM,
	},
}

// Pipline is a deprecated alias for Pipeline (kept for backward compatibility).
var Pipline = Pipeline

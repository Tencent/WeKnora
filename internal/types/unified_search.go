package types

// UnifiedSearchSource identifies one retrieval source in a unified search.
type UnifiedSearchSource string

const (
	// UnifiedSearchSourceRAG searches document chunks through the existing
	// vector + keyword retrieval pipeline.
	UnifiedSearchSourceRAG UnifiedSearchSource = "rag"
	// UnifiedSearchSourceWiki searches published Wiki pages by text relevance.
	UnifiedSearchSourceWiki UnifiedSearchSource = "wiki"
)

// UnifiedSearchRequest describes a unified RAG + Wiki search.
type UnifiedSearchRequest struct {
	Query      string                `json:"query"`
	Sources    []UnifiedSearchSource `json:"sources,omitempty"`
	TopK       int                   `json:"top_k,omitempty"`
	RAGWeight  float64               `json:"rag_weight,omitempty"`
	WikiWeight float64               `json:"wiki_weight,omitempty"`
	RRFK       int                   `json:"rrf_k,omitempty"`
}

// UnifiedSearchResultSource keeps the source reference when results from
// different indexes are merged or deduplicated.
type UnifiedSearchResultSource struct {
	Type        UnifiedSearchSource `json:"type"`
	ID          string              `json:"id"`
	Title       string              `json:"title,omitempty"`
	KnowledgeID string              `json:"knowledge_id,omitempty"`
	WikiSlug    string              `json:"wiki_slug,omitempty"`
}

// UnifiedSearchResult is the common result shape returned by unified search.
// Source-specific identifiers are optional; Sources is the authoritative
// provenance list when one content item is found by both RAG and Wiki.
type UnifiedSearchResult struct {
	ID              string                      `json:"id,omitempty"`
	Content         string                      `json:"content"`
	Summary         string                      `json:"summary,omitempty"`
	Title           string                      `json:"title,omitempty"`
	Score           float64                     `json:"score"`
	KnowledgeBaseID string                      `json:"knowledge_base_id,omitempty"`
	KnowledgeID     string                      `json:"knowledge_id,omitempty"`
	WikiPageID      string                      `json:"wiki_page_id,omitempty"`
	WikiSlug        string                      `json:"wiki_slug,omitempty"`
	Sources         []UnifiedSearchResultSource `json:"sources"`
}

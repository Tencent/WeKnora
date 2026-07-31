package types

import (
	"database/sql/driver"
	"encoding/json"
)

// SearchTargetType represents the type of search target
type SearchTargetType string

const (
	// SearchTargetTypeKnowledgeBase - search entire knowledge base
	SearchTargetTypeKnowledgeBase SearchTargetType = "knowledge_base"
	// SearchTargetTypeKnowledge - search specific knowledge files within a knowledge base
	SearchTargetTypeKnowledge SearchTargetType = "knowledge"
)

// TagScope represents a tag-constrained retrieval scope inside one knowledge base.
type TagScope struct {
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	TagIDs          []string `json:"tag_ids"`
}

// SearchTarget represents a unified search target
// Either search an entire knowledge base, or specific knowledge files within a knowledge base
type SearchTarget struct {
	// Type of search target
	Type SearchTargetType `json:"type"`
	// KnowledgeBaseID is the ID of the knowledge base to search
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// TenantID is the tenant ID that owns this knowledge base
	// Required for cross-tenant shared KB queries
	TenantID uint64 `json:"tenant_id"`
	// SourceTenantID is the server-authorized owner of this target. TenantID is
	// retained for legacy callers and carries the same value for projections.
	SourceTenantID uint64 `json:"-"`
	// KnowledgeIDs is the list of specific knowledge IDs to search within the knowledge base
	// Only used when Type is SearchTargetTypeKnowledge
	KnowledgeIDs []string `json:"knowledge_ids,omitempty"`
	// TagIDs limits retrieval to chunks/documents carrying any of these KB-local tags.
	TagIDs []string `json:"tag_ids,omitempty"`
	// ScopeTagIDs records the logical tag scope selected by the user. For
	// document KBs this is kept for tracing after the relation-table lookup has
	// been resolved to KnowledgeIDs; TagIDs remains the physical index filter.
	ScopeTagIDs []string `json:"scope_tag_ids,omitempty"`
	// FolderFilter is the resolved, immutable pre-TopK folder constraint.
	FolderFilter ResolvedFolderFilter `json:"-"`
	// ExecutionScopeHash links this projection to its prepared execution scope.
	ExecutionScopeHash string `json:"-"`
	// DisableRecallThresholds keeps recall broad inside an already constrained,
	// user-selected scope. The reranker still orders candidates, but vector and
	// keyword thresholds cannot erase the whole explicit scope before reranking.
	DisableRecallThresholds bool `json:"disable_recall_thresholds,omitempty"`
}

// SearchTargets is a list of search targets, pre-computed at request entry point
type SearchTargets []*SearchTarget

// RecallThresholds returns the effective recall thresholds for this target.
func (st *SearchTarget) RecallThresholds(vectorThreshold, keywordThreshold float64) (float64, float64) {
	if st != nil && st.DisableRecallThresholds {
		return 0, 0
	}
	return vectorThreshold, keywordThreshold
}

// EffectiveSourceTenantID preserves compatibility with legacy targets.
func (st *SearchTarget) EffectiveSourceTenantID() uint64 {
	if st == nil {
		return 0
	}
	if st.SourceTenantID != 0 {
		return st.SourceTenantID
	}
	return st.TenantID
}

// EmptyResolvedTagScope reports a document tag scope whose relation lookup
// produced no knowledge IDs.
func (st *SearchTarget) EmptyResolvedTagScope() bool {
	return st != nil &&
		len(st.ScopeTagIDs) > 0 &&
		len(st.TagIDs) == 0 &&
		len(st.KnowledgeIDs) == 0
}

// HasRecallThresholdOverride reports whether any target represents an
// authoritative scope whose candidates must reach reranking before filtering.
func (st SearchTargets) HasRecallThresholdOverride() bool {
	for _, target := range st {
		if target != nil && target.DisableRecallThresholds {
			return true
		}
	}
	return false
}

// HasKnowledgeRetrievalScope reports whether a request has any effective
// knowledge retrieval scope. SearchTargets are the unified runtime form and
// must be considered alongside the legacy/raw KB and knowledge ID fields so
// tag-only mentions are not mistaken for pure chat.
func HasKnowledgeRetrievalScope(
	searchTargets SearchTargets,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
) bool {
	return len(searchTargets) > 0 || len(knowledgeBaseIDs) > 0 || len(knowledgeIDs) > 0
}

// GetAllKnowledgeBaseIDs returns all unique knowledge base IDs from the search targets
func (st SearchTargets) GetAllKnowledgeBaseIDs() []string {
	seen := make(map[string]bool)
	var result []string
	for _, t := range st {
		if !seen[t.KnowledgeBaseID] {
			seen[t.KnowledgeBaseID] = true
			result = append(result, t.KnowledgeBaseID)
		}
	}
	return result
}

// GetKBTenantMap returns a map from knowledge base ID to tenant ID
func (st SearchTargets) GetKBTenantMap() map[string]uint64 {
	result := make(map[string]uint64)
	for _, t := range st {
		if t.KnowledgeBaseID != "" {
			result[t.KnowledgeBaseID] = t.TenantID
		}
	}
	return result
}

// GetTenantIDForKB returns the tenant ID for a given knowledge base ID
// Returns 0 if not found
func (st SearchTargets) GetTenantIDForKB(kbID string) uint64 {
	for _, t := range st {
		if t.KnowledgeBaseID == kbID {
			return t.TenantID
		}
	}
	return 0
}

// ContainsKB checks if the search targets contain a given knowledge base ID
func (st SearchTargets) ContainsKB(kbID string) bool {
	for _, t := range st {
		if t.KnowledgeBaseID == kbID {
			return true
		}
	}
	return false
}

// SearchResult represents the search result
type SearchResult struct {
	// ID
	ID string `gorm:"column:id"              json:"id"`
	// Content
	Content string `gorm:"column:content"         json:"content"`
	// Knowledge ID
	KnowledgeID string `gorm:"column:knowledge_id"    json:"knowledge_id"`
	// Chunk index
	ChunkIndex int `gorm:"column:chunk_index"     json:"chunk_index"`
	// Knowledge title
	KnowledgeTitle string `gorm:"column:knowledge_title" json:"knowledge_title"`
	// Start at
	StartAt int `gorm:"column:start_at"        json:"start_at"`
	// End at
	EndAt int `gorm:"column:end_at"          json:"end_at"`
	// Seq
	Seq int `gorm:"column:seq"             json:"seq"`
	// Score
	Score float64 `                              json:"score"`
	// Match type
	MatchType MatchType `                              json:"match_type"`
	// SubChunkIndex
	SubChunkID []string `                              json:"sub_chunk_id"`
	// Metadata
	Metadata map[string]string `                              json:"metadata"`

	// Chunk 类型
	ChunkType string `json:"chunk_type"`
	// 父 Chunk ID
	ParentChunkID string `json:"parent_chunk_id"`
	// 图片信息 (JSON 格式)
	ImageInfo string `json:"image_info"`

	// Knowledge file name
	// Used for file type knowledge, contains the original file name
	KnowledgeFilename string `json:"knowledge_filename"`

	// Knowledge source
	// Used to indicate the source of the knowledge, such as "url"
	KnowledgeSource string `json:"knowledge_source"`

	// KnowledgeChannel indicates through which channel the knowledge was ingested (web, api, wechat, etc.)
	KnowledgeChannel string `json:"knowledge_channel"`

	// ChunkMetadata stores chunk-level metadata (e.g., generated questions)
	ChunkMetadata JSON `json:"chunk_metadata,omitempty"`

	// MatchedContent is the actual content that was matched in vector search
	// For FAQ: this is the matched question text (standard or similar question)
	MatchedContent string `json:"matched_content,omitempty"`

	// KnowledgeDescription is the description of the knowledge document
	KnowledgeDescription string `json:"knowledge_description,omitempty"`

	// KnowledgeBaseID is the ID of the knowledge base this result belongs to
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
}

// SearchParams represents the search parameters
type SearchParams struct {
	QueryText            string               `json:"query_text"`
	QueryEmbedding       []float32            `json:"query_embedding,omitempty"`
	VectorThreshold      float64              `json:"vector_threshold"`
	KeywordThreshold     float64              `json:"keyword_threshold"`
	MatchCount           int                  `json:"match_count"`
	DisableKeywordsMatch bool                 `json:"disable_keywords_match"`
	DisableVectorMatch   bool                 `json:"disable_vector_match"`
	KnowledgeIDs         []string             `json:"knowledge_ids"`
	TagIDs               []string             `json:"tag_ids"` // Tag IDs for filtering (used for FAQ priority filtering)
	ScopeTagIDs          []string             `json:"scope_tag_ids,omitempty"`
	SourceTenantID       uint64               `json:"-"`
	FolderFilter         ResolvedFolderFilter `json:"-"`
	ExecutionScopeHash   string               `json:"-"`
	OnlyRecommended      bool                 `json:"only_recommended"`
	// KnowledgeBaseIDs overrides the single KB ID passed to HybridSearch,
	// allowing a single retrieval call to span multiple KBs that share the
	// same embedding model. When empty, HybridSearch uses its own id parameter.
	KnowledgeBaseIDs []string `json:"knowledge_base_ids,omitempty"`
	// SkipContextEnrichment skips fetching parent, nearby, and relation chunks
	// in processSearchResults. Used by the chat pipeline where context assembly
	// is handled separately in the merge stage.
	SkipContextEnrichment bool `json:"skip_context_enrichment,omitempty"`
}

// EmptyResolvedTagScope reports a document tag scope with no candidates.
func (p SearchParams) EmptyResolvedTagScope() bool {
	return len(p.ScopeTagIDs) > 0 &&
		len(p.TagIDs) == 0 &&
		len(p.KnowledgeIDs) == 0
}

// ProjectKnowledgeScopeToSearchTargets creates independent runtime targets.
func ProjectKnowledgeScopeToSearchTargets(
	scope *KnowledgeScope,
	executionScopeHash string,
) SearchTargets {
	if scope == nil {
		return nil
	}
	targets := scope.Targets()
	projected := make(SearchTargets, 0, len(targets))
	for _, target := range targets {
		knowledgeIDs := target.KnowledgeIDs()
		folderFilter := target.FolderFilter()
		if folderFilter.Enabled() && len(knowledgeIDs) == 0 {
			continue
		}
		tagIDs := target.TagIDs()
		scopeTagIDs := target.ScopeTagIDs()
		sourceTenantID := target.SourceTenantID()
		targetType := SearchTargetTypeKnowledgeBase
		if len(knowledgeIDs) > 0 ||
			(len(scopeTagIDs) > 0 && len(tagIDs) == 0) {
			targetType = SearchTargetTypeKnowledge
		}
		projected = append(projected, &SearchTarget{
			Type:               targetType,
			KnowledgeBaseID:    target.KnowledgeBaseID(),
			TenantID:           sourceTenantID,
			SourceTenantID:     sourceTenantID,
			KnowledgeIDs:       knowledgeIDs,
			TagIDs:             tagIDs,
			ScopeTagIDs:        scopeTagIDs,
			FolderFilter:       folderFilter,
			ExecutionScopeHash: executionScopeHash,
			DisableRecallThresholds: len(knowledgeIDs) > 0 ||
				len(tagIDs) > 0 ||
				len(scopeTagIDs) > 0,
		})
	}
	return projected
}

// Clone returns an ownership-independent runtime projection.
func (st *SearchTarget) Clone() *SearchTarget {
	if st == nil {
		return nil
	}
	return &SearchTarget{
		Type:                    st.Type,
		KnowledgeBaseID:         st.KnowledgeBaseID,
		TenantID:                st.TenantID,
		SourceTenantID:          st.SourceTenantID,
		KnowledgeIDs:            append([]string(nil), st.KnowledgeIDs...),
		TagIDs:                  append([]string(nil), st.TagIDs...),
		ScopeTagIDs:             append([]string(nil), st.ScopeTagIDs...),
		FolderFilter:            st.FolderFilter.Clone(),
		ExecutionScopeHash:      st.ExecutionScopeHash,
		DisableRecallThresholds: st.DisableRecallThresholds,
	}
}

// Value implements the driver.Valuer interface, used to convert SearchResult to database value
func (c SearchResult) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to SearchResult
func (c *SearchResult) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Pagination represents the pagination parameters
type Pagination struct {
	// Page
	Page int `form:"page"      json:"page"      binding:"omitempty,min=1"`
	// Page size
	PageSize int `form:"page_size" json:"page_size" binding:"omitempty,min=1,max=1000"`
}

// GetPage gets the page number, default is 1
func (p *Pagination) GetPage() int {
	if p.Page < 1 {
		return 1
	}
	return p.Page
}

// GetPageSize gets the page size, default is 20
func (p *Pagination) GetPageSize() int {
	if p.PageSize < 1 {
		return 20
	}
	if p.PageSize > 1000 {
		return 1000
	}
	return p.PageSize
}

// Offset gets the offset for database query
func (p *Pagination) Offset() int {
	return (p.GetPage() - 1) * p.GetPageSize()
}

// Limit gets the limit for database query
func (p *Pagination) Limit() int {
	return p.GetPageSize()
}

// PageResult represents the pagination query result
type PageResult struct {
	Total    int64       `json:"total"`     // Total number of records
	Page     int         `json:"page"`      // Current page number
	PageSize int         `json:"page_size"` // Page size
	Data     interface{} `json:"data"`      // Data
}

// NewPageResult creates a new pagination result
func NewPageResult(total int64, page *Pagination, data interface{}) *PageResult {
	return &PageResult{
		Total:    total,
		Page:     page.GetPage(),
		PageSize: page.GetPageSize(),
		Data:     data,
	}
}

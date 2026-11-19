package types

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Internal candidate budgets are independent of the caller's response limits.
const (
	GraphSearchMaxSeeds          = 100
	GraphSearchMaxRows           = 512
	GraphSearchMaxEvidence       = 512
	GraphSearchMaxNameRunes      = 512
	GraphSearchMaxAttributeRunes = 1000
	GraphSearchMaxAttributes     = 20
	GraphSearchMaxContentRunes   = 2000
	GraphSearchTimeout           = 10 * time.Second
)

// GraphSearchRequest matches a case-sensitive entity-name substring. It does
// not run a model, generate embeddings or change the regular retrieval pipeline.
type GraphSearchRequest struct {
	Query           string `json:"query" binding:"required" minLength:"1" maxLength:"512"`
	MaxNodes        int    `json:"max_nodes" minimum:"0" maximum:"100" default:"50"`
	MaxRelations    int    `json:"max_relations" minimum:"0" maximum:"200" default:"100"`
	MaxSourceChunks int    `json:"max_source_chunks" minimum:"0" maximum:"50" default:"20"`
}

// Normalize validates both HTTP and direct service callers. Zero means default.
func (r *GraphSearchRequest) Normalize() error {
	r.Query = strings.TrimSpace(r.Query)
	if r.Query == "" || !utf8.ValidString(r.Query) || utf8.RuneCountInString(r.Query) > GraphSearchMaxNameRunes {
		return fmt.Errorf("query must contain 1 to %d characters", GraphSearchMaxNameRunes)
	}
	for _, limit := range []struct {
		name                  string
		value                 *int
		defaultValue, maximum int
	}{
		{"max_nodes", &r.MaxNodes, 50, 100},
		{"max_relations", &r.MaxRelations, 100, 200},
		{"max_source_chunks", &r.MaxSourceChunks, 20, 50},
	} {
		if *limit.value < 0 || *limit.value > limit.maximum {
			return fmt.Errorf("%s must be between 1 and %d, or 0 for the default", limit.name, limit.maximum)
		}
		if *limit.value == 0 {
			*limit.value = limit.defaultValue
		}
	}
	return nil
}

// GraphSearchCandidates is internal: physical graph identities never cross the
// public API boundary. Truncation reports candidate caps, not an invented total.
type GraphSearchCandidates struct {
	Graph             *GraphData
	TruncationReasons []string
}

// GraphSearchEntity identifies an entity instance within a live document.
type GraphSearchEntity struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	KnowledgeID    string   `json:"knowledge_id"`
	Attributes     []string `json:"attributes"`
	SourceChunkIDs []string `json:"source_chunk_ids"`
}

// GraphSearchRelation connects two returned, same-document entities.
type GraphSearchRelation struct {
	ID             string   `json:"id"`
	SourceID       string   `json:"source_id"`
	TargetID       string   `json:"target_id"`
	Type           string   `json:"type"`
	SourceChunkIDs []string `json:"source_chunk_ids"`
}

// GraphSearchSourceChunk is a bounded, live SQL projection, not a raw Chunk or
// Knowledge row. It excludes file paths, metadata, image URLs and owner details.
type GraphSearchSourceChunk struct {
	ID               string `json:"id"`
	KnowledgeID      string `json:"knowledge_id"`
	Title            string `json:"title"`
	Content          string `json:"content"`
	ChunkIndex       int    `json:"chunk_index"`
	StartAt          int    `json:"start_at"`
	EndAt            int    `json:"end_at"`
	ContentTruncated bool   `json:"content_truncated" gorm:"-"`
	TitleTruncated   bool   `json:"title_truncated" gorm:"-"`
	TenantID         uint64 `json:"-"`
	KnowledgeBaseID  string `json:"-"`
}

// GraphSearchMeta reports effective limits and observed clipping, not totals.
type GraphSearchMeta struct {
	Truncated         bool     `json:"truncated"`
	TruncationReasons []string `json:"truncation_reasons"`
	MaxNodes          int      `json:"max_nodes"`
	MaxRelations      int      `json:"max_relations"`
	MaxSourceChunks   int      `json:"max_source_chunks"`
}

// GraphSearchResult is the stable public JSON contract, separate from GraphData.
type GraphSearchResult struct {
	KnowledgeBaseID string                   `json:"knowledge_base_id"`
	Query           string                   `json:"query"`
	Entities        []GraphSearchEntity      `json:"entities"`
	Relationships   []GraphSearchRelation    `json:"relationships"`
	SourceChunks    []GraphSearchSourceChunk `json:"source_chunks"`
	Meta            GraphSearchMeta          `json:"meta"`
}

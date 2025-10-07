package types

// EnhancedSearchRequest describes an enhanced search query.
type EnhancedSearchRequest struct {
	VirtualKBID *int64            `json:"virtual_kb_id"`
	TagFilters  []VirtualKBFilter `json:"tag_filters"`
	Limit       int               `json:"limit"`
}

// DocumentScore represents a weighted document result.
type DocumentScore struct {
	DocumentID string  `json:"document_id"`
	Score      float64 `json:"score"`
}

// EnhancedSearchResponse aggregates search results.
type EnhancedSearchResponse struct {
	Results []DocumentScore `json:"results"`
}

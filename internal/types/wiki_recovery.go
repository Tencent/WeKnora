package types

// WikiDocument separates source ingestion from its Wiki result.
type WikiDocument struct {
	KnowledgeID string `json:"knowledge_id"`
	Title       string `json:"title"`
	ParseStatus string `json:"parse_status"`
	WikiStatus  string `json:"wiki_status"`
	LastError   string `json:"last_error"`
	FailCount   int    `json:"fail_count"`
}

// WikiDocumentList is a paginated view of document-level Wiki results.
type WikiDocumentList struct {
	Documents   []*WikiDocument `json:"documents"`
	Total       int64           `json:"total"`
	FailedTotal int64           `json:"failed_total"`
}

package types

// WikiKnowledgePageImpact describes one currently published Wiki page that is
// supported by a source knowledge. It is read from wiki_page_sources rather
// than the legacy wiki_pages.source_refs field.
type WikiKnowledgePageImpact struct {
	PageID              string `json:"page_id"`
	Slug                string `json:"slug"`
	Title               string `json:"title"`
	Summary             string `json:"summary"`
	PageType            string `json:"page_type"`
	FolderID            string `json:"folder_id,omitempty"`
	SupportedBlockCount int    `json:"supported_block_count"`
	TotalSourceCount    int    `json:"total_source_count"`
}

// WikiKnowledgeSourceCleanupResult reports the rows removed when a source
// knowledge is deleted. Repeating the cleanup is valid and returns zero row
// counts, which makes file deletion retries idempotent.
type WikiKnowledgeSourceCleanupResult struct {
	AffectedPages             []WikiKnowledgePageImpact `json:"affected_pages"`
	DeletedBlockSources       int64                     `json:"deleted_block_sources"`
	DeletedPageSources        int64                     `json:"deleted_page_sources"`
	DeletedKnowledgeRevisions int64                     `json:"deleted_knowledge_revisions"`
}

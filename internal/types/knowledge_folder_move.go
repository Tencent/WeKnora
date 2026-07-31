package types

// KnowledgeFolderMoveInput contains the fully resolved scope and placement for
// one transactional batch move. KnowledgeIDs are normalized by the service;
// repositories still copy, deduplicate, and sort them before taking row locks.
type KnowledgeFolderMoveInput struct {
	TenantID        uint64
	KnowledgeBaseID string
	KnowledgeIDs    []string
	TargetFolderID  string
}

// KnowledgeFolderMoveResult reports only aggregate outcomes so callers do not
// expose which requested knowledge IDs were changed.
type KnowledgeFolderMoveResult struct {
	ChangedCount   int `json:"changed_count"`
	UnchangedCount int `json:"unchanged_count"`
}

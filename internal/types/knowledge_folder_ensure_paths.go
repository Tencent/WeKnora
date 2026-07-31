package types

// KnowledgeFolderEnsurePathsRequest describes folder paths to create or reuse.
type KnowledgeFolderEnsurePathsRequest struct {
	ParentID string                           `json:"parent_id"`
	Paths    []KnowledgeFolderEnsurePathInput `json:"paths"`
}

// KnowledgeFolderEnsurePathInput is one ordered client path request.
type KnowledgeFolderEnsurePathInput struct {
	ClientKey string   `json:"client_key"`
	Segments  []string `json:"segments"`
}

// KnowledgeFolderEnsurePathResult identifies the terminal folder for one client path.
type KnowledgeFolderEnsurePathResult struct {
	ClientKey string
	FolderID  string
}

package types

// KnowledgeFileImportOptions controls ingestion behaviour for a file upload.
// It is intentionally separate from KnowledgeProcessOverrides: these options
// decide whether a file enters the pipeline at all, rather than how it is
// parsed once queued.
type KnowledgeFileImportOptions struct {
	// FolderUpload marks the directory-upload flow. It scopes duplicate checks
	// by the full logical path, while ordinary file uploads keep their historic
	// content-based duplicate semantics.
	FolderUpload bool
	// DeferProcessing persists a parseable file as pending without enqueueing
	// it. Folder upload finalization uses this to register every sibling before
	// any Markdown reference is resolved.
	DeferProcessing bool
	// StoreOnly persists an attachment and its resource handle, but never
	// parses, chunks, or embeds it.
	StoreOnly bool
}

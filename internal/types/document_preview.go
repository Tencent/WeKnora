package types

import (
	"io"
	"time"
)

// Persistent preview constants identify jobs, versions, leases, and resource bindings.
const (
	TypeDocumentPreview         = "document:preview"
	DocumentPreviewVersion      = "docx-v1"
	DocumentPreviewLease        = 10 * time.Minute
	ResourceRelationPreviewFile = "preview_file"
)

// DocumentPreviewState is internal durable task payload, never an API model.
// It lives in task_pending_ops so generic knowledge metadata writes cannot
// clobber its ownership token. FileHash follows the source's existing MD5 contract.
type DocumentPreviewState struct {
	KnowledgeID     string    `json:"knowledge_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	SourcePath      string    `json:"source_path"`
	SourceHash      string    `json:"source_hash"`
	Version         string    `json:"version"`
	Status          string    `json:"status"`
	Token           string    `json:"token"`
	ResourceRef     string    `json:"resource_ref,omitempty"`
	NextAttempt     time.Time `json:"next_attempt,omitempty"`
}

// DocumentPreviewArtifact is a write intent persisted before physical IO.
// Retained while ready: this is also the deletion/expiration cleanup ledger.
type DocumentPreviewArtifact struct {
	StateID      int64     `json:"state_id"`
	Token        string    `json:"token"`
	KnowledgeID  string    `json:"knowledge_id"`
	PhysicalPath string    `json:"physical_path"`
	NotBefore    time.Time `json:"not_before"`
	NextCheck    time.Time `json:"next_check"`
}

// DocumentPreviewResult describes the current state and optional ready content.
type DocumentPreviewResult struct {
	Status  string
	Content io.ReadCloser
}

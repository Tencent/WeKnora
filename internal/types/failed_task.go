package types

import "time"

// FailedTaskInfo is the operator-facing projection of an asynq task that
// exhausted its automatic retry budget. Payloads are intentionally not
// exposed by the SystemAdmin API: they may contain document content or
// connector credentials. Only stable routing identifiers are copied out.
type FailedTaskInfo struct {
	ID              string    `json:"id"`
	Queue           string    `json:"queue"`
	Type            string    `json:"type"`
	LastError       string    `json:"last_error"`
	LastFailedAt    time.Time `json:"last_failed_at"`
	Retried         int       `json:"retried"`
	MaxRetry        int       `json:"max_retry"`
	TenantID        uint64    `json:"tenant_id,omitempty"`
	KnowledgeBaseID string    `json:"knowledge_base_id,omitempty"`
	KnowledgeID     string    `json:"knowledge_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
}

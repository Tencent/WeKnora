package types

import "time"

// MemorySubjectSummary 提供管理员可查看的主体标识和统计信息，不包含记忆内容或提取状态。
type MemorySubjectSummary struct {
	ID             string    `json:"id"`
	SubjectID      string    `json:"subject_id"`
	PrincipalType  string    `json:"principal_type"`
	Enabled        bool      `json:"enabled"`
	ItemCount      int64     `json:"item_count"`
	ActiveCount    int64     `json:"active_count"`
	PendingCount   int64     `json:"pending_count"`
	LastActivityAt time.Time `json:"last_activity_at"`
}

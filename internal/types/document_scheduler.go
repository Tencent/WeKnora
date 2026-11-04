package types

import "time"

// DocumentClaim identifies one execution. Only its owner may renew or finish it.
type DocumentClaim struct {
	Op         *TaskPendingOp
	Owner      string
	Generation string
}

// DocumentExecutionLease remains authoritative even if its pending row is deleted.
// It deliberately has no foreign key to the pending row, knowledge or scope.
type DocumentExecutionLease struct {
	TenantID    uint64 `gorm:"primaryKey;autoIncrement:false"`
	KnowledgeID string `gorm:"primaryKey"`
	PendingID   int64
	Owner       string
	ExpiresAt   time.Time
}

// TableName binds execution leases to their independent scheduler table.
func (DocumentExecutionLease) TableName() string { return "document_execution_leases" }

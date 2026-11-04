package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// DocumentSchedulerRepository is the optional durable document admission capability.
// Capacity and the tenant cursor are shared by all replicas through the database.
type DocumentSchedulerRepository interface {
	EnqueueDocument(context.Context, *types.TaskPendingOp) (bool, error)
	ClaimDocument(context.Context, int, time.Duration) (*types.DocumentClaim, error)
	RenewDocument(context.Context, *types.DocumentClaim, time.Duration) (bool, error)
	// FinishDocument accepts success, retry, terminal, or release. Release does not consume a retry.
	// The boolean reports an owned settlement of an existing original pending row.
	FinishDocument(context.Context, *types.DocumentClaim, string, time.Time, string) (bool, error)
	RecoverDocuments(context.Context) error
	DocumentTenants(context.Context) ([]uint64, error)
	HasDocumentWork(context.Context, uint64) (bool, error)
	DocumentKnowledgeIDs(context.Context) (map[string]struct{}, error)
}

// DocumentFailureRecoveryRepository also reports newly archived abandoned
// executions, allowing callers to finalize their original attempt's trace.
type DocumentFailureRecoveryRepository interface {
	RecoverDocumentFailures(context.Context) ([]*types.TaskPendingOp, error)
}

// DocumentPendingCleanupRepository fences cancellation cleanup to the pending
// rows observed before status mutation, preserving later reparse enqueues.
type DocumentPendingCleanupRepository interface {
	SnapshotDocumentPending(context.Context, uint64, string) ([]int64, error)
	DeleteDocumentPending(context.Context, uint64, string, []int64) error
}

// DocumentGenerationRepository fences producer mutations independently of optional trace attempts.
type DocumentGenerationRepository interface {
	CheckDocumentGeneration(context.Context, uint64, string, string) (bool, error)
	BeginDocumentGeneration(context.Context, uint64, string, string, string) (string, error)
	InitialDocumentGeneration(context.Context, uint64, string, string) (string, error)
	EnqueueDocumentGeneration(context.Context, *types.TaskPendingOp, string) (bool, error)
	UpdateDocumentGeneration(context.Context, uint64, string, string, map[string]any) (bool, error)
	SaveDocumentGeneration(context.Context, string, *types.Knowledge) (bool, error)
	FailLegacyDocumentGeneration(context.Context, *types.DocumentProcessPayload, string) (bool, error)
}

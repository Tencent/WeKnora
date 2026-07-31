package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFolderMoveService moves active knowledge within one knowledge-base
// folder tree and returns aggregate changed/unchanged counts.
type KnowledgeFolderMoveService interface {
	MoveKnowledge(
		ctx context.Context,
		input *types.KnowledgeFolderMoveInput,
	) (*types.KnowledgeFolderMoveResult, error)
}

// KnowledgeFolderMoveUpdate is the compare-and-swap state for one knowledge
// placement change. FolderIndexedVersion is intentionally absent: moving a
// knowledge leaves its existing checkpoint unchanged and increments only the
// authoritative FolderVersion.
type KnowledgeFolderMoveUpdate struct {
	TenantID              uint64
	KnowledgeBaseID       string
	KnowledgeID           string
	ExpectedFolderID      string
	ExpectedFolderVersion uint64
	TargetFolderID        string
	UpdatedAt             time.Time
}

// KnowledgeFolderMoveTxRepository exposes only the reads and writes allowed
// inside a serialized knowledge-folder move transaction.
type KnowledgeFolderMoveTxRepository interface {
	KnowledgeFolderReader
	LockKnowledgeForFolderMove(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		knowledgeIDs []string,
	) ([]*types.Knowledge, error)
	UpdateKnowledgeFolderForMove(
		ctx context.Context,
		update KnowledgeFolderMoveUpdate,
	) error
	UpsertKnowledgeFolderIndexPending(
		ctx context.Context,
		pending *types.KnowledgeFolderIndexPending,
	) error
}

// KnowledgeFolderMoveWriteFunc runs inside a replay-safe folder move
// transaction. SQLite may replay it; callbacks must not perform network or
// non-rollbackable side effects.
type KnowledgeFolderMoveWriteFunc func(repo KnowledgeFolderMoveTxRepository) error

// KnowledgeFolderMoveRepository serializes document placement changes with
// folder tree writes in the same tenant and knowledge-base scope.
type KnowledgeFolderMoveRepository interface {
	RunKnowledgeFolderMoveTransaction(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		fn KnowledgeFolderMoveWriteFunc,
	) error
}

// KnowledgeCrossKBMoveRepository performs the one scoped database update that
// switches a knowledge to another knowledge base and resets its folder state.
type KnowledgeCrossKBMoveRepository interface {
	UpdateKnowledgeForCrossKBMove(
		ctx context.Context,
		knowledge *types.Knowledge,
		sourceKnowledgeBaseID string,
	) error
}

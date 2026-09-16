package interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	// ErrEvaluationQuestionResultConflict reports a different result hash for
	// an already published sample_index.
	ErrEvaluationQuestionResultConflict = errors.New("evaluation question result conflict")
	// ErrEvaluationQuestionResultCanceled reports a publication attempt after
	// the task observed a cancellation request.
	ErrEvaluationQuestionResultCanceled = errors.New("evaluation question result canceled")
)

// EvaluationQuestionResultCommand is one transactional per-question
// publication: row insert, finished increment, aggregate metric update, and
// task version bump commit or roll back together.
type EvaluationQuestionResultCommand struct {
	TenantID        uint64
	TaskID          string
	OwnerID         string
	ExpectedVersion uint64
	Total           int
	Finished        int
	Metric          types.JSON
	RuntimeMetrics  types.JSON
	Now             time.Time
	LeaseExpiresAt  time.Time
	Result          *types.EvaluationQuestionResultInput
}

// EvaluationQuestionResultRepository persists per-question facts.
type EvaluationQuestionResultRepository interface {
	// PublishQuestionResult atomically inserts one per-question row and
	// advances the owning task. Identical result hashes are idempotent;
	// differing hashes conflict; expired owners and canceled workers are
	// rejected by the task-state conditions.
	PublishQuestionResult(
		ctx context.Context,
		command EvaluationQuestionResultCommand,
	) (*types.EvaluationTaskEntity, bool, error)
	// ListQuestionResults returns one keyset page in ascending sample_index.
	ListQuestionResults(
		ctx context.Context,
		tenantID uint64,
		taskID string,
		sampleIndexFrom int,
		limit int,
	) ([]*types.EvaluationQuestionResultEntity, error)
}

package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// LearningService provides private practice and progress for authenticated learners.
type LearningService interface {
	GetSettings(context.Context) (*types.LearningSettings, error)
	SetEnabled(context.Context, bool) (*types.LearningSettings, error)
	Overview(context.Context, string) (*types.LearningOverview, error)
	Node(context.Context, string) (*types.LearningNodeView, error)
	Recommend(context.Context, string, int) ([]*types.LearningRecommendation, error)
	RecordView(context.Context, string) error
	Overlay(context.Context, string, []string) ([]*types.LearningNodeView, error)
	PrepareQuiz(context.Context, string) (*types.LearningQuizView, error)
	GetQuiz(context.Context, string) (*types.LearningQuizView, error)
	SubmitAnswer(context.Context, types.LearningAnswer) (*types.LearningAnswerResult, error)
	Export(context.Context, string) (*types.LearningExport, error)
	Clear(context.Context, string) (*types.LearningClearResult, error)
	Handle(context.Context, *asynq.Task) error
	Recover(context.Context) error
}

// LearningScope is resolved only from explicit immutable Web authentication context.
type LearningScope struct {
	TenantID  uint64
	SubjectID string
}

// LearningRepository owns all transactions, including profile fencing and
// source binding checks. No database handle or transaction reaches the service.
type LearningRepository interface {
	Settings(context.Context, LearningScope) (*types.LearningSettings, error)
	SetEnabled(context.Context, LearningScope, bool) (*types.LearningSettings, error)
	Overview(context.Context, LearningScope, string) (*types.LearningOverview, error)
	Node(context.Context, LearningScope, string) (*types.LearningNode, error)
	Overlay(context.Context, LearningScope, string, []string) ([]*types.LearningNode, error)
	Candidates(context.Context, LearningScope, string, []string) ([]*types.LearningNode, error)
	RecordView(context.Context, LearningScope, string) error
	PrepareQuiz(context.Context, LearningScope, string) (*types.LearningQuizView, *types.LearningGeneratePayload, error)
	GetQuiz(context.Context, LearningScope, string) (*types.LearningQuizView, error)
	SubmitAnswer(context.Context, LearningScope, types.LearningAnswer) (*types.LearningAnswerResult, error)
	Export(context.Context, LearningScope, string) (*types.LearningExport, error)
	Clear(context.Context, LearningScope, string) (*types.LearningClearResult, error)
	Claim(context.Context, types.LearningGeneratePayload) (*types.LearningClaim, error)
	Publish(context.Context, *types.LearningClaim, []types.LearningQuestion) error
	Fail(context.Context, *types.LearningClaim, string) error
	Recover(context.Context, int) ([]types.LearningGeneratePayload, error)
}

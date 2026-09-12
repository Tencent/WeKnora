package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// EvaluationHumanRatingRepository appends and lists immutable human judgments.
type EvaluationHumanRatingRepository interface {
	AppendHumanRating(
		context.Context, uint64, string, int, string, types.EvaluationHumanRatingInput,
	) (*types.EvaluationHumanRatingRevision, error)
	ListHumanRatings(
		context.Context, uint64, string, int,
	) ([]*types.EvaluationHumanRatingRevision, error)
}

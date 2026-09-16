package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEvaluationHumanRatingsAppendImmutableRevisions(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.EvaluationHumanRatingRevision{}))
	taskRepo := NewEvaluationTaskRepository(db)
	questionRepo := NewEvaluationQuestionResultRepository(db)
	task := newEvaluationTaskEntity(41, "human-rating")
	started := startEvaluationQuestionTask(t, taskRepo, task)
	_, inserted, err := questionRepo.PublishQuestionResult(context.Background(),
		newEvaluationQuestionCommandFixture(started, started.Version, 0))
	require.NoError(t, err)
	require.True(t, inserted)

	ratings := NewEvaluationHumanRatingRepository(db)
	input := types.EvaluationHumanRatingInput{
		RubricKey: "answer-quality", RubricVersion: "1.0.0",
		RubricSnapshot: types.JSON(`{"title":"Answer quality","scale":{"1":"poor","5":"excellent"}}`),
		Score:          4, Comment: "supported by the reference",
	}
	first, err := ratings.AppendHumanRating(context.Background(), task.TenantID, task.ID, 0, "rater-1", input)
	require.NoError(t, err)
	require.Equal(t, 1, first.Revision)
	input.Score = 5
	second, err := ratings.AppendHumanRating(context.Background(), task.TenantID, task.ID, 0, "rater-1", input)
	require.NoError(t, err)
	require.Equal(t, 2, second.Revision)
	require.NotNil(t, second.SupersedesID)
	require.Equal(t, first.ID, *second.SupersedesID)

	input.RubricVersion = "2.0.0"
	third, err := ratings.AppendHumanRating(context.Background(), task.TenantID, task.ID, 0, "rater-2", input)
	require.NoError(t, err)
	require.Equal(t, 3, third.Revision)
	require.Nil(t, third.SupersedesID)

	items, err := ratings.ListHumanRatings(context.Background(), task.TenantID, task.ID, 0)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, 3, items[0].Revision)
	require.Equal(t, 2, items[1].Revision)
	require.Equal(t, 1, items[2].Revision)
	require.NotEqual(t, items[0].ID, items[1].ID)
}

func TestEvaluationHumanRatingsRejectInvalidRubric(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.EvaluationHumanRatingRevision{}))
	_, err := NewEvaluationHumanRatingRepository(db).AppendHumanRating(
		context.Background(), 7, "task", 0, "rater", types.EvaluationHumanRatingInput{
			RubricKey: "quality", RubricVersion: "1", RubricSnapshot: types.JSON(`[]`), Score: 3,
		},
	)
	require.ErrorContains(t, err, "JSON object")
}

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func learningAnsweredQuiz(
	t *testing.T,
	db *gorm.DB,
	scope interfaces.LearningScope,
	p *types.WikiPage,
	c *types.Chunk,
	variation string,
) *types.LearningQuizView {
	t.Helper()
	repo, ctx := NewLearningRepository(db), context.Background()
	_, err := repo.SetEnabled(ctx, scope, true)
	require.NoError(t, err)
	_, wake, err := repo.PrepareQuiz(ctx, scope, p.ID)
	require.NoError(t, err)
	claim, err := repo.Claim(ctx, *wake)
	require.NoError(t, err)
	require.NotNil(t, claim)
	questions := learningTestQuestions(c)
	for i := range questions {
		questions[i].Prompt = variation + questions[i].Prompt
	}
	require.NoError(t, repo.Publish(ctx, claim, questions))
	q, err := repo.GetQuiz(ctx, scope, claim.Quiz.ID)
	require.NoError(t, err)
	_, err = repo.SubmitAnswer(ctx, scope, types.LearningAnswer{
		QuestionID: q.Questions[0].ID, OptionID: "a", AttemptID: uuid.NewString(),
	})
	require.NoError(t, err)
	return q
}

func TestLearningDeletedSourcePurgesSurvivingPageHistory(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, action := range []string{
			"export", "recover", "uncited_source", "hard_delete", "move_tenant", "new_assessment",
		} {
			t.Run(dialect+"/"+action, func(t *testing.T) {
				db, ctx := learningTestDB(t, dialect), context.Background()
				repo := NewLearningRepository(db)
				scope := interfaces.LearningScope{TenantID: 7, SubjectID: "web_user:alice"}
				p, c := learningSeed(t, db)
				p.Slug = "concept/multi-source"
				require.NoError(t, db.Save(p).Error)
				healthyPage, healthyChunk := learningSeed(t, db)
				healthy := learningAnsweredQuiz(t, db, scope, healthyPage, healthyChunk, "healthy ")
				secondDoc := &types.Knowledge{
					ID: uuid.NewString(), TenantID: 7, KnowledgeBaseID: p.KnowledgeBaseID,
					EnableStatus: "enabled", ParseStatus: "completed", CustomMetadata: types.JSON("{}"),
				}
				require.NoError(t, db.Create(secondDoc).Error)
				secondChunk := *c
				secondChunk.ID, secondChunk.KnowledgeID = uuid.NewString(), secondDoc.ID
				secondChunk.SeqID = 0
				require.NoError(t, db.Create(&secondChunk).Error)
				p.SourceRefs = append(p.SourceRefs, secondDoc.ID+"|Second source")
				p.ChunkRefs = append(p.ChunkRefs, secondChunk.ID)
				require.NoError(t, db.Save(p).Error)
				q := learningAnsweredQuiz(t, db, scope, p, c, "old ")
				removedID, remainingChunk := c.KnowledgeID, &secondChunk
				if action == "uncited_source" {
					removedID, remainingChunk = secondDoc.ID, c
				}
				// Wiki cleanup can prune a source reference without deleting the page.
				p.SourceRefs, p.ChunkRefs = types.StringArray{
					remainingChunk.KnowledgeID,
				}, types.StringArray{
					remainingChunk.ID,
				}
				require.NoError(t, db.Save(p).Error)
				switch action {
				case "hard_delete":
					require.NoError(t, db.Unscoped().Where("id = ?", removedID).Delete(&types.Knowledge{}).Error)
				case "move_tenant":
					require.NoError(
						t,
						db.Model(&types.Knowledge{}).Where("id = ?", removedID).Update("tenant_id", 8).Error,
					)
				default:
					require.NoError(t, db.Where("id = ?", removedID).Delete(&types.Knowledge{}).Error)
				}
				var refreshed *types.LearningQuizView
				if action == "new_assessment" {
					refreshed = learningAnsweredQuiz(t, db, scope, p, remainingChunk, "new ")
				}
				if action == "recover" {
					// A healthy, earlier quiz must not starve the bounded cleanup scan.
					_, err := repo.SetEnabled(ctx, scope, false)
					require.NoError(t, err)
					_, err = repo.Recover(ctx, 1)
					require.NoError(t, err)
					var n int64
					require.NoError(t, db.Model(&types.LearningQuiz{}).Where("id = ?", q.ID).Count(&n).Error)
					require.Zero(t, n)
				}
				exported, err := repo.Export(ctx, scope, "")
				require.NoError(t, err)
				for _, old := range exported.Quizzes {
					require.NotEqual(t, q.ID, old.ID)
				}
				for _, old := range exported.Attempts {
					require.NotEqual(t, q.Questions[0].ID, old.Result.QuestionID)
				}
				want := 1
				if refreshed != nil {
					want++
				}
				require.Len(t, exported.Attempts, want)
				require.Len(t, exported.Nodes, want)
				for _, model := range []any{&types.LearningQuestion{}, &types.LearningAttempt{}} {
					var n int64
					require.NoError(t, db.Model(model).Where("quiz_id = ?", q.ID).Count(&n).Error)
					require.Zero(t, n)
				}
				var retained types.LearningQuiz
				require.NoError(t, db.First(&retained, "id = ?", healthy.ID).Error)
				require.NoError(t, db.First(&types.WikiPage{}, "id = ?", p.ID).Error)
				if refreshed != nil {
					var mastery types.LearningMastery
					require.NoError(t, db.First(&mastery, "page_id = ?", p.ID).Error)
					require.Equal(t, 1, mastery.Attempts)
				}
			})
		}
	}
}

func TestLearningEditedSourceKeepsHistoricalAnswers(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db, ctx := learningTestDB(t, dialect), context.Background()
			scope := interfaces.LearningScope{TenantID: 7, SubjectID: "web_user:alice"}
			p, c := learningSeed(t, db)
			q := learningAnsweredQuiz(t, db, scope, p, c, "")
			require.NoError(t, db.Model(c).Update("content", c.Content+" Revised.").Error)
			repo := NewLearningRepository(db)
			_, err := repo.Recover(ctx, 1)
			require.NoError(t, err)
			exported, err := repo.Export(ctx, scope, "")
			require.NoError(t, err)
			require.Len(t, exported.Attempts, 1)
			require.Len(t, exported.Quizzes, 1)
			require.Equal(t, q.ID, exported.Quizzes[0].ID)
			require.Equal(t, "stale", exported.Quizzes[0].Status)
		})
	}
}

func TestLearningPostgresDeletedSourceSerializesWithExport(t *testing.T) {
	db, ctx := learningTestDB(t, "postgres"), context.Background()
	scope := interfaces.LearningScope{TenantID: 7, SubjectID: "web_user:alice"}
	p, c := learningSeed(t, db)
	learningAnsweredQuiz(t, db, scope, p, c, "")
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	require.NoError(t, tx.Where("id = ?", c.KnowledgeID).Delete(&types.Knowledge{}).Error)
	type response struct {
		data *types.LearningExport
		err  error
	}
	done := make(chan response, 1)
	go func() {
		data, err := NewLearningRepository(db).Export(ctx, scope, "")
		done <- response{data, err}
	}()
	select {
	case <-done:
		t.Fatal("export bypassed the source deletion lock")
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, tx.Commit().Error)
	result := <-done
	require.NoError(t, result.err)
	raw, err := json.Marshal(result.data)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "PRIVATE_EXPLANATION")
	require.Empty(t, result.data.Attempts)
}

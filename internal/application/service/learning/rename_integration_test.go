package learning

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	wikiservice "github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestLearningRenameKeepsAssessmentMemoryAndPrivateQuizCard(t *testing.T) {
	f := newFixture(t)
	memory, _ := learningPersistentMemory(t, f)
	require.NoError(t, f.db.AutoMigrate(&types.WikiPageIssue{}))
	_, err := memory.Remember(f.ctx, types.MemoryItem{
		Kind: types.MemoryKindProfile, Content: "PERSONAL_MEMORY_SECRET", Origin: types.MemoryOriginExplicit,
	})
	require.NoError(t, err)
	for range types.MemoryDocAffinityMinHits {
		memory.RecordAnswerSources(f.ctx, []types.MemoryDocAffinity{{
			KnowledgeID: f.chunk.KnowledgeID, KnowledgeBaseID: f.kb.ID, Title: "Transaction source",
		}})
	}
	quiz := f.ready(t)
	answer := types.LearningAnswer{QuestionID: quiz.Questions[0].ID, OptionID: "0", AttemptID: "before-rename"}
	result, err := f.svc.SubmitAnswer(f.ctx, answer)
	require.NoError(t, err)
	require.Equal(t, 1, result.Mastery.Attempts)

	wiki := wikiservice.NewWikiPageService(repository.NewWikiPageRepository(f.db), nil, nil, nil, nil)
	renamed, err := wiki.(interfaces.WikiPageRenamer).RenamePage(f.ctx, interfaces.WikiPageRenameRequest{
		KnowledgeBaseID: f.kb.ID, PageID: f.page.ID, OldSlug: f.page.Slug, NewSlug: "concept/renamed",
	})
	require.NoError(t, err)
	require.Equal(t, f.page.ID, renamed.Page.ID)
	require.Equal(t, f.page.SourceRefs, renamed.Page.SourceRefs)
	require.Equal(t, f.page.ChunkRefs, renamed.Page.ChunkRefs)
	require.Equal(t, f.page.Version, renamed.Page.Version)
	node, err := f.svc.Node(f.ctx, f.page.ID)
	require.NoError(t, err)
	require.Equal(t, "concept/renamed", node.Slug)
	require.True(t, node.Familiar)
	require.Equal(t, result.Mastery, node.Mastery)
	quiz, err = f.svc.GetQuiz(f.ctx, quiz.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", quiz.Status)
	require.Equal(t, "concept/renamed", quiz.Slug)
	require.Equal(t, result, quiz.Questions[0].Result)
	replayed, err := f.svc.SubmitAnswer(f.ctx, answer)
	require.NoError(t, err)
	require.Equal(t, result, replayed)
	exported, err := f.svc.Export(f.ctx, f.kb.ID)
	require.NoError(t, err)
	require.Len(t, exported.Attempts, 1)
	require.Equal(t, result.QuestionID, exported.Attempts[0].Result.QuestionID)

	tool := tools.NewLearningTool(tools.ToolPrepareLearningQuiz, f.svc, wiki, []string{f.kb.ID}, nil)
	args, err := json.Marshal(map[string]string{"knowledge_base_id": f.kb.ID, "slug": node.Slug})
	require.NoError(t, err)
	card, err := tool.Execute(f.ctx, args)
	require.NoError(t, err)
	require.True(t, card.Success, card.Error)
	require.Equal(t, quiz.ID, card.Data["quiz_id"])
	for _, data := range []any{
		card,
		tools.SanitizeToolResultForClient(tool.Name(), card),
		tools.SanitizeAgentStepsForStorage([]types.AgentStep{
			{ToolCalls: []types.ToolCall{{Name: tool.Name(), Result: card}}},
		}),
	} {
		raw, err := json.Marshal(data)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "PRIVATE_EXPLANATION")
		require.NotContains(t, string(raw), "correct_option")
		require.NotContains(t, string(raw), "PERSONAL_MEMORY_SECRET")
	}
	require.Equal(t, 2, f.model.calls, "reusing the quiz must not invoke the model again")

	_, err = memory.Clear(f.ctx)
	require.NoError(t, err)
	node, err = f.svc.Node(f.ctx, f.page.ID)
	require.NoError(t, err)
	require.False(t, node.Familiar)
	require.Equal(t, result.Mastery, node.Mastery, "forgetting memory must not erase assessed practice")
}

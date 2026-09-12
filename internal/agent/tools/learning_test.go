package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func learningToolContext() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "alice"})
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})
}

type learningToolService struct {
	interfaces.LearningService
	calls     int
	memoryOff bool
}

func (s *learningToolService) Overview(ctx context.Context, kbID string) (*types.LearningOverview, error) {
	s.calls++
	s.memoryOff = !types.MemoryAllowedForAgent(ctx)
	return &types.LearningOverview{Enabled: true, KnowledgeBaseID: kbID}, nil
}

func (s *learningToolService) PrepareQuiz(context.Context, string) (*types.LearningQuizView, error) {
	s.calls++
	return &types.LearningQuizView{ID: "quiz", Status: "ready", Questions: []types.LearningQuestionView{
		{
			ID:       "q",
			Answered: true,
			Result:   &types.LearningAnswerResult{CorrectOption: "private-answer", Explanation: "private-explanation"},
		},
	}}, nil
}

type learningToolWiki struct{ interfaces.WikiPageService }

func (learningToolWiki) GetPageBySlug(context.Context, string, string) (*types.WikiPage, error) {
	return &types.WikiPage{ID: "page", Title: "RAG"}, nil
}

func TestLearningToolsDoNotDiscloseAnswersFromAnExistingQuiz(t *testing.T) {
	svc := &learningToolService{}
	tool := NewLearningTool(ToolPrepareLearningQuiz, svc, learningToolWiki{}, []string{"kb"}, nil)
	result, err := tool.Execute(
		learningToolContext(),
		json.RawMessage(`{"knowledge_base_id":"kb","slug":"concept/rag"}`),
	)
	require.NoError(t, err)
	require.True(t, result.Success)
	for _, data := range []any{
		result,
		SanitizeToolResultForClient(tool.Name(), result),
		SanitizeAgentStepsForStorage([]types.AgentStep{
			{ToolCalls: []types.ToolCall{{Name: tool.Name(), Result: result}}},
		}),
	} {
		encoded, err := json.Marshal(data)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "private-answer")
		require.NotContains(t, string(encoded), "private-explanation")
	}
	require.Equal(t, "quiz", result.Data["quiz_id"])
}

func TestLearningToolsDenyMachineAndBorrowedScopes(t *testing.T) {
	for _, kind := range []string{
		types.PrincipalAPITenant,
		types.PrincipalAPIPlatform,
		types.PrincipalAPIExternalUser,
		types.PrincipalIMUser,
		types.PrincipalEmbedVisitor,
	} {
		t.Run(kind, func(t *testing.T) {
			svc := &learningToolService{}
			tool := NewLearningTool(ToolGetLearningProfile, svc, nil, []string{"kb"}, nil)
			ctx := types.WithPrincipal(learningToolContext(), types.Principal{Type: kind, ID: "alice"})
			result, err := tool.Execute(ctx, json.RawMessage(`{"knowledge_base_id":"kb"}`))
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Zero(t, svc.calls)
		})
	}
	require.False(t, LearningWebCallerAllowed(types.WithExecutionTenant(learningToolContext(), 8)))
	require.False(t, LearningWebCallerAllowed(context.Background()))
	require.False(
		t,
		LearningWebCallerAllowed(
			types.WithPrincipal(learningToolContext(), types.Principal{Type: types.PrincipalWebUser, ID: "bob"}),
		),
	)
}

func TestLearningToolsRespectKBAndMemoryRestrictions(t *testing.T) {
	svc := &learningToolService{}
	off := false
	tool := NewLearningTool(ToolGetLearningProfile, svc, nil, []string{"kb"}, &off)
	denied, err := tool.Execute(learningToolContext(), json.RawMessage(`{"knowledge_base_id":"foreign"}`))
	require.NoError(t, err)
	require.False(t, denied.Success)
	require.Zero(t, svc.calls)
	ok, err := tool.Execute(learningToolContext(), json.RawMessage(`{"knowledge_base_id":"kb"}`))
	require.NoError(t, err)
	require.True(t, ok.Success)
	require.True(t, svc.memoryOff)
}

func TestLearningOnlyAcceptsWholeKnowledgeBaseTargets(t *testing.T) {
	targets := types.SearchTargets{
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "whole"},
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "tag", TagIDs: []string{"tag"}},
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "doc", KnowledgeIDs: []string{"doc"}},
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "shared"},
	}
	require.Equal(t, []string{"whole"}, LearningWholeKBIDs(targets, []string{"whole", "tag", "doc"}))
	require.False(t, CanRunConcurrently(ToolPrepareLearningQuiz))
	for _, name := range []string{ToolGetLearningProfile, ToolRecommendLearningTopics, ToolPrepareLearningQuiz} {
		tool := NewLearningTool(name, &learningToolService{}, nil, nil, nil)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(tool.Parameters(), &schema))
		require.Equal(t, false, schema["additionalProperties"])
		props := schema["properties"].(map[string]any)
		require.NotContains(t, props, "subject_id")
		require.NotContains(t, props, "tenant_id")
		require.NotContains(t, props, "correct_option")
		require.NotContains(t, props, "answer")
	}
}

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type feedbackRepositoryStub struct {
	interfaces.FeedbackRepository
	input           *types.ApplyMessageFeedbackInput
	resetCalls      int
	governanceCalls int
}

func (s *feedbackRepositoryStub) ApplyMessageFeedback(
	_ context.Context,
	input types.ApplyMessageFeedbackInput,
) (*types.MessageFeedbackState, error) {
	s.input = &input
	return &types.MessageFeedbackState{Type: input.Type, ReasonCode: input.ReasonCode}, nil
}

func (s *feedbackRepositoryStub) ResetChunkFeedback(
	_ context.Context,
	_ types.ResetChunkFeedbackInput,
) error {
	s.resetCalls++
	return nil
}

func (s *feedbackRepositoryStub) ListChunkFeedbackGovernance(
	_ context.Context,
	_ uint64,
	_ string,
	_ *types.ChunkFeedbackListQuery,
) ([]*types.ChunkFeedbackListItem, int64, error) {
	s.governanceCalls++
	return []*types.ChunkFeedbackListItem{{
		ChunkID: "chunk-1", LikeCount: 9, DislikeCount: 1, StoredRecallWeight: 0.8,
	}}, 1, nil
}

func (s *feedbackRepositoryStub) GetChunkFeedbackDetails(
	_ context.Context,
	_ uint64,
	chunkID string,
) (*types.ChunkFeedbackDetails, error) {
	s.governanceCalls++
	return &types.ChunkFeedbackDetails{
		ChunkID: chunkID, KnowledgeBaseID: "kb-1", LikeCount: 9, DislikeCount: 1,
		StoredRecallWeight: 0.8,
	}, nil
}

func (s *feedbackRepositoryStub) ListChunkFeedbackHistory(
	_ context.Context,
	_ uint64,
	_, _ string,
	_ *types.Pagination,
) ([]*types.ChunkFeedbackAudit, int64, error) {
	s.governanceCalls++
	return []*types.ChunkFeedbackAudit{{ID: 1}}, 1, nil
}

func feedbackServiceContext(principal types.Principal) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	return types.WithPrincipal(ctx, principal)
}

func TestFeedbackServiceRejectsNonWebPrincipals(t *testing.T) {
	for _, principalType := range []string{
		types.PrincipalAPITenant,
		types.PrincipalAPIPlatform,
		types.PrincipalAPIExternalUser,
		types.PrincipalIMUser,
		types.PrincipalEmbedVisitor,
	} {
		t.Run(principalType, func(t *testing.T) {
			repo := &feedbackRepositoryStub{}
			svc := NewFeedbackService(repo, &config.Config{Feedback: config.DefaultFeedbackConfig()})
			_, err := svc.ApplyMessageFeedback(
				feedbackServiceContext(types.Principal{Type: principalType, ID: "caller"}),
				"session", "message", types.FeedbackTypeLike, nil,
			)
			assert.ErrorIs(t, err, ErrFeedbackForbidden)
			assert.Nil(t, repo.input)
		})
	}
}

func TestFeedbackServiceUsesAuthenticatedWebActor(t *testing.T) {
	repo := &feedbackRepositoryStub{}
	svc := NewFeedbackService(repo, &config.Config{Feedback: config.DefaultFeedbackConfig()})
	state, err := svc.ApplyMessageFeedback(
		feedbackServiceContext(types.Principal{Type: types.PrincipalWebUser, ID: "user-1"}),
		"session-1", "message-1", types.FeedbackTypeDislike,
		func() *types.FeedbackReasonCode {
			reason := types.FeedbackReasonInaccurate
			return &reason
		}(),
	)
	require.NoError(t, err)
	assert.Equal(t, types.FeedbackTypeDislike, state.Type)
	require.NotNil(t, repo.input)
	assert.Equal(t, uint64(7), repo.input.MessageTenantID)
	assert.Equal(t, "user-1", repo.input.ActorUserID)
	assert.Equal(t, "session-1", repo.input.SessionID)
	assert.Equal(t, "message-1", repo.input.MessageID)
}

func TestFeedbackServiceRejectsInvalidReasonCombination(t *testing.T) {
	valid := types.FeedbackReasonInaccurate
	empty := types.FeedbackReasonCode("")
	invalid := types.FeedbackReasonCode("invented")
	for _, testCase := range []struct {
		name           string
		feedbackType   types.FeedbackType
		reason         *types.FeedbackReasonCode
		expectAccepted bool
	}{
		{name: "dislike valid", feedbackType: types.FeedbackTypeDislike, reason: &valid, expectAccepted: true},
		{name: "dislike nil", feedbackType: types.FeedbackTypeDislike},
		{name: "dislike empty", feedbackType: types.FeedbackTypeDislike, reason: &empty},
		{name: "dislike invalid", feedbackType: types.FeedbackTypeDislike, reason: &invalid},
		{name: "like no reason", feedbackType: types.FeedbackTypeLike, expectAccepted: true},
		{name: "like with reason", feedbackType: types.FeedbackTypeLike, reason: &valid},
		{name: "none no reason", feedbackType: types.FeedbackTypeNone, expectAccepted: true},
		{name: "none with reason", feedbackType: types.FeedbackTypeNone, reason: &valid},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &feedbackRepositoryStub{}
			svc := NewFeedbackService(repo, &config.Config{Feedback: config.DefaultFeedbackConfig()})
			_, err := svc.ApplyMessageFeedback(
				feedbackServiceContext(types.Principal{Type: types.PrincipalWebUser, ID: "user-1"}),
				"session", "message", testCase.feedbackType, testCase.reason,
			)
			if testCase.expectAccepted {
				require.NoError(t, err)
				require.NotNil(t, repo.input)
				assert.Equal(t, testCase.reason, repo.input.ReasonCode)
				return
			}
			assert.ErrorIs(t, err, ErrInvalidFeedback)
			assert.Nil(t, repo.input)
		})
	}
}

func TestFeedbackServiceCollectionSwitchClosesWritesAndGovernance(t *testing.T) {
	policy := config.DefaultFeedbackConfig()
	policy.Enabled = false
	repo := &feedbackRepositoryStub{}
	svc := NewFeedbackService(repo, &config.Config{Feedback: policy})
	ctx := feedbackServiceContext(types.Principal{Type: types.PrincipalWebUser, ID: "user-1"})

	_, err := svc.ApplyMessageFeedback(ctx, "session", "message", types.FeedbackTypeLike, nil)
	assert.ErrorIs(t, err, ErrFeedbackDisabled)
	assert.ErrorIs(t, svc.ResetChunkFeedback(ctx, "kb-1", "chunk-1"), ErrFeedbackDisabled)
	_, err = svc.ListChunkFeedback(ctx, "kb-1", &types.ChunkFeedbackListQuery{})
	assert.ErrorIs(t, err, ErrFeedbackDisabled)
	_, err = svc.GetChunkFeedbackGovernanceDetails(ctx, "kb-1", "chunk-1")
	assert.ErrorIs(t, err, ErrFeedbackDisabled)
	_, err = svc.ListChunkFeedbackHistory(ctx, "kb-1", "chunk-1", nil)
	assert.ErrorIs(t, err, ErrFeedbackDisabled)
	assert.Nil(t, repo.input)
	assert.Zero(t, repo.resetCalls)
	assert.Zero(t, repo.governanceCalls)
}

func TestFeedbackGovernanceUsesCurrentEffectivePolicyAndRejectsAPIKeys(t *testing.T) {
	policy := config.DefaultFeedbackConfig()
	policy.MinimumSampleCount = 5
	repo := &feedbackRepositoryStub{}
	svc := NewFeedbackService(repo, &config.Config{Feedback: policy})
	webCtx := feedbackServiceContext(types.Principal{Type: types.PrincipalWebUser, ID: "user-1"})

	result, err := svc.ListChunkFeedback(webCtx, "kb-1", &types.ChunkFeedbackListQuery{})
	require.NoError(t, err)
	items, ok := result.Data.([]*types.ChunkFeedbackListItem)
	require.True(t, ok)
	require.Len(t, items, 1)
	assert.Equal(t, 0.8, items[0].StoredRecallWeight)
	assert.Equal(t, policy.HighRecallWeight, items[0].EffectiveRecallWeight)

	apiCtx := feedbackServiceContext(types.Principal{Type: types.PrincipalAPITenant, ID: "api-key"})
	_, err = svc.ListChunkFeedback(apiCtx, "kb-1", &types.ChunkFeedbackListQuery{})
	assert.ErrorIs(t, err, ErrFeedbackForbidden)
	_, err = svc.GetChunkFeedbackGovernanceDetails(apiCtx, "kb-1", "chunk-1")
	assert.ErrorIs(t, err, ErrFeedbackForbidden)
	_, err = svc.ListChunkFeedbackHistory(apiCtx, "kb-1", "chunk-1", nil)
	assert.ErrorIs(t, err, ErrFeedbackForbidden)
}

func TestFeedbackGovernanceRejectsChunkOutsideURLKnowledgeBase(t *testing.T) {
	repo := &feedbackRepositoryStub{}
	svc := NewFeedbackService(repo, &config.Config{Feedback: config.DefaultFeedbackConfig()})
	ctx := feedbackServiceContext(types.Principal{Type: types.PrincipalWebUser, ID: "user-1"})

	_, err := svc.GetChunkFeedbackGovernanceDetails(ctx, "kb-other", "chunk-1")
	assert.ErrorIs(t, err, repository.ErrFeedbackChunkNotFound)
}

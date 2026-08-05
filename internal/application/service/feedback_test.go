package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type feedbackRepositoryStub struct {
	interfaces.FeedbackRepository
	input       *types.ApplyMessageFeedbackInput
	resetInput  *types.ResetChunkFeedbackInput
	detailCalls int
}

func (s *feedbackRepositoryStub) ApplyMessageFeedback(
	_ context.Context,
	input types.ApplyMessageFeedbackInput,
) (*types.MessageFeedbackState, error) {
	s.input = &input
	return &types.MessageFeedbackState{Type: input.Type, ReasonCode: input.ReasonCode}, nil
}

func (s *feedbackRepositoryStub) ResetChunkFeedback(
	_ context.Context, input types.ResetChunkFeedbackInput,
) error {
	s.resetInput = &input
	return nil
}

func (s *feedbackRepositoryStub) GetChunkFeedbackDetails(
	_ context.Context, _ uint64, _ string,
) (*types.ChunkFeedbackDetails, error) {
	s.detailCalls++
	return &types.ChunkFeedbackDetails{}, nil
}

func feedbackServiceContext(principal types.Principal) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 7})
	return types.WithPrincipal(ctx, principal)
}

func feedbackGovernanceContext(role types.TenantRole, principalType string) context.Context {
	ctx := feedbackServiceContext(types.Principal{Type: principalType, ID: "caller"})
	return context.WithValue(ctx, types.TenantRoleContextKey, role)
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
			svc := NewFeedbackService(repo)
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
	svc := NewFeedbackService(repo)
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
			svc := NewFeedbackService(repo)
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

func TestFeedbackGovernanceRequiresWebAdminOrOwner(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		role          types.TenantRole
		principalType string
		allowed       bool
	}{
		{name: "viewer", role: types.TenantRoleViewer, principalType: types.PrincipalWebUser},
		{name: "contributor", role: types.TenantRoleContributor, principalType: types.PrincipalWebUser},
		{name: "api key", role: types.TenantRoleOwner, principalType: types.PrincipalAPITenant},
		{name: "admin", role: types.TenantRoleAdmin, principalType: types.PrincipalWebUser, allowed: true},
		{name: "owner", role: types.TenantRoleOwner, principalType: types.PrincipalWebUser, allowed: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &feedbackRepositoryStub{}
			svc := NewFeedbackService(repo)
			ctx := feedbackGovernanceContext(testCase.role, testCase.principalType)

			_, detailErr := svc.GetChunkFeedbackDetails(ctx, "chunk")
			resetErr := svc.ResetChunkFeedback(ctx, "kb", "chunk")
			if testCase.allowed {
				require.NoError(t, detailErr)
				require.NoError(t, resetErr)
				assert.Equal(t, 1, repo.detailCalls)
				require.NotNil(t, repo.resetInput)
				return
			}
			assert.ErrorIs(t, detailErr, ErrFeedbackForbidden)
			assert.ErrorIs(t, resetErr, ErrFeedbackForbidden)
			assert.Zero(t, repo.detailCalls)
			assert.Nil(t, repo.resetInput)
		})
	}
}

func TestFeedbackGovernanceRejectsCrossTenantSharedAccessWithoutSideEffects(t *testing.T) {
	newCrossTenantContext := func() context.Context {
		// TenantInfo is the authenticated actor tenant (A). TenantID models the
		// source tenant (B) after KBAccess resolved a shared KB/chunk.
		ctx := feedbackGovernanceContext(types.TenantRoleAdmin, types.PrincipalWebUser)
		return context.WithValue(ctx, types.TenantIDContextKey, uint64(8))
	}

	t.Run("shared viewer cannot read governance details", func(t *testing.T) {
		repo := &feedbackRepositoryStub{}
		_, err := NewFeedbackService(repo).GetChunkFeedbackDetails(newCrossTenantContext(), "shared-viewer-chunk")
		assert.ErrorIs(t, err, ErrFeedbackForbidden)
		assert.Zero(t, repo.detailCalls, "cross-tenant detail must not reach the repository")
	})

	t.Run("shared editor cannot reset feedback", func(t *testing.T) {
		repo := &feedbackRepositoryStub{}
		err := NewFeedbackService(repo).ResetChunkFeedback(newCrossTenantContext(), "shared-editor-kb", "shared-editor-chunk")
		assert.ErrorIs(t, err, ErrFeedbackForbidden)
		assert.Nil(t, repo.resetInput, "cross-tenant reset must not reach the repository")
	})
}

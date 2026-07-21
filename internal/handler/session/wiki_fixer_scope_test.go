package session

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type wikiFixerKBLookupStub struct {
	kb         *types.KnowledgeBase
	err        error
	calledWith string
}

func (s *wikiFixerKBLookupStub) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
	s.calledWith = id
	return s.kb, s.err
}

type wikiFixerKBShareStub struct {
	permission        types.OrgMemberRole
	isShared          bool
	err               error
	checkedKBID       string
	checkedTenantID   uint64
	checkedTenantRole types.TenantRole
}

func (s *wikiFixerKBShareStub) CheckTenantKBPermission(
	_ context.Context,
	kbID string,
	callerTenantID uint64,
	callerTenantRole types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	s.checkedKBID = kbID
	s.checkedTenantID = callerTenantID
	s.checkedTenantRole = callerTenantRole
	return s.permission, s.isShared, s.err
}

func wikiFixerKnowledgeBase(id string, tenantID uint64, creatorID string) *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:        id,
		TenantID:  tenantID,
		CreatorID: creatorID,
		IndexingStrategy: types.IndexingStrategy{
			WikiEnabled: true,
		},
	}
}

func enforcedWikiFixerConfig() *config.Config {
	enabled := true
	return &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
}

func wikiFixerUserContext(userID string, role types.TenantRole) context.Context {
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, userID)
	return context.WithValue(ctx, types.TenantRoleContextKey, role)
}

func requireWikiFixerErrorCode(t *testing.T, err error, code apperrors.ErrorCode) {
	t.Helper()
	require.Error(t, err)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, code, appErr.Code)
}

func TestResolveBuiltinWikiFixerTenantScope_SharedEditorUsesSourceTenant(t *testing.T) {
	agent := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 10, Name: "Wiki Fixer"}
	kbLookup := &wikiFixerKBLookupStub{kb: wikiFixerKnowledgeBase("kb-shared", 20, "source-owner")}
	kbShare := &wikiFixerKBShareStub{permission: types.OrgRoleEditor, isShared: true}

	gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
		context.Background(), enforcedWikiFixerConfig(), agent, 10, types.TenantRoleContributor,
		[]string{"kb-shared"}, kbLookup, kbShare,
	)

	require.NoError(t, err)
	require.NotSame(t, agent, gotAgent)
	require.Equal(t, uint64(20), gotAgent.TenantID)
	require.Equal(t, uint64(20), effectiveTenantID)
	require.Equal(t, uint64(10), agent.TenantID, "must not mutate the cached built-in agent")
	require.Equal(t, "kb-shared", kbLookup.calledWith)
	require.Equal(t, "kb-shared", kbShare.checkedKBID)
	require.Equal(t, uint64(10), kbShare.checkedTenantID)
	require.Equal(t, types.TenantRoleContributor, kbShare.checkedTenantRole)
}

func TestResolveBuiltinWikiFixerTenantScope_RejectsSharedViewer(t *testing.T) {
	agent := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 10}
	kbLookup := &wikiFixerKBLookupStub{kb: wikiFixerKnowledgeBase("kb-shared", 20, "source-owner")}
	kbShare := &wikiFixerKBShareStub{permission: types.OrgRoleViewer, isShared: true}

	gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
		context.Background(), enforcedWikiFixerConfig(), agent, 10, types.TenantRoleContributor,
		[]string{"kb-shared"}, kbLookup, kbShare,
	)

	require.Same(t, agent, gotAgent)
	require.Zero(t, effectiveTenantID)
	requireWikiFixerErrorCode(t, err, apperrors.ErrForbidden)
}

func TestResolveBuiltinWikiFixerTenantScope_EnforcesOwnKBWriteMatrix(t *testing.T) {
	agent := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 10}
	cases := []struct {
		name     string
		ctx      context.Context
		wantCode apperrors.ErrorCode
	}{
		{
			name: "creator",
			ctx:  wikiFixerUserContext("creator", types.TenantRoleContributor),
		},
		{
			name: "admin",
			ctx:  wikiFixerUserContext("other-user", types.TenantRoleAdmin),
		},
		{
			name:     "non-owner contributor",
			ctx:      wikiFixerUserContext("other-user", types.TenantRoleContributor),
			wantCode: apperrors.ErrForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
				tc.ctx, enforcedWikiFixerConfig(), agent, 10, types.TenantRoleContributor,
				[]string{"kb-own"}, &wikiFixerKBLookupStub{kb: wikiFixerKnowledgeBase("kb-own", 10, "creator")}, nil,
			)

			if tc.wantCode != 0 {
				require.Same(t, agent, gotAgent)
				require.Zero(t, effectiveTenantID)
				requireWikiFixerErrorCode(t, err, tc.wantCode)
				return
			}
			require.NoError(t, err)
			require.Same(t, agent, gotAgent)
			require.Zero(t, effectiveTenantID)
		})
	}
}

func TestResolveBuiltinWikiFixerTenantScope_RequiresWikiEnabledSingleKB(t *testing.T) {
	agent := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 10}

	t.Run("multiple knowledge bases", func(t *testing.T) {
		lookup := &wikiFixerKBLookupStub{kb: wikiFixerKnowledgeBase("kb-a", 10, "creator")}
		gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
			wikiFixerUserContext("creator", types.TenantRoleContributor), enforcedWikiFixerConfig(), agent, 10,
			types.TenantRoleContributor, []string{"kb-a", "kb-b"}, lookup, nil,
		)

		require.Same(t, agent, gotAgent)
		require.Zero(t, effectiveTenantID)
		require.Empty(t, lookup.calledWith)
		requireWikiFixerErrorCode(t, err, apperrors.ErrBadRequest)
	})

	t.Run("non wiki knowledge base", func(t *testing.T) {
		kb := wikiFixerKnowledgeBase("kb-document", 10, "creator")
		kb.IndexingStrategy.WikiEnabled = false
		gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
			wikiFixerUserContext("creator", types.TenantRoleContributor), enforcedWikiFixerConfig(), agent, 10,
			types.TenantRoleContributor, []string{"kb-document"}, &wikiFixerKBLookupStub{kb: kb}, nil,
		)

		require.Same(t, agent, gotAgent)
		require.Zero(t, effectiveTenantID)
		requireWikiFixerErrorCode(t, err, apperrors.ErrBadRequest)
	})
}

func TestResolveBuiltinWikiFixerTenantScope_RequiresAPIKeyIngestCapability(t *testing.T) {
	agent := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 10}
	kb := wikiFixerKnowledgeBase("kb-own", 10, "creator")

	t.Run("chat capability alone is denied", func(t *testing.T) {
		ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
			Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)},
		})
		gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
			ctx, enforcedWikiFixerConfig(), agent, 10, types.TenantRoleViewer,
			[]string{"kb-own"}, &wikiFixerKBLookupStub{kb: kb}, nil,
		)

		require.Same(t, agent, gotAgent)
		require.Zero(t, effectiveTenantID)
		requireWikiFixerErrorCode(t, err, apperrors.ErrForbidden)
	})

	t.Run("chat and ingest capabilities are allowed", func(t *testing.T) {
		ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
			Capabilities: types.StringArray{
				string(types.APIKeyCapabilityChat),
				string(types.APIKeyCapabilityIngest),
			},
		})
		gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
			ctx, enforcedWikiFixerConfig(), agent, 10, types.TenantRoleViewer,
			[]string{"kb-own"}, &wikiFixerKBLookupStub{kb: kb}, nil,
		)

		require.NoError(t, err)
		require.Same(t, agent, gotAgent)
		require.Zero(t, effectiveTenantID)
	})
}

func TestResolveBuiltinWikiFixerTenantScope_FailsClosedOnLookupAndPermissionErrors(t *testing.T) {
	agent := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 10}

	t.Run("knowledge base lookup error", func(t *testing.T) {
		gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
			context.Background(), enforcedWikiFixerConfig(), agent, 10, types.TenantRoleContributor,
			[]string{"kb-shared"}, &wikiFixerKBLookupStub{err: errors.New("lookup failed")}, &wikiFixerKBShareStub{},
		)

		require.Same(t, agent, gotAgent)
		require.Zero(t, effectiveTenantID)
		requireWikiFixerErrorCode(t, err, apperrors.ErrServiceUnavailable)
	})

	t.Run("knowledge base not found", func(t *testing.T) {
		gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
			context.Background(), enforcedWikiFixerConfig(), agent, 10, types.TenantRoleContributor,
			[]string{"missing"}, &wikiFixerKBLookupStub{err: repository.ErrKnowledgeBaseNotFound}, &wikiFixerKBShareStub{},
		)

		require.Same(t, agent, gotAgent)
		require.Zero(t, effectiveTenantID)
		requireWikiFixerErrorCode(t, err, apperrors.ErrNotFound)
	})

	t.Run("shared permission check error", func(t *testing.T) {
		gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
			context.Background(), enforcedWikiFixerConfig(), agent, 10, types.TenantRoleContributor,
			[]string{"kb-shared"}, &wikiFixerKBLookupStub{kb: wikiFixerKnowledgeBase("kb-shared", 20, "source-owner")},
			&wikiFixerKBShareStub{err: errors.New("permission failed")},
		)

		require.Same(t, agent, gotAgent)
		require.Zero(t, effectiveTenantID)
		requireWikiFixerErrorCode(t, err, apperrors.ErrServiceUnavailable)
	})
}

func TestResolveBuiltinWikiFixerTenantScope_IgnoresNonWikiFixerAgents(t *testing.T) {
	agent := &types.CustomAgent{ID: "custom-agent", TenantID: 10}
	lookup := &wikiFixerKBLookupStub{kb: wikiFixerKnowledgeBase("kb-shared", 20, "source-owner")}

	gotAgent, effectiveTenantID, err := resolveBuiltinWikiFixerTenantScope(
		context.Background(), enforcedWikiFixerConfig(), agent, 10, types.TenantRoleContributor,
		[]string{"kb-shared"}, lookup, &wikiFixerKBShareStub{permission: types.OrgRoleEditor, isShared: true},
	)

	require.NoError(t, err)
	require.Same(t, agent, gotAgent)
	require.Zero(t, effectiveTenantID)
	require.Empty(t, lookup.calledWith)
}

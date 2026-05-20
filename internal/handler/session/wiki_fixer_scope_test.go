package session

import (
	"context"
	"errors"
	"testing"

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

func TestResolveBuiltinWikiFixerTenantScope_SharedEditorUsesSourceTenant(t *testing.T) {
	agent := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 10, Name: "Wiki Fixer"}
	kbLookup := &wikiFixerKBLookupStub{
		kb: &types.KnowledgeBase{ID: "kb-shared", TenantID: 20, Name: "Shared KB"},
	}
	kbShare := &wikiFixerKBShareStub{
		permission: types.OrgRoleEditor,
		isShared:   true,
	}

	gotAgent, effectiveTenantID := resolveBuiltinWikiFixerTenantScope(
		context.Background(),
		agent,
		10,
		types.TenantRoleContributor,
		[]string{"kb-shared"},
		kbLookup,
		kbShare,
	)

	require.NotSame(t, agent, gotAgent)
	require.Equal(t, uint64(20), gotAgent.TenantID)
	require.Equal(t, uint64(20), effectiveTenantID)
	require.Equal(t, uint64(10), agent.TenantID, "must not mutate the cached built-in agent")
	require.Equal(t, "kb-shared", kbLookup.calledWith)
	require.Equal(t, "kb-shared", kbShare.checkedKBID)
	require.Equal(t, uint64(10), kbShare.checkedTenantID)
	require.Equal(t, types.TenantRoleContributor, kbShare.checkedTenantRole)
}

func TestResolveBuiltinWikiFixerTenantScope_SharedViewerDoesNotSwitchTenant(t *testing.T) {
	agent := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 10}
	kbLookup := &wikiFixerKBLookupStub{
		kb: &types.KnowledgeBase{ID: "kb-shared", TenantID: 20},
	}
	kbShare := &wikiFixerKBShareStub{
		permission: types.OrgRoleViewer,
		isShared:   true,
	}

	gotAgent, effectiveTenantID := resolveBuiltinWikiFixerTenantScope(
		context.Background(),
		agent,
		10,
		types.TenantRoleContributor,
		[]string{"kb-shared"},
		kbLookup,
		kbShare,
	)

	require.Same(t, agent, gotAgent)
	require.Zero(t, effectiveTenantID)
	require.Equal(t, uint64(10), gotAgent.TenantID)
}

func TestResolveBuiltinWikiFixerTenantScope_IgnoresNonWikiFixerAgents(t *testing.T) {
	agent := &types.CustomAgent{ID: "custom-agent", TenantID: 10}
	kbLookup := &wikiFixerKBLookupStub{
		kb: &types.KnowledgeBase{ID: "kb-shared", TenantID: 20},
	}
	kbShare := &wikiFixerKBShareStub{
		permission: types.OrgRoleEditor,
		isShared:   true,
	}

	gotAgent, effectiveTenantID := resolveBuiltinWikiFixerTenantScope(
		context.Background(),
		agent,
		10,
		types.TenantRoleContributor,
		[]string{"kb-shared"},
		kbLookup,
		kbShare,
	)

	require.Same(t, agent, gotAgent)
	require.Zero(t, effectiveTenantID)
	require.Empty(t, kbLookup.calledWith)
}

func TestResolveBuiltinWikiFixerTenantScope_RequiresSingleKnowledgeBase(t *testing.T) {
	agent := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 10}
	kbLookup := &wikiFixerKBLookupStub{
		kb: &types.KnowledgeBase{ID: "kb-shared", TenantID: 20},
	}

	gotAgent, effectiveTenantID := resolveBuiltinWikiFixerTenantScope(
		context.Background(),
		agent,
		10,
		types.TenantRoleContributor,
		[]string{"kb-a", "kb-b"},
		kbLookup,
		&wikiFixerKBShareStub{permission: types.OrgRoleEditor, isShared: true},
	)

	require.Same(t, agent, gotAgent)
	require.Zero(t, effectiveTenantID)
	require.Empty(t, kbLookup.calledWith)
}

func TestResolveBuiltinWikiFixerTenantScope_FallsBackOnLookupOrPermissionErrors(t *testing.T) {
	agent := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 10}

	t.Run("kb lookup error", func(t *testing.T) {
		gotAgent, effectiveTenantID := resolveBuiltinWikiFixerTenantScope(
			context.Background(),
			agent,
			10,
			types.TenantRoleContributor,
			[]string{"kb-shared"},
			&wikiFixerKBLookupStub{err: errors.New("lookup failed")},
			&wikiFixerKBShareStub{permission: types.OrgRoleEditor, isShared: true},
		)

		require.Same(t, agent, gotAgent)
		require.Zero(t, effectiveTenantID)
	})

	t.Run("permission check error", func(t *testing.T) {
		gotAgent, effectiveTenantID := resolveBuiltinWikiFixerTenantScope(
			context.Background(),
			agent,
			10,
			types.TenantRoleContributor,
			[]string{"kb-shared"},
			&wikiFixerKBLookupStub{kb: &types.KnowledgeBase{ID: "kb-shared", TenantID: 20}},
			&wikiFixerKBShareStub{err: errors.New("permission failed")},
		)

		require.Same(t, agent, gotAgent)
		require.Zero(t, effectiveTenantID)
	})
}

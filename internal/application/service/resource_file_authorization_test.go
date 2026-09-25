package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type catalogFileShares struct{}

func (catalogFileShares) CheckTenantKBPermission(
	_ context.Context,
	kb string,
	_ uint64,
	_ types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return types.OrgRoleViewer, kb == "shared", nil
}

func (catalogFileShares) GetKnowledgeBasesByIDsOnly(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{{ID: "shared", TenantID: 7}}, nil
}

type catalogFileAgent struct{ agent *types.CustomAgent }

func (a *catalogFileAgent) GetSharedAgentForTenant(
	context.Context,
	uint64,
	types.TenantRole,
	string,
	...uint64,
) (*types.CustomAgent, error) {
	return a.agent, nil
}

func TestCatalogFileAuthorizationRejectsPrivateHandlesInSharedText(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}, &types.Knowledge{}, &types.Chunk{}, &types.WikiPage{}))
	ctx := newSharedAccessContext()
	const physical = "local://7/exports/private.png"
	ref, err := catalog.Register(ctx, 7, physical, interfaces.ResourceRegistration{})
	require.NoError(t, err)
	for _, kb := range []string{"private", "shared"} {
		require.NoError(t, db.Create(&types.KnowledgeBase{ID: kb, TenantID: 7}).Error)
		require.NoError(
			t,
			db.Create(&types.Knowledge{ID: kb + "-doc", TenantID: 7, KnowledgeBaseID: kb, Type: "file"}).Error,
		)
	}
	require.NoError(
		t,
		catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, "private-doc", types.ResourceRelationSourceFile),
	)
	text := "![private](" + ref + ") ![legacy](" + physical + ")"
	require.NoError(
		t,
		db.Create(
			&types.Chunk{ID: "chunk", TenantID: 7, KnowledgeBaseID: "shared", KnowledgeID: "shared-doc", Content: text},
		).Error,
	)
	require.NoError(
		t,
		db.Create(&types.WikiPage{ID: "wiki", TenantID: 7, KnowledgeBaseID: "shared", Content: text}).Error,
	)
	grant := &access.KBAccess{
		KnowledgeBase:     &types.KnowledgeBase{ID: "shared", TenantID: 7},
		Caller:            types.CallerFromContext(ctx),
		EffectiveTenantID: 7,
		Permission:        types.OrgRoleViewer,
	}
	lookup := catalog.(interfaces.KBResourceLookup)
	message := &types.Message{
		ID:                  "message",
		AgentTenantID:       1,
		Content:             text,
		KnowledgeReferences: []*types.SearchResult{{KnowledgeBaseID: "shared", Content: text}},
	}
	shares := access.MessageKBShareAuthorizer{ShareGuard: catalogFileShares{}, KBs: catalogFileShares{}}
	check := func(allowed bool) {
		t.Helper()
		for _, path := range []string{ref, physical} {
			_, err := access.ResolveKBFile(ctx, grant, "shared", path, catalog, lookup)
			if allowed {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, access.ErrForbidden)
			}
			_, err = access.AuthorizeMessageFile(ctx, message, path, nil, catalog, shares)
			if allowed && path == ref {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, access.ErrForbidden)
			}
		}
	}
	check(false)
	require.NoError(
		t,
		catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, "shared-doc", types.ResourceRelationSourceFile),
	)
	check(true)
	require.NoError(t, db.Where("id = ?", "shared").Delete(&types.KnowledgeBase{}).Error)
	check(false)
}

func TestCatalogSharedAgentFileRequiresCurrentSelectionOrExactArtifactBinding(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}, &types.Knowledge{}))
	ctx := newSharedAccessContext()
	const physical = "local://7/exports/source.pdf"
	ref, err := catalog.Register(ctx, 7, physical, interfaces.ResourceRegistration{})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.KnowledgeBase{ID: "private", TenantID: 7}).Error)
	doc := &types.Knowledge{ID: "doc", TenantID: 7, KnowledgeBaseID: "private", Type: "file"}
	require.NoError(t, db.Create(doc).Error)
	require.NoError(t, catalog.Bind(ctx, ref, types.ResourceOwnerKnowledge, "doc", types.ResourceRelationSourceFile))
	agents := &catalogFileAgent{agent: &types.CustomAgent{ID: "agent", TenantID: 7}}
	message := &types.Message{
		ID:            "message",
		AgentID:       "agent",
		AgentTenantID: 7,
		Content:       ref + " " + physical,
		Artifacts:     types.MessageArtifacts{{URL: ref}},
	}
	check := func(allowed bool) {
		t.Helper()
		for _, path := range []string{ref, physical} {
			_, err := access.AuthorizeMessageFile(
				ctx,
				message,
				path,
				agents,
				catalog,
				access.MessageKBShareAuthorizer{},
			)
			if allowed {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, access.ErrForbidden)
			}
		}
	}
	for _, mode := range []string{"none", "", "unknown", "selected", "all"} {
		agents.agent.Config = types.CustomAgentConfig{KBSelectionMode: mode, KnowledgeBases: []string{"private"}}
		check(mode == "selected" || mode == "all")
	}
	agents.agent.Config = types.CustomAgentConfig{KBSelectionMode: "selected", KnowledgeBases: []string{"other"}}
	check(false)
	agents.agent.Config.KnowledgeBases = []string{"private"}
	scoped := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"other"}})
	_, err = access.AuthorizeMessageFile(scoped, message, ref, agents, catalog, access.MessageKBShareAuthorizer{})
	require.ErrorIs(t, err, access.ErrForbidden)
	require.NoError(t, db.Delete(doc).Error)
	check(false)
	agents.agent.Config.KBSelectionMode = "none"
	require.NoError(
		t,
		catalog.Bind(ctx, ref, types.ResourceOwnerMessage, "another-message", types.ResourceRelationArtifact),
	)
	check(false)
	require.NoError(t, catalog.Bind(ctx, ref, types.ResourceOwnerMessage, "message", types.ResourceRelationArtifact))
	check(true)
	agents.agent = nil
	check(false)
}

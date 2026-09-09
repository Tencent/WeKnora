package access

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type fileCatalog struct{ resource *types.StoredResource }

func (c fileCatalog) ResolvePath(_ context.Context, ref string) (string, *types.StoredResource, error) {
	if c.resource == nil {
		return ref, nil, nil
	}
	return c.resource.PhysicalPath, c.resource, nil
}

type fileBinding struct {
	allowed bool
	err     error
	kb      string
	tenant  uint64
}

func (b *fileBinding) IsReferencedByKnowledgeBase(_ context.Context, tenant uint64, kb, _ string) (bool, error) {
	b.kb, b.tenant = kb, tenant
	return b.allowed, b.err
}

type fileMessages struct {
	message *types.Message
	tenant  uint64
}

func (m *fileMessages) GetMessage(ctx context.Context, _, _ string) (*types.Message, error) {
	m.tenant = types.MustTenantIDFromContext(ctx)
	return m.message, nil
}

func TestKBFilesRequireExactGrantAndBinding(t *testing.T) {
	base := callerContext()
	grant := &KBAccess{
		Caller:            types.CallerFromContext(base),
		KnowledgeBase:     &types.KnowledgeBase{ID: "kb", TenantID: 2},
		EffectiveTenantID: 2,
		Permission:        types.OrgRoleViewer,
	}
	ctx := grant.Context(base)
	bindings := &fileBinding{allowed: true}
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	catalog := fileCatalog{
		&types.StoredResource{TenantID: 2, PhysicalPath: "local://2/exports/a.png", OriginalName: "a.png"},
	}
	file, err := ResolveKBFile(ctx, grant, "kb", ref, catalog, bindings)
	require.NoError(t, err)
	require.Equal(t, uint64(2), file.OwnerTenantID)
	require.Equal(t, "kb", bindings.kb)
	require.Equal(t, uint64(2), bindings.tenant)
	bindings.allowed = false
	_, err = ResolveKBFile(ctx, grant, "kb", ref, catalog, bindings)
	require.ErrorIs(t, err, ErrForbidden, "a same-tenant file still needs an exact KB binding")
	bindings.allowed = true
	_, err = ResolveKBFile(ctx, grant, "another", ref, catalog, bindings)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = ResolveKBFile(ctx, nil, "kb", ref, catalog, bindings)
	require.ErrorIs(t, err, ErrForbidden, "execution tenant alone is not a grant")
	bindings.err = errors.New("database unavailable")
	_, err = ResolveKBFile(ctx, grant, "kb", ref, catalog, bindings)
	require.ErrorIs(t, err, bindings.err)
}

func TestMessageFilesRequireReferenceAndRecheckRevocation(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	messages := &fileMessages{
		message: &types.Message{ID: "message", AgentID: "agent", AgentTenantID: 2, Role: "assistant"},
	}
	agents := &agentLookup{agent: &types.CustomAgent{ID: "agent", TenantID: 2}}
	catalog := artifactFileCatalog{
		fileCatalog{&types.StoredResource{TenantID: 2, PhysicalPath: "local://2/exports/a.png"}},
	}
	ctx := types.WithExecutionTenant(callerContext(), 2)
	_, err := ResolveMessageFile(ctx, "session", "message", ref, messages, agents, catalog, MessageKBShareAuthorizer{})
	require.ErrorIs(t, err, ErrForbidden)
	messages.message.Content = "![image](" + ref + "x)"
	_, err = ResolveMessageFile(ctx, "session", "message", ref, messages, agents, catalog, MessageKBShareAuthorizer{})
	require.ErrorIs(t, err, ErrForbidden, "a longer handle is not the requested handle")
	messages.message.Content = "![image](" + ref + ")"
	_, err = ResolveMessageFile(ctx, "session", "message", ref, messages, agents, catalog, MessageKBShareAuthorizer{})
	require.NoError(t, err)
	require.Equal(t, uint64(1), messages.tenant, "session lookup must use the caller")
	agents.agent = nil
	_, err = ResolveMessageFile(ctx, "session", "message", ref, messages, agents, catalog, MessageKBShareAuthorizer{})
	require.ErrorIs(t, err, ErrForbidden, "historical references cannot bypass share revocation")
}

func TestMessageArtifactsKeepSessionOwnershipSeparateFromAgentOutput(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	message := &types.Message{
		AgentID:       "agent",
		AgentTenantID: 2,
		Artifacts:     types.MessageArtifacts{{URL: ref, FileName: "report.pdf"}},
	}
	catalog := fileCatalog{&types.StoredResource{TenantID: 1, PhysicalPath: "local://1/exports/report.pdf"}}
	file, err := ResolveMessageArtifact(callerContext(), message, 0, nil, catalog, MessageKBShareAuthorizer{})
	require.NoError(t, err)
	require.Equal(t, uint64(1), file.OwnerTenantID)
	catalog.resource.TenantID = 2
	catalog.resource.PhysicalPath = "local://2/exports/report.pdf"
	_, err = ResolveMessageArtifact(callerContext(), message, 0, nil, catalog, MessageKBShareAuthorizer{})
	require.ErrorIs(t, err, ErrForbidden)
}

type messageFileKBs struct{ kb *types.KnowledgeBase }

func (k messageFileKBs) GetKnowledgeBasesByIDsOnly(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{k.kb}, nil
}

func TestMessageSharedKBFilesRequireLiveBinding(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	message := &types.Message{
		AgentTenantID: 1, Role: "assistant",
		KnowledgeReferences: types.References{{KnowledgeBaseID: "shared", Content: ref}},
	}
	catalog := fileCatalog{&types.StoredResource{
		Handle: "AbCdEfGhIjKlMnOpQrStUv", TenantID: 2, PhysicalPath: "local://2/exports/a.png",
	}}
	binding := &fileBinding{allowed: true}
	shares := &shareLookup{permission: types.OrgRoleViewer}
	authorizer := MessageKBShareAuthorizer{
		ShareGuard: shares, KBs: messageFileKBs{kb: &types.KnowledgeBase{ID: "shared", TenantID: 2}}, Bindings: binding,
	}
	_, err := AuthorizeMessageFile(callerContext(), message, ref, nil, catalog, authorizer)
	require.NoError(t, err)
	require.Equal(t, "shared", binding.kb)
	require.Equal(t, uint64(2), binding.tenant)
	binding.allowed = false
	_, err = AuthorizeMessageFile(callerContext(), message, ref, nil, catalog, authorizer)
	require.ErrorIs(t, err, ErrForbidden, "retrieval text alone must not authorize an unrelated same-tenant resource")
	binding.allowed = true
	binding.err = errors.New("binding lookup unavailable")
	_, err = AuthorizeMessageFile(callerContext(), message, ref, nil, catalog, authorizer)
	require.ErrorIs(t, err, ErrForbidden)
	binding.err = nil
	authorizer.Bindings = nil
	_, err = AuthorizeMessageFile(callerContext(), message, ref, nil, catalog, authorizer)
	require.ErrorIs(t, err, ErrForbidden, "missing live lookup must fail closed")
}

// This fixture represents a catalog claim made by the artifact collector.
type artifactFileCatalog struct{ fileCatalog }

func (artifactFileCatalog) GetMessageFileBindings(
	_ context.Context,
	_ uint64,
	_, messageID string,
) (*types.MessageFileBindings, error) {
	return &types.MessageFileBindings{MessageArtifact: messageID == "message"}, nil
}

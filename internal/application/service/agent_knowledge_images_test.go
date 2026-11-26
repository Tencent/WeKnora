package service

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type knowledgeImageTestDocs struct {
	interfaces.KnowledgeService
	tags map[string][]*types.KnowledgeTag
}

func (s knowledgeImageTestDocs) GetKnowledgeTags(context.Context, []string) (map[string][]*types.KnowledgeTag, error) {
	return s.tags, nil
}

type knowledgeImageTestStorage struct {
	interfaces.StorageBackendResolver
	file      interfaces.FileService
	backendID string
	tenantID  uint64
}

func (s *knowledgeImageTestStorage) ResolveFileService(
	_ context.Context, tenant *types.Tenant, backendID, provider, _ string,
) (interfaces.FileService, string, error) {
	s.backendID, s.tenantID = backendID, tenant.ID
	return s.file, provider, nil
}

type knowledgeImageTestFile struct {
	interfaces.FileService
	data  []byte
	path  string
	reads int
}

func (f *knowledgeImageTestFile) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	f.path = path
	f.reads++
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

type knowledgeImageTestShare struct{ allowed bool }

func (s *knowledgeImageTestShare) CheckTenantKBPermission(
	context.Context, string, uint64, types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return types.OrgRoleViewer, s.allowed, nil
}

func (s *knowledgeImageTestShare) GetKnowledgeBasesByIDsOnly(
	context.Context, []string,
) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{{ID: "foreign", TenantID: 8}}, nil
}

type knowledgeImageTestArtifacts struct{ ref string }

func (s knowledgeImageTestArtifacts) Read(_ context.Context, reference string) ([]byte, error) {
	if s.ref == reference {
		return []byte("session output\n"), nil
	}
	return nil, tools.ErrArtifactUnavailable
}

func TestAgentReadsBoundKnowledgeImage(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	ctx := types.WithCaller(t.Context(), types.Caller{TenantID: 7, UserID: "alice"})
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases (
		id TEXT PRIMARY KEY, tenant_id INT, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE knowledges (
		id TEXT PRIMARY KEY, tenant_id INT, knowledge_base_id TEXT, title TEXT,
		enable_status TEXT, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb',7),('foreign',8)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledges (id,tenant_id,knowledge_base_id,title,enable_status)
		VALUES ('doc',7,'kb','Flowchart.pdf','enabled'),('other',7,'kb','Other','enabled'),
		('fdoc',8,'foreign','Foreign','enabled')`).Error)
	path := "storage://original/local://7/exports/flowchart.jpg"
	register := func(tenant uint64, path, kind string) string {
		ref, err := catalog.Register(ctx, tenant, path, interfaces.ResourceRegistration{Kind: kind})
		require.NoError(t, err)
		return ref
	}
	ref := register(7, path, "image")
	artifactRef := register(7, "storage://original/local://7/exports/report.md", "file")
	foreignRef := register(8, "storage://foreign-backend/local://8/exports/flowchart.jpg", "image")
	unboundRef := register(7, "storage://original/local://7/exports/not-bound.jpg", "image")
	invalidBinding := register(7, "storage://original/local://7/exports/source.jpg", "image")
	attachmentOnly := register(7, "storage://original/local://7/exports/attachment.jpg", "image")
	for _, b := range []struct{ ref, doc, relation string }{
		{ref, "doc", types.ResourceRelationExtractedImage},
		{ref, "doc", types.ResourceRelationAttachment},
		{foreignRef, "fdoc", types.ResourceRelationExtractedImage},
		{invalidBinding, "doc", types.ResourceRelationSourceFile},
		{attachmentOnly, "doc", types.ResourceRelationAttachment},
		{artifactRef, "doc", types.ResourceRelationAttachment},
	} {
		require.NoError(t, catalog.Bind(ctx, b.ref, types.ResourceOwnerKnowledge, b.doc, b.relation))
	}
	var jpg bytes.Buffer
	require.NoError(t, jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 32, 32)), nil))
	file := &knowledgeImageTestFile{data: jpg.Bytes()}
	storage := &knowledgeImageTestStorage{file: file}
	shares := &knowledgeImageTestShare{}
	svc := &agentService{
		knowledgeImages: newAgentKnowledgeImages(catalog, repository.NewResourceRepository(db), shares, storage),
		knowledgeService: knowledgeImageTestDocs{tags: map[string][]*types.KnowledgeTag{
			"doc": {{ID: "drawing"}},
		}},
	}
	makeReader := func(targets types.SearchTargets) *tools.ToolRegistry {
		registry := tools.NewToolRegistry()
		registry.RegisterTool(tools.NewReadDocumentTool(svc.knowledgeService, nil, targets))
		registry.RegisterTool(tools.NewReadFileTool(nil).WithArtifacts(knowledgeImageTestArtifacts{ref: artifactRef}))
		svc.registerKnowledgeImageReader(registry, &types.AgentConfig{SearchTargets: targets})
		return registry
	}
	read := func(ctx context.Context, registry *tools.ToolRegistry, reference string) *types.ToolResult {
		args, err := json.Marshal(tools.ReadFileInput{Path: reference, Detail: "full", Focus: "follow the arrows"})
		require.NoError(t, err)
		result, err := registry.ExecuteTool(ctx, tools.ToolReadFile, args)
		require.NoError(t, err)
		return result
	}
	target := func(kb string, docs, tags []string) types.SearchTargets {
		return types.SearchTargets{{
			Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: kb, KnowledgeIDs: docs, TagIDs: tags,
		}}
	}
	registry := makeReader(target("kb", []string{"doc"}, []string{"drawing"}))
	result := read(ctx, registry, ref)
	require.True(t, result.Success, result.Error)
	require.Len(t, result.Images, 1)
	require.Contains(t, result.Images[0], "data:image/jpeg;base64,")
	require.Equal(t, "full", result.ImageDetail)
	require.Equal(t, "follow the arrows", result.ImageFocus)
	require.Equal(t, "original", storage.backendID)
	require.Equal(t, uint64(7), storage.tenantID)
	require.Equal(t, path, file.path)
	require.Contains(t, result.Output, "Flowchart.pdf")
	require.Equal(t, "doc", result.Data["knowledge_id"])
	steps := tools.SanitizeAgentStepsForStorage([]types.AgentStep{{ToolCalls: []types.ToolCall{{
		Name: tools.ToolReadFile, Result: result,
	}}}})
	stored := steps[0].ToolCalls[0].Result
	require.Empty(t, stored.Images)
	require.Equal(t, []map[string]interface{}{{"url": ref}}, stored.Data["resource_references"])

	// The reader is also offered without a sandbox, attachments or artifacts.
	bare := tools.NewToolRegistry()
	config := &types.AgentConfig{SearchTargets: target("kb", nil, nil)}
	svc.registerKnowledgeImageReader(bare, config)
	require.NotContains(t, bare.ListTools(), tools.ToolReadFile)
	bare.RegisterTool(tools.NewReadDocumentTool(svc.knowledgeService, nil, config.SearchTargets))
	svc.registerKnowledgeImageReader(bare, config)
	require.True(t, read(ctx, bare, ref).Success)

	reads := file.reads
	for _, invalid := range []string{
		unboundRef, invalidBinding, attachmentOnly, foreignRef, "https://example.invalid/flowchart.jpg",
	} {
		require.False(t, read(ctx, registry, invalid).Success)
	}
	require.False(t, read(ctx, makeReader(target("kb", []string{"other"}, []string{"drawing"})), ref).Success,
		"a matching tag must not override a document whitelist")
	require.False(t, read(ctx, makeReader(target("kb", nil, []string{"other-tag"})), ref).Success)
	restrictedKey := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KnowledgeBaseIDs: []string{"other"}})
	require.False(t, read(restrictedKey, registry, ref).Success)
	foreignRegistry := makeReader(target("foreign", nil, nil))
	require.False(t, read(types.WithExecutionTenant(ctx, 8), foreignRegistry, foreignRef).Success)
	require.Contains(t, read(ctx, registry, artifactRef).Output, "session output")
	require.Equal(t, reads, file.reads, "failed authorization must not open storage")
	require.True(t, read(ctx, makeReader(target("kb", nil, []string{"drawing"})), ref).Success)
	shares.allowed = true
	foreignImage := read(ctx, foreignRegistry, foreignRef)
	require.True(t, foreignImage.Success, foreignImage.Error)
	require.Equal(t, uint64(8), storage.tenantID)
	require.Equal(t, "foreign-backend", storage.backendID)
	foreignSteps := tools.SanitizeAgentStepsForStorage([]types.AgentStep{{ToolCalls: []types.ToolCall{{
		Name: tools.ToolReadFile, Result: foreignImage,
	}}}})
	foreignMessage := &types.Message{AgentTenantID: 7, Role: "assistant", AgentSteps: foreignSteps}
	shareAuthorizer := access.MessageKBShareAuthorizer{ShareGuard: shares, KBs: shares}
	_, err := access.AuthorizeMessageFile(ctx, foreignMessage, foreignRef, nil, catalog, shareAuthorizer)
	require.NoError(t, err, "the tool result proves the shared KB even without an inline Markdown image")
	shares.allowed = false
	require.False(t, read(ctx, foreignRegistry, foreignRef).Success, "share revocation must take effect")
	_, err = access.AuthorizeMessageFile(ctx, foreignMessage, foreignRef, nil, catalog, shareAuthorizer)
	require.ErrorIs(t, err, access.ErrForbidden, "existing message-file authorization still checks current shares")

	// Disabling or deleting the document invalidates future reads.
	reads = file.reads
	require.NoError(t, db.Exec(`UPDATE knowledges SET enable_status = 'disabled' WHERE id = 'doc'`).Error)
	require.False(t, read(ctx, registry, ref).Success)
	require.NoError(t, db.Exec(`UPDATE knowledges
		SET enable_status = 'enabled', deleted_at = CURRENT_TIMESTAMP WHERE id = 'doc'`).Error)
	require.False(t, read(ctx, registry, ref).Success)
	require.Equal(t, reads, file.reads)
}

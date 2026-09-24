package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type resourceURLFixture struct {
	srv     *Server
	ctx     context.Context
	ep      *types.MCPEndpoint
	db      *gorm.DB
	ref     string
	path    string
	files   *resourceURLFiles
	storage *resourceURLStorage
}

type resourceURLFiles struct {
	interfaces.FileService
	calls      int
	path       string
	tenant     uint64
	tenantInfo uint64
	url        string
	err        error
}

func (s *resourceURLFiles) GetFileURL(ctx context.Context, path string) (string, error) {
	s.calls++
	s.path = path
	s.tenant = types.MustTenantIDFromContext(ctx)
	if info, ok := types.TenantInfoFromContext(ctx); ok {
		s.tenantInfo = info.ID
	}
	return s.url, s.err
}

type resourceURLStorage struct {
	interfaces.StorageBackendResolver
	files   interfaces.FileService
	catalog interfaces.ResourceCatalog
	err     error
	calls   int
	tenant  uint64
	backend string
}

func (s *resourceURLStorage) ResolveFileService(
	_ context.Context, tenant *types.Tenant, backend, provider, _ string,
) (interfaces.FileService, string, error) {
	s.calls++
	s.tenant = tenant.ID
	s.backend = backend
	// Like StorageBackendService, wrap per call so APP_EXTERNAL_URL is read then.
	return filesvc.NewResourceCatalogFileService(s.files, s.catalog), provider, s.err
}

// catalogWrapped mirrors initFileService, which decorates the global service.
func (f *resourceURLFixture) catalogWrapped() interfaces.FileService {
	return filesvc.NewResourceCatalogFileService(f.files, f.srv.resourceCatalog)
}

// The real binding repository distinguishes extracted images from unowned
// references and verifies that both the document and KB are still live.
func newResourceURLFixture(t *testing.T) *resourceURLFixture {
	t.Helper()
	t.Setenv("APP_EXTERNAL_URL", "https://weknora.example/prefix")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "images.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&types.StoredResource{}, &types.ResourceBinding{}, &types.ResourceAccessGrant{},
		&types.Knowledge{}, &types.KnowledgeBase{}, &types.Chunk{}, &types.StorageBackend{},
	))
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 2, Name: "Shared"}
	require.NoError(t, db.Create(kb).Error)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc-1", TenantID: 2, KnowledgeBaseID: kb.ID, Type: "file",
	}).Error)
	catalog := service.NewResourceCatalog(repository.NewResourceRepository(db))
	path := "local://2/exports/figure.png"
	ref, err := catalog.Register(context.Background(), 2, path, interfaces.ResourceRegistration{
		Kind: "image", MimeType: "image/png", OriginalName: "figure.png",
	})
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(
		context.Background(), ref, types.ResourceOwnerKnowledge, "doc-1", types.ResourceRelationExtractedImage,
	))
	files := &resourceURLFiles{url: "https://storage.example/figure.png?signature=test"}
	storage := &resourceURLStorage{files: files, catalog: catalog}
	srv := &Server{
		kbService:       &stubKBService{kbs: map[string]*types.KnowledgeBase{kb.ID: kb}},
		kbShareService:  &stubKBShareService{shared: map[string]types.OrgMemberRole{kb.ID: types.OrgRoleViewer}},
		tenantService:   &stubTenantService{tenants: map[uint64]*types.Tenant{2: {ID: 2}}},
		resourceCatalog: catalog, storageResolver: storage,
	}
	ep := &types.MCPEndpoint{
		ID: "ep", TenantID: 1, KnowledgeBaseIDs: types.StringArray{kb.ID},
		Tools: types.StringArray{types.MCPEndpointToolReadDocument, types.MCPEndpointToolAsk},
	}
	ctx := context.WithValue(mcpCallContext(1, ep), types.TenantInfoContextKey, &types.Tenant{ID: 1})
	return &resourceURLFixture{
		srv: srv, ctx: ctx, ep: ep, db: db, ref: ref, path: path, files: files, storage: storage,
	}
}

func (f *resourceURLFixture) rewrite(result *mcp.CallToolResult) *mcp.CallToolResult {
	return f.srv.rewriteResourceURLs(f.ctx, f.ep, result)
}

func (f *resourceURLFixture) updateResource(t *testing.T, updates map[string]any) {
	t.Helper()
	require.NoError(t, f.db.Model(&types.StoredResource{}).
		Where("handle = ?", strings.TrimPrefix(f.ref, types.ResourceScheme)).Updates(updates).Error)
}

func TestMCPResourceURLsRewriteTextAndTypedStructuredContent(t *testing.T) {
	f := newResourceURLFixture(t)
	typed := map[string]any{
		"large_integer": uint64(1152921504606846977),
		"chunks":        []map[string]any{{"images": []map[string]string{{"url": f.ref}}}},
		"references":    []askReference{{Images: []askImage{{URL: f.ref}}}},
		"wiki": struct {
			Content string `json:"content"`
		}{Content: "![figure](" + f.ref + ")"},
	}
	original := mcp.NewToolResultStructured(typed, "![figure]("+f.ref+")")
	result := f.rewrite(original)
	text := result.Content[0].(mcp.TextContent).Text
	require.NotContains(t, text, "resource://")
	require.Contains(t, text, "https://weknora.example/prefix/r/")
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "resource://")
	require.Contains(t, string(raw), `"large_integer":1152921504606846977`)
	url := strings.TrimSuffix(strings.TrimPrefix(text, "![figure]("), ")")
	require.Equal(t, 3, strings.Count(string(raw), url))
	resource, err := f.srv.resourceCatalog.ResolveAccessGrant(f.ctx, strings.TrimPrefix(
		url, "https://weknora.example/prefix/r/",
	))
	require.NoError(t, err)
	require.Equal(t, uint64(2), resource.TenantID)
	var grants int64
	require.NoError(t, f.db.Model(&types.ResourceAccessGrant{}).Count(&grants).Error)
	require.EqualValues(t, 1, grants, "one response must reuse one URL across text and metadata")
	require.Contains(t, original.Content[0].(mcp.TextContent).Text, f.ref)
	raw, err = json.Marshal(original.StructuredContent)
	require.NoError(t, err)
	require.Contains(t, string(raw), f.ref, "persisted/shared source values must remain untouched")
	require.Equal(t, uint64(2), f.storage.tenant)
}

func TestMCPResourceURLsRequireExistingPreviewPermission(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *resourceURLFixture)
	}{
		{"outside endpoint", func(_ *testing.T, f *resourceURLFixture) {
			f.ep.KnowledgeBaseIDs = types.StringArray{"other-kb"}
		}},
		{"revoked share", func(_ *testing.T, f *resourceURLFixture) {
			f.srv.kbShareService = &stubKBShareService{}
		}},
		{"wrong resource tenant", func(t *testing.T, f *resourceURLFixture) {
			f.updateResource(t, map[string]any{"tenant_id": 3})
		}},
		{"wrong physical tenant", func(t *testing.T, f *resourceURLFixture) {
			f.updateResource(t, map[string]any{"physical_path": "local://3/exports/figure.png"})
		}},
		{"deleted resource", func(t *testing.T, f *resourceURLFixture) {
			require.NoError(t, f.srv.resourceCatalog.MarkDeleted(f.ctx, f.ref))
		}},
		{"deleted document", func(t *testing.T, f *resourceURLFixture) {
			require.NoError(t, f.db.Delete(&types.Knowledge{ID: "doc-1"}).Error)
		}},
		{"deleted KB", func(t *testing.T, f *resourceURLFixture) {
			require.NoError(t, f.db.Delete(&types.KnowledgeBase{ID: "kb-1"}).Error)
		}},
		{"original upload", func(t *testing.T, f *resourceURLFixture) {
			f.updateResource(t, map[string]any{"physical_path": "local://2/doc-1/original.png"})
			require.NoError(t, f.db.Model(&types.ResourceBinding{}).Where("owner_id = ?", "doc-1").
				Update("relation", types.ResourceRelationSourceFile).Error)
		}},
		{"historical text only", func(t *testing.T, f *resourceURLFixture) {
			require.NoError(t, f.db.Where("owner_id = ?", "doc-1").Delete(&types.ResourceBinding{}).Error)
			require.NoError(t, f.db.Create(&types.Chunk{
				ID: "chunk", TenantID: 2, KnowledgeID: "doc-1", KnowledgeBaseID: "kb-1",
				Content: "![image](" + f.ref + ")", ImageInfo: `[{"url":"` + f.ref + `"}]`,
			}).Error)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newResourceURLFixture(t)
			tc.setup(t, f)
			result := f.rewrite(mcp.NewToolResultText(f.ref))
			require.Equal(t, f.ref, result.Content[0].(mcp.TextContent).Text)
			require.Zero(t, f.storage.calls)
			var count int64
			require.NoError(t, f.db.Model(&types.ResourceAccessGrant{}).Count(&count).Error)
			require.Zero(t, count, "denied references must never mint public grants")
		})
	}
}

func TestMCPResourceURLsUnrestrictedEndpointCoversSharedKB(t *testing.T) {
	// ask on an unrestricted endpoint searches the agent's KBs, which can be
	// shared from another workspace; the caller's viewer grant is what counts.
	f := newResourceURLFixture(t)
	f.ep.KnowledgeBaseIDs = nil
	kbs := f.srv.kbService.(*stubKBService)
	for i := 0; i < 20; i++ {
		id := "own-" + strconv.Itoa(i)
		kbs.kbs[id] = &types.KnowledgeBase{ID: id, TenantID: 1}
	}
	result := f.rewrite(mcp.NewToolResultText(f.ref))
	require.True(t, strings.HasPrefix(result.Content[0].(mcp.TextContent).Text, "https://weknora.example/prefix/r/"))
	require.Equal(t, 1, kbs.lookups, "only KBs that bind the reference are authorized")

	f = newResourceURLFixture(t)
	f.ep.KnowledgeBaseIDs = nil
	f.srv.kbShareService = &stubKBShareService{}
	result = f.rewrite(mcp.NewToolResultText(f.ref))
	require.Equal(t, f.ref, result.Content[0].(mcp.TextContent).Text)
}

func TestMCPResourceURLsStructuredContentShapes(t *testing.T) {
	f := newResourceURLFixture(t)
	result := f.rewrite(mcp.NewToolResultStructured([]map[string]string{{"url": f.ref}}, "list"))
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "resource://")
	require.Contains(t, string(raw), "https://weknora.example/prefix/r/")

	type plain struct{ Count uint64 }
	result = f.rewrite(mcp.NewToolResultStructured(plain{Count: 7}, "no references"))
	require.Equal(t, plain{Count: 7}, result.StructuredContent, "no reference keeps the typed value")
}

func TestMCPResourceURLsLegacyGlobalFallbackUsesOwner(t *testing.T) {
	f := newResourceURLFixture(t)
	t.Setenv("APP_EXTERNAL_URL", "")
	t.Setenv("STORAGE_TYPE", "minio")
	f.updateResource(t, map[string]any{"physical_path": "minio://bucket/2/exports/figure.png", "provider": "minio"})
	f.srv.storageResolver = service.NewStorageBackendService(
		repository.NewStorageBackendRepository(f.db), nil, f.srv.resourceCatalog,
	)
	f.srv.fileService = f.catalogWrapped()
	result := f.rewrite(mcp.NewToolResultText(f.ref))
	require.Equal(t, f.files.url, result.Content[0].(mcp.TextContent).Text)
	require.Equal(t, "minio://bucket/2/exports/figure.png", f.files.path)
	require.Equal(t, uint64(2), f.files.tenant)
	require.Equal(t, uint64(2), f.files.tenantInfo)
}

func TestMCPResourceURLsLegacyPathsAndSafeFallback(t *testing.T) {
	f := newResourceURLFixture(t)
	t.Setenv("APP_EXTERNAL_URL", "")
	result := f.rewrite(mcp.NewToolResultText(f.path))
	require.Equal(t, f.files.url, result.Content[0].(mcp.TextContent).Text)
	f.files.url = f.path
	result = f.rewrite(mcp.NewToolResultText(f.ref))
	require.Equal(t, f.ref, result.Content[0].(mcp.TextContent).Text, "no public URL leaves reference intact")
	f.files.err = errors.New("storage unavailable")
	result = f.rewrite(mcp.NewToolResultText(f.ref))
	require.Equal(t, f.ref, result.Content[0].(mcp.TextContent).Text)
	f.storage.err = errors.New("backend unavailable")
	f.srv.fileService = f.catalogWrapped()
	f.updateResource(t, map[string]any{
		"storage_backend_id": "explicit", "physical_path": "storage://explicit/" + f.path,
	})
	before := f.files.calls
	result = f.rewrite(mcp.NewToolResultText(f.ref))
	require.Equal(t, f.ref, result.Content[0].(mcp.TextContent).Text)
	require.Equal(t, before, f.files.calls, "explicit backend must never fall back to another bucket")
}

func TestMCPResourceURLsSkipErrorsAndPlainResults(t *testing.T) {
	f := newResourceURLFixture(t)
	failed := mcp.NewToolResultError(f.ref)
	require.Same(t, failed, f.rewrite(failed))
	require.Nil(t, f.rewrite(nil))
	plain := "unchanged https://example.com/public.png"
	require.Equal(t, plain, f.rewrite(mcp.NewToolResultText(plain)).Content[0].(mcp.TextContent).Text)
	require.Zero(t, f.storage.calls)
}

func TestMCPExistingToolTransportRewritesResourceURLs(t *testing.T) {
	f := newResourceURLFixture(t)
	srv := NewServer(f.srv.kbService, nil, nil, nil, nil, nil, nil, f.srv.kbShareService,
		f.srv.tenantService, nil, nil, nil, nil, f.srv.resourceCatalog, nil, f.srv.storageResolver)
	// Replace the existing document handler to isolate the outbound middleware
	// while retaining the real MCP transport, catalog and endpoint guard.
	srv.mcp.AddTool(readDocumentTool(), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultStructured(map[string]any{"image": f.ref}, "![image]("+f.ref+")"), nil
	})
	r := gin.New()
	r.POST("/mcp/:endpoint_id", func(c *gin.Context) {
		c.Request = c.Request.WithContext(f.ctx)
		c.Next()
	}, gin.WrapH(srv.Handler()))
	response := rpc(t, r, "tools/call", map[string]any{
		"name": types.MCPEndpointToolReadDocument, "arguments": map[string]any{"knowledge_id": "doc-1"},
	})
	raw, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "resource://")
	require.Contains(t, string(raw), "https://weknora.example/prefix/r/")
	require.NotContains(t, toolNames(t, rpc(t, r, "tools/list", map[string]any{})), "get_image")
}

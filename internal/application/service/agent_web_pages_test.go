package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	fileService "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type webPageMemoryFiles struct {
	interfaces.FileService
	objects map[string]string
	reads   int
}

func (f *webPageMemoryFiles) SaveBytes(
	_ context.Context, b []byte, tenant uint64, name string, _ bool,
) (string, error) {
	path := fmt.Sprintf("local://%d/exports/%s", tenant, name)
	f.objects[path] = string(b)
	return path, nil
}

func (f *webPageMemoryFiles) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	f.reads++
	content, ok := f.objects[path]
	if !ok {
		return nil, fmt.Errorf("missing object")
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

func (f *webPageMemoryFiles) DeleteFile(_ context.Context, path string) error {
	delete(f.objects, path)
	return nil
}

func TestSavedWebPagesPersistAcrossRunsAndEnforceMessageScope(t *testing.T) {
	catalog, db := newResourceCatalogForTest(t)
	// Only the ownership columns are needed; use the production GORM resource models above.
	require.NoError(t, db.Exec(`CREATE TABLE sessions (
 id TEXT PRIMARY KEY, tenant_id INTEGER, user_id TEXT, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE messages (
 id TEXT PRIMARY KEY, session_id TEXT, role TEXT, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO sessions (id,tenant_id,user_id) VALUES
 ('session-a',7,'alice'),('session-b',7,'bob')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO messages (id,session_id,role) VALUES
 ('message-a','session-a','assistant'),('message-b','session-b','assistant')`).Error)
	memory := &webPageMemoryFiles{objects: map[string]string{}}
	fs := fileService.NewResourceCatalogFileService(memory, catalog)
	pages := &agentWebPages{
		db: db, catalog: catalog, tenantID: 7, ownerID: "alice", sessionID: "session-a", messageID: "message-a",
		files: func(context.Context, string) (interfaces.FileService, error) { return fs, nil },
	}
	path, err := pages.Save(t.Context(), "first line\n完整正文\nthird line")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(path, "web://"))
	// Reads resolve the original backend even if the tenant's default later changes.
	require.NoError(t, db.Model(&types.StoredResource{}).
		Where("handle = ?", strings.TrimPrefix(path, "web://")).
		Update("storage_backend_id", "original-backend").Error)
	pages.files = func(_ context.Context, backendID string) (interfaces.FileService, error) {
		require.Equal(t, "original-backend", backendID)
		return fs, nil
	}
	// A new tool/source instance in a later turn can read without any old in-memory cache.
	later := *pages
	later.messageID = "later-message"
	reader := tools.NewReadFileTool(nil).WithWebPages(&later)
	result, err := reader.Execute(t.Context(), []byte(fmt.Sprintf(`{"path":%q,"offset":2,"limit":1}`, path)))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Contains(t, result.Output, "完整正文")
	require.Equal(t, 3, result.Data["next_offset"])
	reads := memory.reads
	for _, scope := range []struct {
		tenant         uint64
		owner, session string
	}{
		{8, "alice", "session-a"}, {7, "bob", "session-a"}, {7, "bob", "session-b"},
	} {
		other := *pages
		other.tenantID, other.ownerID, other.sessionID = scope.tenant, scope.owner, scope.session
		_, err := other.Read(t.Context(), path)
		require.Error(t, err)
	}
	require.Equal(t, reads, memory.reads, "authorization must run before the storage read")
	// A normal file bound to this message is not a web snapshot capability.
	ref, err := fs.SaveBytes(t.Context(), []byte("private attachment"), 7, "private.md", false)
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(t.Context(), ref,
		types.ResourceOwnerMessage, "message-a", types.ResourceRelationAttachment))
	_, err = pages.Read(t.Context(), strings.Replace(ref, "resource://", "web://", 1))
	require.Error(t, err)
	for _, invalid := range []string{"web://../secret", "resource://invalid", "/etc/passwd", path + "/extra"} {
		_, err = pages.Read(t.Context(), invalid)
		require.Error(t, err)
	}
	require.NoError(t, db.Exec(`UPDATE messages SET deleted_at = CURRENT_TIMESTAMP WHERE id='message-a'`).Error)
	_, err = pages.Read(t.Context(), path)
	require.Error(t, err)
	_, err = pages.Save(t.Context(), "after deletion")
	require.Error(t, err)
	require.Equal(t, reads, memory.reads)
}

func TestWebPageReaderRegistrationPreservesExistingReader(t *testing.T) {
	_, db := newResourceCatalogForTest(t)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	svc := &agentService{db: db}
	registry := tools.NewToolRegistry()
	registry.RegisterTool(tools.NewWebFetchTool())
	registry.RegisterTool(tools.NewWebSearchTool(nil, 5, ""))
	existing := tools.NewReadFileTool(nil)
	registry.RegisterTool(existing)
	svc.registerWebPageFiles(ctx, registry, &types.AgentConfig{WebSearchEnabled: true}, "session", "message")
	actual, err := registry.GetTool(tools.ToolReadFile)
	require.NoError(t, err)
	require.Same(t, existing, actual)
	require.Contains(t, actual.Description(), "web://")
	withoutSandbox := tools.NewToolRegistry()
	withoutSandbox.RegisterTool(tools.NewWebFetchTool())
	svc.registerWebPageFiles(ctx, withoutSandbox, &types.AgentConfig{WebSearchEnabled: true}, "session", "message")
	_, err = withoutSandbox.GetTool(tools.ToolReadFile)
	require.NoError(t, err)
	disabled := tools.NewToolRegistry()
	disabled.RegisterTool(tools.NewWebFetchTool())
	svc.registerWebPageFiles(ctx, disabled, &types.AgentConfig{}, "session", "message")
	_, err = disabled.GetTool(tools.ToolReadFile)
	require.Error(t, err)
}

func TestSharedAgentWebPagesUseSessionOwnerStorageScope(t *testing.T) {
	_, db := newResourceCatalogForTest(t)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(99))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	ctx = types.WithSandboxTenantID(ctx, 7)
	registry := tools.NewToolRegistry()
	registry.RegisterTool(tools.NewWebFetchTool())
	var storageTenant uint64
	resolver := &webPageStorageResolver{resolve: func(tenant *types.Tenant) {
		storageTenant = tenant.ID
	}}
	svc := &agentService{db: db, storageResolver: resolver}
	svc.registerWebPageFiles(ctx, registry, &types.AgentConfig{WebSearchEnabled: true}, "session", "message")
	// Exercise construction of the source through a read. A scoped fixture binds a page
	// to the caller's session, while the search/provider tenant is 99.
	require.NoError(t, db.Exec(`CREATE TABLE sessions
		(id TEXT PRIMARY KEY, tenant_id INTEGER, user_id TEXT, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE messages
		(id TEXT PRIMARY KEY, session_id TEXT, role TEXT, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO sessions (id,tenant_id,user_id) VALUES ('session',7,'alice')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO messages (id,session_id,role)
		VALUES ('message','session','assistant')`).Error)
	catalog := NewResourceCatalog(repository.NewResourceRepository(db))
	ref, err := catalog.Register(ctx, 7, "local://7/page.md", interfaces.ResourceRegistration{})
	require.NoError(t, err)
	require.NoError(t, catalog.Bind(ctx, ref, types.ResourceOwnerMessage, "message", webPageRelation))
	reader, err := registry.GetTool(tools.ToolReadFile)
	require.NoError(t, err)
	path := strings.Replace(ref, "resource://", "web://", 1)
	result, err := reader.Execute(ctx, []byte(fmt.Sprintf(`{"path":%q}`, path)))
	require.NoError(t, err)
	require.False(t, result.Success, "resolver is expected to fail after routing to the session owner tenant")
	require.Equal(t, uint64(7), storageTenant)
}

type webPageStorageResolver struct {
	interfaces.StorageBackendResolver
	resolve func(*types.Tenant)
}

func (r *webPageStorageResolver) ResolveFileService(
	_ context.Context, tenant *types.Tenant, _, _, _ string,
) (interfaces.FileService, string, error) {
	r.resolve(tenant)
	return nil, "", fmt.Errorf("storage unavailable for this test")
}

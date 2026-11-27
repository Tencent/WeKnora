package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newCatalogRefreshRepository(t *testing.T) (*gorm.DB, interfaces.MCPServiceRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "catalog.db")+"?_journal_mode=WAL"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.MCPService{}, &types.MCPMetadata{}))
	return db, repository.NewMCPServiceRepository(db)
}

func TestMCPCatalogRefreshRejectsConcurrentConnectionChange(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	newEndpoint := func(tool string) *httptest.Server {
		server := sdkserver.NewMCPServer(tool, "1", sdkserver.WithToolCapabilities(false))
		server.AddTool(
			sdkmcp.NewTool(tool),
			func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				return sdkmcp.NewToolResultText(tool), nil
			},
		)
		upstream := httptest.NewServer(sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true)))
		t.Cleanup(upstream.Close)
		return upstream
	}
	a, b := newEndpoint("tool_a"), newEndpoint("tool_b")
	_, repo := newCatalogRefreshRepository(t)
	manager := mcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	settings := NewMCPServiceService(repo, manager, nil)
	metadata := settings.(interfaces.MCPMetadataService)
	ctx := types.WithCaller(context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1)),
		types.Caller{TenantID: 1, Role: types.TenantRoleAdmin})
	service := &types.MCPService{
		ID: "orders", Name: "Orders", TenantID: 1, Enabled: true,
		URL: &a.URL, TransportType: types.MCPTransportHTTPStreamable,
	}
	require.NoError(t, repo.Create(ctx, service))
	previous, err := metadata.RefreshMCPMetadata(ctx, service.TenantID, service.ID)
	require.NoError(t, err)

	// Apply the settings edit after tools/list has returned and before its snapshot is saved.
	newRegistry := func(row *types.MCPService, editAfterList func([]*types.MCPTool)) *tools.ToolRegistry {
		t.Helper()
		registry := tools.NewToolRegistry()
		io := &tools.MCPMetadataIO{Get: metadata.GetMCPMetadata}
		io.Put = func(
			ctx context.Context, tenant uint64, source *types.MCPService, listed []*types.MCPTool,
			instructions string, server types.MCPServerInfo, started time.Time,
		) error {
			editAfterList(listed)
			return metadata.PersistMCPMetadata(ctx, tenant, source, listed, instructions, server, started)
		}
		_, err := tools.RegisterMCPTools(
			ctx, registry, []*types.MCPService{row}, manager, nil, 0, settings.GetMCPServiceByID, io,
		)
		require.NoError(t, err)
		return registry
	}
	list := func(registry *tools.ToolRegistry, refresh bool) *types.ToolResult {
		t.Helper()
		args, err := json.Marshal(map[string]any{"mode": "list_tools", "server_id": service.ID, "refresh": refresh})
		require.NoError(t, err)
		result, err := registry.ExecuteTool(ctx, tools.ToolDiscoverMCPTools, args)
		require.NoError(t, err)
		return result
	}
	active := newRegistry(service, func(listed []*types.MCPTool) {
		require.Len(t, listed, 1)
		require.Equal(t, "tool_a", listed[0].Name)
		edit := &types.MCPService{ID: service.ID, TenantID: service.TenantID, URL: &b.URL}
		require.NoError(t, settings.UpdateMCPService(ctx, edit, nil))
	})
	interrupted := list(active, true)
	assert.False(t, interrupted.Success, "the in-flight refresh returned %s", interrupted.Output)

	current, err := settings.GetMCPServiceByID(ctx, service.TenantID, service.ID)
	require.NoError(t, err)
	require.Equal(t, b.URL, *current.URL)
	stored, err := metadata.GetMCPMetadata(ctx, current.TenantID, current.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.True(t, stored.Stale)
	assert.Equal(t, previous.ConfigFingerprint, stored.ConfigFingerprint)
	assert.True(t, previous.SyncedAt.Equal(stored.SyncedAt), "the interrupted refresh replaced the saved directory")

	// Editing display text during B's refresh must still allow B's new directory to be used and saved.
	next := newRegistry(current, func(listed []*types.MCPTool) {
		require.Len(t, listed, 1)
		require.Equal(t, "tool_b", listed[0].Name)
		edit := &types.MCPService{ID: current.ID, TenantID: current.TenantID, Description: "Current order tools"}
		require.NoError(t, settings.UpdateMCPService(ctx, edit, map[string]bool{"description": true}))
	})
	offline := list(next, false)
	assert.False(t, offline.Success, "a new engine advertised %s", offline.Output)
	recovered := list(next, true)
	require.True(t, recovered.Success, recovered.Error)
	var page struct{ Tools []struct{ Name string } }
	require.NoError(t, json.Unmarshal([]byte(recovered.Output), &page))
	require.Len(t, page.Tools, 1)
	require.Equal(t, "tool_b", page.Tools[0].Name)
	edited, err := settings.GetMCPServiceByID(ctx, current.TenantID, current.ID)
	require.NoError(t, err)
	require.Equal(t, "Current order tools", edited.Description)
	stored, err = metadata.GetMCPMetadata(ctx, current.TenantID, current.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.False(t, stored.Stale)
	require.Len(t, stored.Tools, 1)
	require.Equal(t, "tool_b", stored.Tools[0].Name)
}

func TestMCPMetadataSaveRollsBackOnReadbackFailure(t *testing.T) {
	db, repo := newCatalogRefreshRepository(t)
	ctx := t.Context()
	service := &types.MCPService{ID: "orders", TenantID: 1, Enabled: true}
	require.NoError(t, repo.Create(ctx, service))
	metadata := NewMCPServiceService(repo, nil, nil).(interfaces.MCPMetadataService)
	save := func(tool string, started time.Time) error {
		return metadata.PersistMCPMetadata(ctx, 1, service, []*types.MCPTool{{Name: tool}},
			"", types.MCPServerInfo{}, started)
	}
	now := time.Now().UTC()
	require.NoError(t, save("previous", now))
	// The upsert succeeds. Its readback must discover the malformed stored
	// tools and roll back the write, leaving the original directory and date.
	require.NoError(t, db.Exec(`CREATE TRIGGER damage_directory AFTER UPDATE ON mcp_metadata
		BEGIN UPDATE mcp_metadata SET tools = '{' WHERE service_id = NEW.service_id; END`).Error)
	err := save("replacement", now.Add(time.Minute))
	require.ErrorIs(t, err, types.ErrMCPMetadataStorage)
	got, err := metadata.GetMCPMetadata(ctx, 1, service.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Tools, 1)
	require.Equal(t, "previous", got.Tools[0].Name)
	require.True(t, got.SyncedAt.Equal(now))
}

func TestMCPMetadataSaveRejectsSQLiteConcurrentEdit(t *testing.T) {
	for _, edit := range []string{"url", "disable", "delete"} {
		t.Run(edit, func(t *testing.T) {
			db, repo := newCatalogRefreshRepository(t)
			ctx := t.Context()
			oldURL, newURL := "https://old.example/mcp", "https://new.example/mcp"
			source := &types.MCPService{ID: "orders", TenantID: 1, Enabled: true, URL: &oldURL}
			require.NoError(t, repo.Create(ctx, source))
			metadata := NewMCPServiceService(repo, nil, nil).(interfaces.MCPMetadataService)
			now := time.Now().UTC()
			persist := func(name string, started time.Time) error {
				return metadata.PersistMCPMetadata(ctx, 1, source, []*types.MCPTool{{Name: name}},
					"", types.MCPServerInfo{}, started)
			}
			require.NoError(t, persist("previous", now))
			// Let an independent SQLite connection commit its edit after the
			// save transaction has read the service but before its upsert.
			edited := false
			afterRead := db.Callback().Query().After("gorm:query")
			require.NoError(t, afterRead.Register("test:edit_service", func(tx *gorm.DB) {
				if edited || tx.Statement.Table != "mcp_services" {
					return
				}
				edited = true
				change := *source
				switch edit {
				case "url":
					change.URL = &newURL
					require.NoError(t, repo.Update(ctx, &change))
				case "disable":
					change.Enabled = false
					require.NoError(t, repo.Update(ctx, &change))
				case "delete":
					require.NoError(t, repo.Delete(ctx, 1, source.ID))
				}
			}))
			require.ErrorIs(t, persist("replacement", now.Add(time.Minute)), types.ErrMCPMetadataConnectionChanged)
			require.True(t, edited)
			stored, err := repo.(interfaces.MCPMetadataRepository).GetMetadata(ctx, 1, source.ID, "")
			require.NoError(t, err)
			require.NotNil(t, stored)
			require.Len(t, stored.Tools, 1)
			require.Equal(t, "previous", stored.Tools[0].Name)
			require.True(t, stored.SyncedAt.Equal(now))
		})
	}
}

func TestMCPMetadataSaveRequiresVerifiableSource(t *testing.T) {
	db, repo := newCatalogRefreshRepository(t)
	source := &types.MCPService{ID: "orders", TenantID: 1, Enabled: true}
	require.NoError(t, repo.Create(t.Context(), source))
	metadata := NewMCPServiceService(repo, nil, nil).(interfaces.MCPMetadataService)
	require.NoError(t, db.Migrator().DropTable(&types.MCPService{}))
	err := metadata.PersistMCPMetadata(t.Context(), 1, source, nil, "", types.MCPServerInfo{}, time.Now())
	require.ErrorIs(t, err, types.ErrMCPMetadataConnectionUnverified)
	require.NotErrorIs(t, err, types.ErrMCPMetadataStorage)
	_, err = metadata.GetMCPMetadata(t.Context(), 1, source.ID)
	require.ErrorIs(t, err, types.ErrMCPMetadataConnectionUnverified)
}

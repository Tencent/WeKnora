package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMCPMetadataRefreshAndOfflineEditing(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.MCPService{}, &types.MCPMetadata{}))
	repo := repository.NewMCPServiceRepository(db)
	manager := mcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	svc := NewMCPServiceService(repo, manager, nil)
	metadata := svc.(interfaces.MCPMetadataService)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	server := sdkserver.NewMCPServer(
		"Orders",
		"2",
		sdkserver.WithToolCapabilities(false),
		sdkserver.WithInstructions("Original server instructions"),
	)
	desc := strings.Repeat("Full description. ", 100)
	schema := json.RawMessage(
		`{"type":"object","properties":{"id":{"type":"string"}},` +
			`"oneOf":[{"required":["id"]}],"additionalProperties":false}`,
	)
	server.AddTool(
		sdkmcp.Tool{Name: "get_order", Description: desc, RawInputSchema: schema},
		func(context.Context, sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return sdkmcp.NewToolResultText("unused"), nil
		},
	)
	upstream := httptest.NewServer(sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true)))
	t.Cleanup(upstream.Close)
	service := &types.MCPService{
		ID:                "svc",
		TenantID:          1,
		Name:              "Orders",
		Description:       "My overview",
		UsageInstructions: "My guidance",
		Enabled:           true,
		URL:               &upstream.URL,
		TransportType:     types.MCPTransportHTTPStreamable,
	}
	require.NoError(t, repo.Create(ctx, service))
	none, err := metadata.GetMCPMetadata(ctx, 1, "svc")
	require.NoError(t, err)
	require.Nil(t, none)
	saved, err := metadata.RefreshMCPMetadata(ctx, 1, "svc")
	require.NoError(t, err)
	require.Equal(t, "Original server instructions", saved.Instructions)
	require.Equal(t, "Orders", saved.ServerName)
	require.Len(t, saved.Tools, 1)
	require.Equal(t, desc, saved.Tools[0].Description)
	require.JSONEq(t, string(schema), string(saved.Tools[0].InputSchema))
	upstream.Close()
	_, err = metadata.RefreshMCPMetadata(ctx, 1, "svc")
	require.Error(t, err)
	got, err := metadata.GetMCPMetadata(ctx, 1, "svc")
	require.NoError(t, err)
	require.Equal(t, saved.Tools, got.Tools, "failed refresh must retain the persisted snapshot")
	require.False(t, got.Stale)
	require.NoError(
		t,
		svc.UpdateMCPService(
			ctx,
			&types.MCPService{
				ID:                "svc",
				TenantID:          1,
				Description:       "Edited overview",
				UsageInstructions: "Edited guidance",
			},
			map[string]bool{"description": true, "usage_instructions": true},
		),
	)
	got, err = metadata.GetMCPMetadata(ctx, 1, "svc")
	require.NoError(t, err)
	require.False(t, got.Stale, "documentation edits do not invalidate upstream identity")
	stored, err := svc.GetMCPServiceByID(ctx, 1, "svc")
	require.NoError(t, err)
	require.Equal(t, "Edited guidance", stored.UsageInstructions)
	newURL := upstream.URL + "/changed"
	require.NoError(t, svc.UpdateMCPService(ctx, &types.MCPService{ID: "svc", TenantID: 1, URL: &newURL}, nil))
	got, err = metadata.GetMCPMetadata(ctx, 1, "svc")
	require.NoError(t, err)
	require.True(t, got.Stale)
	_, err = metadata.GetMCPMetadata(ctx, 2, "svc")
	require.ErrorIs(t, err, types.ErrMCPServiceNotFound, "another tenant must not access the snapshot")
	require.NoError(t, metadata.PersistMCPMetadata(
		ctx,
		1,
		"svc",
		[]*types.MCPTool{{Name: "live", Description: "from chat", InputSchema: schema}},
		"from-chat",
	))
	got, err = metadata.GetMCPMetadata(ctx, 1, "svc")
	require.NoError(t, err)
	require.Equal(t, "live", got.Tools[0].Name)
	require.Equal(t, "from-chat", got.Instructions)

	// OAuth metadata belongs to the effective authorizing user, not the admin
	// who happened to create the shared service configuration.
	stored, err = repo.GetByID(ctx, 1, "svc")
	require.NoError(t, err)
	stored.AuthConfig = &types.MCPAuthConfig{AuthType: types.MCPAuthOAuth}
	require.NoError(t, repo.Update(ctx, stored))
	userA := types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "a"})
	userB := types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "b"})
	personal := *saved
	personal.Principal = types.MCPOAuthPrincipalFromContext(userA).StorageID()
	personal.ConfigFingerprint = types.MCPConfigFingerprint(stored)
	require.NoError(t, repo.(interfaces.MCPMetadataRepository).SaveMetadata(ctx, &personal))
	got, err = metadata.GetMCPMetadata(userA, 1, "svc")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.False(t, got.Stale)
	got, err = metadata.GetMCPMetadata(userB, 1, "svc")
	require.NoError(t, err)
	require.Nil(t, got, "never fall back to another user's or a formerly shared snapshot")
	_, err = metadata.GetMCPMetadata(ctx, 1, "svc")
	require.ErrorIs(t, err, types.ErrMCPOAuthPrincipalRequired, "OAuth metadata cannot be read without a principal")
}

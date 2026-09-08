package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func metadataPrincipal(ctx context.Context, service *types.MCPService) (string, error) {
	if !service.AuthConfig.IsOAuth() {
		return "", nil
	}
	p := types.MCPOAuthPrincipalFromContext(ctx).StorageID()
	if p == "" {
		return "", fmt.Errorf("OAuth metadata requires an authenticated principal")
	}
	return p, nil
}

func (s *mcpServiceService) GetMCPMetadata(ctx context.Context, tenant uint64, id string) (*types.MCPMetadata, error) {
	service, err := s.mcpServiceRepo.GetByID(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	if service == nil || tenant == 0 {
		return nil, fmt.Errorf("MCP service not found")
	}
	principal, err := metadataPrincipal(ctx, service)
	if err != nil {
		return nil, err
	}
	repo, ok := s.mcpServiceRepo.(interfaces.MCPMetadataRepository)
	if !ok {
		return nil, fmt.Errorf("MCP metadata storage is unavailable")
	}
	snapshot, err := repo.GetMetadata(ctx, tenant, id, principal)
	if err == nil && snapshot != nil {
		snapshot.Stale = snapshot.ConfigFingerprint != types.MCPConfigFingerprint(service)
	}
	return snapshot, err
}

// Refresh performs no user operations and never publishes a partial tools/list.
// A failed refresh preserves the last stored snapshot for inspection. Config
// fingerprints keep snapshots for old connections out of the execution path.
func (s *mcpServiceService) RefreshMCPMetadata(
	ctx context.Context,
	tenant uint64,
	id string,
) (*types.MCPMetadata, error) {
	service, err := s.mcpServiceRepo.GetByID(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	if service == nil || tenant == 0 {
		return nil, fmt.Errorf("MCP service not found")
	}
	principal, err := metadataPrincipal(ctx, service)
	if err != nil {
		return nil, err
	}
	repo, ok := s.mcpServiceRepo.(interfaces.MCPMetadataRepository)
	if !ok {
		return nil, fmt.Errorf("MCP metadata storage is unavailable")
	}
	started := time.Now().UTC()
	config := &mcp.ClientConfig{Service: service}
	if service.AuthConfig.IsOAuth() {
		config.OAuthRepo, config.TenantID, config.Principal = s.oauthRepo, tenant, types.MCPOAuthPrincipalFromContext(
			ctx,
		)
	}
	client, err := mcp.NewMCPClient(config)
	if err != nil {
		return nil, err
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.Connect(refreshCtx); err != nil {
		return nil, err
	}
	defer func() { _ = client.Disconnect() }()
	init, err := client.Initialize(refreshCtx)
	if err != nil {
		return nil, err
	}
	listed, err := client.ListTools(refreshCtx)
	if err != nil {
		return nil, fmt.Errorf("could not refresh complete MCP directory: %w", err)
	}
	seen := make(map[string]bool)
	for _, tool := range listed {
		if tool == nil || tool.Name == "" || seen[tool.Name] {
			return nil, fmt.Errorf("MCP directory contains empty or duplicate tool names")
		}
		seen[tool.Name] = true
	}
	if listed == nil {
		listed = []*types.MCPTool{}
	}
	snapshot := &types.MCPMetadata{
		TenantID: tenant, ServiceID: id, Principal: principal,
		ConfigFingerprint: types.MCPConfigFingerprint(service), Tools: listed,
		Instructions: init.Instructions, ServerName: init.ServerInfo.Name,
		ServerVersion: init.ServerInfo.Version, ServerDescription: init.ServerInfo.Description,
		SyncedAt: started,
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if len(raw) > 8*1024*1024 {
		return nil, fmt.Errorf("MCP metadata exceeds the 8 MiB storage limit")
	}
	current, err := s.mcpServiceRepo.GetByID(refreshCtx, tenant, id)
	if err != nil {
		return nil, err
	}
	if current == nil || types.MCPConfigFingerprint(current) != snapshot.ConfigFingerprint {
		return nil, fmt.Errorf("MCP connection changed during refresh; refresh the saved configuration again")
	}
	if err := repo.SaveMetadata(refreshCtx, snapshot); err != nil {
		return nil, err
	}
	return s.GetMCPMetadata(refreshCtx, tenant, id)
}

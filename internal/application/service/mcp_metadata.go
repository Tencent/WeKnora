package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
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
		return "", types.ErrMCPOAuthPrincipalRequired
	}
	return p, nil
}

func (s *mcpServiceService) metadataRepo() (interfaces.MCPMetadataRepository, error) {
	repo, ok := s.mcpServiceRepo.(interfaces.MCPMetadataRepository)
	if !ok {
		return nil, types.ErrMCPMetadataStorage
	}
	return repo, nil
}

func (s *mcpServiceService) loadServiceForMetadata(
	ctx context.Context,
	tenant uint64,
	id string,
) (*types.MCPService, string, interfaces.MCPMetadataRepository, error) {
	service, err := s.mcpServiceRepo.GetByID(ctx, tenant, id)
	if err != nil {
		return nil, "", nil, err
	}
	if service == nil || tenant == 0 {
		return nil, "", nil, types.ErrMCPServiceNotFound
	}
	principal, err := metadataPrincipal(ctx, service)
	if err != nil {
		return nil, "", nil, err
	}
	repo, err := s.metadataRepo()
	if err != nil {
		return nil, "", nil, err
	}
	return service, principal, repo, nil
}

func (s *mcpServiceService) GetMCPMetadata(ctx context.Context, tenant uint64, id string) (*types.MCPMetadata, error) {
	service, principal, repo, err := s.loadServiceForMetadata(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	snapshot, err := repo.GetMetadata(ctx, tenant, id, principal)
	if err == nil && snapshot != nil {
		snapshot.Stale = snapshot.ConfigFingerprint != types.MCPConfigFingerprint(service)
	}
	return snapshot, err
}

func (s *mcpServiceService) ListMCPMetadataSummaries(
	ctx context.Context,
	tenant uint64,
	services []*types.MCPService,
) (map[string]*types.MCPMetadataSummary, error) {
	out := make(map[string]*types.MCPMetadataSummary, len(services))
	if len(services) == 0 {
		return out, nil
	}
	repo, err := s.metadataRepo()
	if err != nil {
		return out, nil
	}
	principals := []string{""}
	if p := types.MCPOAuthPrincipalFromContext(ctx).StorageID(); p != "" {
		principals = append(principals, p)
	}
	rows, err := repo.ListMetadataSummaries(ctx, tenant, principals)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*types.MCPMetadataSummary, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		byKey[row.ServiceID+"\x00"+row.Principal] = row
	}
	for _, service := range services {
		if service == nil {
			continue
		}
		principal, err := metadataPrincipal(ctx, service)
		if err != nil {
			continue
		}
		if row := byKey[service.ID+"\x00"+principal]; row != nil {
			out[service.ID] = row
		}
	}
	return out, nil
}

func (s *mcpServiceService) commitMCPMetadata(
	ctx context.Context,
	tenant uint64,
	service *types.MCPService,
	principal string,
	listed []*types.MCPTool,
	instructions, serverName, serverVersion, serverDescription string,
	started time.Time,
) (*types.MCPMetadata, error) {
	repo, err := s.metadataRepo()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, tool := range listed {
		if tool == nil || tool.Name == "" || seen[tool.Name] {
			return nil, types.ErrMCPMetadataInvalidTools
		}
		seen[tool.Name] = true
	}
	if listed == nil {
		listed = []*types.MCPTool{}
	}
	snapshot := &types.MCPMetadata{
		TenantID:          tenant,
		ServiceID:         service.ID,
		Principal:         principal,
		ConfigFingerprint: types.MCPConfigFingerprint(service),
		Tools:             listed,
		Instructions:      instructions,
		ServerName:        serverName,
		ServerVersion:     serverVersion,
		ServerDescription: serverDescription,
		SyncedAt:          started,
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if len(raw) > 8*1024*1024 {
		return nil, types.ErrMCPMetadataTooLarge
	}
	current, err := s.mcpServiceRepo.GetByID(ctx, tenant, service.ID)
	if err != nil {
		return nil, err
	}
	if current == nil || types.MCPConfigFingerprint(current) != snapshot.ConfigFingerprint {
		return nil, types.ErrMCPMetadataConnectionChanged
	}
	if err := repo.SaveMetadata(ctx, snapshot); err != nil {
		return nil, err
	}
	return s.GetMCPMetadata(ctx, tenant, service.ID)
}

// PersistMCPMetadata writes a complete directory already listed on an authorized
// connection. It is how a chatting OAuth user stores their own snapshot without
// an admin settings refresh.
func (s *mcpServiceService) PersistMCPMetadata(
	ctx context.Context,
	tenant uint64,
	id string,
	listed []*types.MCPTool,
	instructions string,
) error {
	service, principal, _, err := s.loadServiceForMetadata(ctx, tenant, id)
	if err != nil {
		return err
	}
	_, err = s.commitMCPMetadata(
		ctx, tenant, service, principal, listed, instructions, "", "", "", time.Now().UTC(),
	)
	return err
}

// Refresh performs no user operations and never publishes a partial tools/list.
// A failed refresh preserves the last stored snapshot for inspection. Config
// fingerprints keep snapshots for old connections out of the execution path.
func (s *mcpServiceService) RefreshMCPMetadata(
	ctx context.Context,
	tenant uint64,
	id string,
) (*types.MCPMetadata, error) {
	service, principal, _, err := s.loadServiceForMetadata(ctx, tenant, id)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("could not refresh MCP directory: %w", err)
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.Connect(refreshCtx); err != nil {
		return nil, fmt.Errorf("could not refresh MCP directory: %w", err)
	}
	defer func() { _ = client.Disconnect() }()
	init, err := client.Initialize(refreshCtx)
	if err != nil {
		return nil, fmt.Errorf("could not refresh MCP directory: %w", err)
	}
	listed, err := client.ListTools(refreshCtx)
	if err != nil {
		return nil, fmt.Errorf("could not refresh complete MCP directory: %w", err)
	}
	const maxLoggedTools = 30
	logged := 0
	for i, tool := range listed {
		if tool == nil {
			continue
		}
		if logged >= maxLoggedTools {
			logger.Debugf(
				refreshCtx,
				"MCP metadata omitted remaining tools from debug listing service_id=%s",
				service.ID,
			)
			break
		}
		logger.Debugf(
			refreshCtx,
			"MCP metadata tool[%d] service_id=%s name=%s description_len=%d schema_len=%d",
			i,
			service.ID,
			tool.Name,
			len(tool.Description),
			len(tool.InputSchema),
		)
		logged++
	}
	logger.Debugf(
		refreshCtx,
		"MCP metadata listed %d tools service_id=%s instructions_len=%d server_description_len=%d",
		len(listed),
		service.ID,
		len(init.Instructions),
		len(init.ServerInfo.Description),
	)
	return s.commitMCPMetadata(
		refreshCtx,
		tenant,
		service,
		principal,
		listed,
		init.Instructions,
		init.ServerInfo.Name,
		init.ServerInfo.Version,
		init.ServerInfo.Description,
		started,
	)
}

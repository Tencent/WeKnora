package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// PluginPreflightResponse contains the preflight check data needed before installation.
type PluginPreflightResponse struct {
	PluginName      string               `json:"plugin_name"`
	SkillCount      int                  `json:"skill_count"`
	MCPCount        int                  `json:"mcp_count"`
	RequiresSandbox bool                 `json:"requires_sandbox"`
	EnvDeclarations []types.PluginEnvVar `json:"env_declarations"`
}

// MarketplacePluginView represents a plugin in marketplace listings.
type MarketplacePluginView struct {
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Version         string   `json:"version,omitempty"`
	Category        string   `json:"category,omitempty"`
	Keywords        []string `json:"keywords,omitempty"`
	SkillCount      int      `json:"skill_count"`
	MCPCount        int      `json:"mcp_count"`
	RequiresSandbox bool     `json:"requires_sandbox"`
	Installed       bool     `json:"installed"`
	// InstalledVersion is the version the tenant installed. Refreshing the
	// marketplace does not upgrade an install, so it can lag Version.
	InstalledVersion string `json:"installed_version,omitempty"`
	Status           string `json:"status,omitempty"`
	InstallID        string `json:"install_id,omitempty"`
	Error            string `json:"error,omitempty"`
}

// MarketplaceDetailView represents the full marketplace with its plugins.
type MarketplaceDetailView struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Version     string                  `json:"version,omitempty"`
	OwnerName   string                  `json:"owner_name,omitempty"`
	OwnerEmail  string                  `json:"owner_email,omitempty"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
	Plugins     []MarketplacePluginView `json:"plugins"`
}

// PluginInstallResult is returned after initiating installation.
type PluginInstallResult struct {
	InstallID         string            `json:"install_id"`
	Status            string            `json:"status"`
	MCPServers        []string          `json:"mcp_servers,omitempty"`
	Skills            []string          `json:"skills,omitempty"`
	MCPOAuthStates    map[string]string `json:"mcp_oauth_states,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	AsyncInstallation bool              `json:"async_installation"`
}

// PluginDetailView represents an installed plugin's detailed state.
type PluginDetailView struct {
	ID              string `json:"id"`
	TenantID        uint64 `json:"tenant_id"`
	MarketplaceID   string `json:"marketplace_id"`
	MarketplaceName string `json:"marketplace_name,omitempty"`
	PluginName      string `json:"plugin_name"`
	Version         string `json:"version,omitempty"`
	// LatestVersion is the version the marketplace ships now; it differs from
	// Version after a refresh, until the plugin is reinstalled.
	LatestVersion    string                   `json:"latest_version,omitempty"`
	Description      string                   `json:"description,omitempty"`
	SandboxConfigIDs []string                 `json:"sandbox_config_ids"`
	Status           string                   `json:"status"`
	Error            string                   `json:"error,omitempty"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
	MCPServices      []PluginMCPServiceView   `json:"mcp_services,omitempty"`
	Skills           []PluginSkillInstallView `json:"skills,omitempty"`
	EnvStatus        []PluginEnvStatus        `json:"env_status,omitempty"`
}

// PluginMCPServiceView is the part of a plugin MCP service anyone who can see
// the plugin may see. It carries no secret and no header value.
type PluginMCPServiceView struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description,omitempty"`
	URL           string                 `json:"url,omitempty"`
	TransportType types.MCPTransportType `json:"transport_type"`
	Enabled       bool                   `json:"enabled"`
	AuthConfig    PluginMCPAuthView      `json:"auth_config"`
}

// PluginMCPAuthView names the auth strategy without any credential.
type PluginMCPAuthView struct {
	AuthType types.MCPAuthType `json:"auth_type,omitempty"`
}

func pluginMCPServiceView(svc *types.MCPService) PluginMCPServiceView {
	view := PluginMCPServiceView{
		ID: svc.ID, Name: svc.Name, Description: svc.Description,
		TransportType: svc.TransportType, Enabled: svc.Enabled,
	}
	if svc.URL != nil {
		view.URL = *svc.URL
	}
	if svc.AuthConfig != nil {
		view.AuthConfig = PluginMCPAuthView{AuthType: svc.AuthConfig.AuthType}
	}
	return view
}

// PluginSkillInstallView is one plugin skill installed on one sandbox.
type PluginSkillInstallView struct {
	CatalogID       string    `json:"catalog_id"`
	SkillName       string    `json:"skill_name"`
	SkillID         string    `json:"skill_id"`
	SandboxConfigID string    `json:"sandbox_config_id"`
	Status          string    `json:"status"`
	Error           string    `json:"error,omitempty"`
	Version         string    `json:"version,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// PluginEnvStatus describes whether a declared env var is configured.
type PluginEnvStatus struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	IsSet       bool   `json:"is_set"`
}

// PluginService coordinates marketplaces, plugins, resources, and sandboxes.
type PluginService struct {
	repo        interfaces.PluginRepository
	mcpSvc      interfaces.MCPServiceService
	skillSvc    *TenantSkillService
	files       interfaces.FileService
	configsRepo repository.TenantSandboxConfigRepository
	mcpRepo     interfaces.MCPServiceRepository
	skillRepo   repository.TenantSkillRepository
	oauth       interfaces.MCPOAuthRepository
	sourceHTTP  *http.Client
}

// NewPluginService creates a new PluginService instance.
func NewPluginService(
	repo interfaces.PluginRepository,
	mcpSvc interfaces.MCPServiceService,
	skillSvc *TenantSkillService,
	files interfaces.FileService,
	configsRepo repository.TenantSandboxConfigRepository,
	mcpRepo interfaces.MCPServiceRepository,
	skillRepo repository.TenantSkillRepository,
	oauth interfaces.MCPOAuthRepository,
) *PluginService {
	return &PluginService{
		repo:        repo,
		mcpSvc:      mcpSvc,
		skillSvc:    skillSvc,
		files:       files,
		configsRepo: configsRepo,
		mcpRepo:     mcpRepo,
		skillRepo:   skillRepo,
		oauth:       oauth,
	}
}

// releaseMarketplace deletes a shared marketplace, its packages and their
// stored bundles once no tenant binding refers to them.
func (s *PluginService) releaseMarketplace(ctx context.Context, id string) {
	deleted, bundleRefs, err := s.repo.DeleteMarketplaceIfUnreferenced(ctx, id)
	if err != nil {
		logger.Warnf(ctx, "[plugin] release marketplace %s failed: %v", id, err)
		return
	}
	if !deleted || s.files == nil {
		return
	}
	s.deleteBundles(ctx, bundleRefs)
}

func (s *PluginService) deleteBundles(ctx context.Context, refs []string) {
	for _, ref := range refs {
		if dErr := s.files.DeleteFile(ctx, ref); dErr != nil {
			logger.Warnf(ctx, "[plugin] delete stored bundle %s failed: %v", ref, dErr)
		}
	}
}

// AddMarketplaceFromArchive parses an archive and binds the tenant to the
// shared marketplace for it, creating that marketplace when no tenant has
// imported the same bytes yet. The archive and skill zips are stored as
// resources of the first tenant to upload those bytes. Re-importing a
// marketplace name the tenant already holds moves the tenant (and its
// installs) to the new archive.
func (s *PluginService) AddMarketplaceFromArchive(
	ctx context.Context, tenantID uint64, archive []byte,
) (*types.PluginMarketplaceEntity, error) {
	if len(archive) == 0 {
		return nil, apperrors.NewBadRequestError("archive cannot be empty")
	}

	files, err := UnpackMarketplaceArchive(archive)
	if err != nil {
		return nil, err
	}

	parsed, err := ParsePluginMarketplace(files)
	if err != nil {
		return nil, err
	}

	manifestBytes, err := json.Marshal(parsed.Manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}

	sum := sha256.Sum256(archive)
	bundleSHA := hex.EncodeToString(sum[:])

	current, err := s.repo.GetMarketplaceByName(ctx, tenantID, parsed.Manifest.Name)
	if err != nil {
		return nil, err
	}
	if current != nil && current.BundleSHA256 == bundleSHA {
		return withImportWarnings(current, parsed.Warnings), nil
	}

	shared, err := s.repo.GetMarketplaceByNameSHA(ctx, parsed.Manifest.Name, bundleSHA)
	if err != nil {
		return nil, err
	}
	createdHere := false
	if shared == nil {
		shared, createdHere, err = s.createSharedMarketplace(
			ctx,
			tenantID,
			parsed,
			string(manifestBytes),
			archive,
			bundleSHA,
		)
		if err != nil {
			return nil, err
		}
	}

	if current == nil {
		err = s.repo.BindMarketplace(ctx, tenantID, shared)
	} else {
		err = s.repo.SwapMarketplaceBinding(ctx, tenantID, current.ID, shared)
	}
	// The row we just inserted has no binding when this fails. Drop it here;
	// a marketplace another tenant already held is left for that tenant.
	if err != nil && createdHere {
		s.releaseMarketplace(ctx, shared.ID)
	}
	if errors.Is(err, repository.ErrPluginMarketplaceGone) {
		return nil, apperrors.NewConflictError("市场正在被清理，请重试导入")
	}
	if errors.Is(err, repository.ErrPluginMarketplaceNameTaken) ||
		errors.Is(err, repository.ErrPluginMarketplaceNotBound) {
		// A concurrent import of the same name won. Identical bytes mean this
		// import is already done.
		if winner, wErr := s.repo.GetMarketplaceByName(ctx, tenantID, parsed.Manifest.Name); wErr == nil &&
			winner != nil && winner.BundleSHA256 == bundleSHA {
			return winner, nil
		}
		return nil, apperrors.NewConflictError("同名市场正在被同时导入或删除，请刷新后重试")
	}
	var missing *repository.PluginMissingError
	if errors.As(err, &missing) {
		return nil, apperrors.NewConflictError(fmt.Sprintf(
			"新版本市场不再包含已安装的插件 %q，请先卸载该插件再导入", missing.PluginName))
	}
	if err != nil {
		return nil, err
	}
	if current != nil {
		s.releaseMarketplace(ctx, current.ID)
	}
	return withImportWarnings(shared, parsed.Warnings), nil
}

func withImportWarnings(mp *types.PluginMarketplaceEntity, warnings []string) *types.PluginMarketplaceEntity {
	if mp == nil || len(warnings) == 0 {
		return mp
	}
	mp.ImportWarnings = append([]string(nil), warnings...)
	return mp
}

// createSharedMarketplace stores the archive and every repackaged skill zip
// under the first tenant that uploaded this exact archive, then inserts the
// marketplace with one package per plugin. Later tenants reuse that row and
// those files. When another tenant inserted the same (name, sha256)
// concurrently, that row wins and the copies stored here are dropped.
func (s *PluginService) createSharedMarketplace(
	ctx context.Context, tenantID uint64, parsed *ParsedMarketplace, manifest string, archive []byte, bundleSHA string,
) (*types.PluginMarketplaceEntity, bool, error) {
	if s.files == nil {
		return nil, false, errors.New("file service is not configured")
	}
	if tenantID == 0 {
		return nil, false, errors.New("marketplace archive requires a tenant")
	}
	var stored []string
	save := func(data []byte, name string) (string, error) {
		ref, err := s.files.SaveBytes(ctx, data, tenantID, name, false)
		if err != nil {
			return "", err
		}
		stored = append(stored, ref)
		return ref, nil
	}

	bundleRef, err := save(archive, fmt.Sprintf("plugin-marketplace-%s.bin", bundleSHA))
	if err != nil {
		return nil, false, fmt.Errorf("store marketplace archive: %w", err)
	}

	now := time.Now()
	pkgs := make([]*types.PluginPackageEntity, 0, len(parsed.Order))
	for position, name := range parsed.Order {
		plugin := parsed.Plugins[name]
		skills := make(types.PluginPackageSkills, 0, len(plugin.Skills))
		for _, skill := range plugin.Skills {
			ref, sErr := save(skill.Archive, fmt.Sprintf("plugin-skill-%s-%s.zip", bundleSHA[:16], skill.PrefixedName))
			if sErr != nil {
				s.deleteBundles(ctx, stored)
				return nil, false, fmt.Errorf("store skill %s: %w", skill.PrefixedName, sErr)
			}
			skills = append(skills, types.PluginPackageSkill{
				Name: skill.OriginalName, PrefixedName: skill.PrefixedName, BundleRef: ref,
			})
		}
		pkgs = append(pkgs, &types.PluginPackageEntity{
			ID:              uuid.NewString(),
			Name:            plugin.Name,
			Position:        position,
			Version:         plugin.Version,
			Description:     plugin.Description,
			Category:        plugin.Category,
			Keywords:        plugin.Keywords,
			RequiresSandbox: plugin.RequiresSandbox,
			EnvDeclarations: plugin.EnvDeclarations,
			MCPServers:      plugin.MCPServers,
			MCPWarnings:     plugin.MCPWarnings,
			Skills:          skills,
			CreatedAt:       now,
		})
	}

	entity := &types.PluginMarketplaceEntity{
		ID:           uuid.NewString(),
		Name:         parsed.Manifest.Name,
		Description:  parsed.Manifest.Metadata.Description,
		Version:      parsed.Manifest.Metadata.Version,
		OwnerName:    parsed.Manifest.Owner.Name,
		OwnerEmail:   parsed.Manifest.Owner.Email,
		Manifest:     manifest,
		BundleRef:    bundleRef,
		BundleSHA256: bundleSHA,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	createErr := s.repo.CreateMarketplace(ctx, entity, pkgs)
	if createErr == nil {
		return entity, true, nil
	}
	s.deleteBundles(ctx, stored)
	winner, err := s.repo.GetMarketplaceByNameSHA(ctx, parsed.Manifest.Name, bundleSHA)
	if err != nil {
		return nil, false, err
	}
	if winner == nil {
		return nil, false, createErr
	}
	return winner, false, nil
}

// AddMarketplaceFromSource downloads from a remote source and adds the marketplace.
func (s *PluginService) AddMarketplaceFromSource(
	ctx context.Context, tenantID uint64, source string,
) (*types.PluginMarketplaceEntity, error) {
	archive, err := FetchMarketplaceArchive(ctx, source, s.sourceHTTP)
	if err != nil {
		return nil, err
	}
	return s.AddMarketplaceFromArchive(ctx, tenantID, archive)
}

// ListMarketplaces returns all marketplaces with plugin summary information.
func (s *PluginService) ListMarketplaces(ctx context.Context, tenantID uint64) ([]MarketplaceDetailView, error) {
	mps, err := s.repo.ListMarketplaces(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	installs, err := s.repo.ListInstalls(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	out := make([]MarketplaceDetailView, 0, len(mps))
	for _, mp := range mps {
		if mp == nil {
			continue
		}
		view, err := s.buildMarketplaceView(ctx, mp, installs)
		if err != nil {
			logger.Warnf(ctx, "[plugin] build marketplace view %s failed: %v", mp.Name, err)
			continue
		}
		out = append(out, *view)
	}
	return out, nil
}

// GetMarketplace returns the detailed view of one marketplace.
func (s *PluginService) GetMarketplace(
	ctx context.Context,
	tenantID uint64,
	id string,
) (*MarketplaceDetailView, error) {
	mp, err := s.repo.GetMarketplaceByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if mp == nil {
		return nil, apperrors.NewNotFoundError("marketplace not found")
	}
	installs, err := s.repo.ListInstallsByMarketplace(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return s.buildMarketplaceView(ctx, mp, installs)
}

func (s *PluginService) buildMarketplaceView(
	ctx context.Context, mp *types.PluginMarketplaceEntity, installs []*types.PluginInstall,
) (*MarketplaceDetailView, error) {
	pkgs, err := s.repo.ListPackages(ctx, mp.ID)
	if err != nil {
		return nil, err
	}
	installByPackage := make(map[string]*types.PluginInstall, len(installs))
	for _, inst := range installs {
		if inst != nil {
			installByPackage[inst.PackageID] = inst
		}
	}

	plugins := make([]MarketplacePluginView, 0, len(pkgs))
	for _, pkg := range pkgs {
		pv := MarketplacePluginView{
			Name:            pkg.Name,
			Description:     pkg.Description,
			Version:         pkg.Version,
			Category:        pkg.Category,
			Keywords:        pkg.Keywords,
			SkillCount:      len(pkg.Skills),
			MCPCount:        len(pkg.MCPServers),
			RequiresSandbox: pkg.RequiresSandbox,
		}
		if inst, ok := installByPackage[pkg.ID]; ok {
			pv.Installed = true
			pv.InstallID = inst.ID
			pv.InstalledVersion = inst.Version
			pv.Status = inst.Status
			pv.Error = inst.Error
		}
		plugins = append(plugins, pv)
	}

	return &MarketplaceDetailView{
		ID:          mp.ID,
		Name:        mp.Name,
		Description: mp.Description,
		Version:     mp.Version,
		OwnerName:   mp.OwnerName,
		OwnerEmail:  mp.OwnerEmail,
		CreatedAt:   mp.CreatedAt,
		UpdatedAt:   mp.UpdatedAt,
		Plugins:     plugins,
	}, nil
}

// tenantPackage resolves a plugin package of a marketplace the tenant holds.
func (s *PluginService) tenantPackage(
	ctx context.Context, tenantID uint64, marketplaceID, pluginName string,
) (*types.PluginPackageEntity, error) {
	mp, err := s.repo.GetMarketplaceByID(ctx, tenantID, marketplaceID)
	if err != nil {
		return nil, err
	}
	if mp == nil {
		return nil, apperrors.NewNotFoundError("marketplace not found")
	}
	pkg, err := s.repo.GetPackage(ctx, marketplaceID, pluginName)
	if err != nil {
		return nil, err
	}
	if pkg == nil {
		return nil, apperrors.NewNotFoundError(fmt.Sprintf("plugin %q not found in marketplace", pluginName))
	}
	return pkg, nil
}

// PreflightPlugin returns the environment variable declarations and metadata for installing a plugin.
func (s *PluginService) PreflightPlugin(
	ctx context.Context, tenantID uint64, marketplaceID, pluginName string,
) (*PluginPreflightResponse, error) {
	pkg, err := s.tenantPackage(ctx, tenantID, marketplaceID, pluginName)
	if err != nil {
		return nil, err
	}
	return &PluginPreflightResponse{
		PluginName:      pkg.Name,
		SkillCount:      len(pkg.Skills),
		MCPCount:        len(pkg.MCPServers),
		RequiresSandbox: pkg.RequiresSandbox,
		EnvDeclarations: pkg.EnvDeclarations,
	}, nil
}

// loadPackageSkill reads a stored skill zip and parses it back into a bundle.
func (s *PluginService) loadPackageSkill(
	ctx context.Context,
	skill types.PluginPackageSkill,
) (*SkillBundle, []byte, error) {
	if s.files == nil {
		return nil, nil, errors.New("file service is not configured")
	}
	rc, err := s.files.GetFile(ctx, skill.BundleRef)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rc.Close() }()
	archive, err := io.ReadAll(io.LimitReader(rc, maxSkillBundleTotalBytes+1))
	if err != nil {
		return nil, nil, err
	}
	bundle, err := ParseSkillBundle(archive)
	if err != nil {
		return nil, nil, err
	}
	return bundle, archive, nil
}

// DeleteMarketplace removes the tenant's binding to a marketplace once none
// of its plugins are installed in the tenant. The shared row and archive go
// away with the last binding.
func (s *PluginService) DeleteMarketplace(ctx context.Context, tenantID uint64, id string) error {
	err := s.repo.UnbindMarketplaceIfUnused(ctx, tenantID, id)
	switch {
	case errors.Is(err, repository.ErrPluginMarketplaceNotBound):
		return apperrors.NewNotFoundError("marketplace not found")
	case errors.Is(err, repository.ErrPluginMarketplaceInUse):
		return apperrors.NewConflictError("无法删除市场：仍有已安装的插件，请先卸载所有插件")
	case err != nil:
		return err
	}
	s.releaseMarketplace(ctx, id)
	return nil
}

// pluginMCPServiceName is "<marketplace>__<server>", the workspace name of an
// MCP server shipped by a marketplace plugin.
func pluginMCPServiceName(marketplaceName, serverName string) string {
	return marketplaceName + "__" + serverName
}

// InstallPlugin installs a single plugin from a marketplace.
func (s *PluginService) InstallPlugin(
	ctx context.Context,
	tenantID uint64,
	marketplaceID string,
	pluginName string,
	sandboxConfigIDs []string,
	userEnv map[string]string,
) (*PluginInstallResult, error) {
	plugin, err := s.tenantPackage(ctx, tenantID, marketplaceID, pluginName)
	if err != nil {
		return nil, err
	}
	marketplace, err := s.repo.GetMarketplaceByID(ctx, tenantID, marketplaceID)
	if err != nil {
		return nil, err
	}
	if marketplace == nil {
		return nil, apperrors.NewNotFoundError("marketplace not found")
	}
	sandboxConfigIDs = uniqueNonEmptyStrings(sandboxConfigIDs)

	// 1. Sandbox Pre-checks
	if plugin.RequiresSandbox {
		if len(sandboxConfigIDs) == 0 {
			return nil, apperrors.NewBadRequestError("该插件包含技能，至少需要指定一个沙箱目标")
		}

		// Check if the workspace has any usable sandbox config at all
		allConfigs, cfgErr := s.configsRepo.ListByTenant(ctx, tenantID)
		if cfgErr != nil {
			return nil, cfgErr
		}
		if s.skillSvc == nil {
			return nil, errors.New("skill service is not configured")
		}

		hasAnyUsableTarget := false
		if s.skillSvc.host.Desktop && s.skillSvc.host.SkillsAvailable() {
			hasAnyUsableTarget = true
		} else if len(allConfigs) > 0 {
			hasAnyUsableTarget = true
		}
		if !hasAnyUsableTarget {
			return nil, apperrors.NewConflictError("sandbox_required: 工作区未配置任何可用沙箱")
		}

		for _, cfgID := range sandboxConfigIDs {
			if reqErr := s.skillSvc.RequireSkillTarget(ctx, tenantID, cfgID); reqErr != nil {
				return nil, apperrors.NewBadRequestError(fmt.Sprintf("沙箱目标 %q 无效或不存在: %v", cfgID, reqErr))
			}
		}
	}

	// 2. Variable and Name Checks
	if userEnv == nil {
		userEnv = make(map[string]string)
	}
	var missingRequired []string
	for _, decl := range plugin.EnvDeclarations {
		if decl.Required {
			if val, ok := userEnv[decl.Name]; !ok || strings.TrimSpace(val) == "" {
				missingRequired = append(missingRequired, decl.Name)
			}
		}
	}
	if len(missingRequired) > 0 {
		return nil, apperrors.NewBadRequestError(fmt.Sprintf("缺少必填环境变量: %s", strings.Join(missingRequired, ", ")))
	}

	// Check plugin name collision across marketplaces in this workspace
	existingInstall, err := s.repo.GetInstallByPluginName(ctx, tenantID, pluginName)
	if err != nil {
		return nil, err
	}
	if existingInstall != nil {
		if existingInstall.MarketplaceID == marketplaceID {
			return nil, apperrors.NewConflictError(fmt.Sprintf("插件 %q 已安装", pluginName))
		}
		holder := existingInstall.MarketplaceID
		if other, mErr := s.repo.GetMarketplaceByID(ctx, tenantID, existingInstall.MarketplaceID); mErr == nil &&
			other != nil {
			holder = other.Name
		}
		return nil, apperrors.NewConflictError(fmt.Sprintf(
			"plugin_name_taken: 插件名 %q 已由市场 %q 安装，请先在该市场卸载", pluginName, holder))
	}

	// Check name collision for each MCP and Catalog skill
	if len(plugin.MCPServers) > 0 {
		existingMCPs, lErr := s.mcpRepo.List(ctx, tenantID)
		if lErr != nil {
			return nil, lErr
		}
		taken := make(map[string]bool, len(existingMCPs))
		for _, m := range existingMCPs {
			if m != nil {
				taken[m.Name] = true
			}
		}
		for mcpName := range plugin.MCPServers {
			prefixedMCPName := pluginMCPServiceName(marketplace.Name, mcpName)
			if taken[prefixedMCPName] {
				return nil, apperrors.NewConflictError(fmt.Sprintf("MCP 服务名 %q 已存在，无法安装插件", prefixedMCPName))
			}
		}
	}

	for _, skill := range plugin.Skills {
		existingCat, cErr := s.skillRepo.GetCatalogByName(ctx, tenantID, skill.PrefixedName)
		if cErr == nil && existingCat != nil {
			return nil, apperrors.NewConflictError(fmt.Sprintf("技能目录名 %q 已存在，无法安装插件", skill.PrefixedName))
		}
	}

	// 3. Create the kind=plugin binding that represents the install
	installID := uuid.NewString()
	now := time.Now()
	installEntity := &types.PluginInstall{
		ID:               installID,
		TenantID:         tenantID,
		MarketplaceID:    marketplaceID,
		PackageID:        plugin.ID,
		PluginName:       pluginName,
		Version:          plugin.Version,
		Description:      plugin.Description,
		SandboxConfigIDs: types.PluginSandboxConfigIDs(sandboxConfigIDs),
		Status:           types.PluginStatusInstalling,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.repo.CreateInstall(ctx, installEntity); err != nil {
		if errors.Is(err, repository.ErrPluginInstallExists) {
			return nil, apperrors.NewConflictError(fmt.Sprintf("插件 %q 已安装", pluginName))
		}
		if errors.Is(err, repository.ErrPluginMarketplaceNotBound) {
			return nil, apperrors.NewNotFoundError("marketplace not found")
		}
		return nil, err
	}

	// Past this point every failure tears down what was created, exactly as
	// an uninstall does, and returns the cause.
	fail := func(cause error) (*PluginInstallResult, error) {
		logger.Warnf(ctx, "[plugin] installation of %s failed, rolling back: %v", pluginName, cause)
		if tErr := s.teardownInstall(ctx, tenantID, installEntity); tErr != nil {
			logger.Warnf(ctx, "[plugin] roll back install %s failed: %v", installID, tErr)
		}
		return nil, cause
	}

	// 4. Create MCP Services synchronously
	var createdMCPServices []*types.MCPService
	for srvName, srvDef := range plugin.MCPServers {
		prefixedName := pluginMCPServiceName(marketplace.Name, srvName)
		mcpType := types.MCPTransportHTTPStreamable
		if strings.EqualFold(srvDef.Type, "sse") {
			mcpType = types.MCPTransportSSE
		}

		headers, authConfig := pluginMCPAuth(srvDef.Headers, userEnv)
		serverURL := srvDef.URL

		mcpEntity := &types.MCPService{
			ID:            uuid.NewString(),
			TenantID:      tenantID,
			Name:          prefixedName,
			Description:   fmt.Sprintf("From plugin %s in marketplace %s", pluginName, marketplace.Name),
			TransportType: mcpType,
			URL:           &serverURL,
			Headers:       headers,
			AuthConfig:    authConfig,
			Enabled:       true,
			CreatedAt:     now,
			UpdatedAt:     now,
		}

		if err := s.mcpSvc.CreateMCPService(ctx, mcpEntity); err != nil {
			return fail(fmt.Errorf("create MCP service %s: %w", prefixedName, err))
		}
		createdMCPServices = append(createdMCPServices, mcpEntity)

		res := &types.PluginTenantBindingEntity{
			ID:              uuid.NewString(),
			TenantID:        tenantID,
			PluginInstallID: installID,
			Kind:            types.PluginResourceKindMCPService,
			ResourceID:      mcpEntity.ID,
			ResourceName:    mcpEntity.Name,
			CreatedAt:       now,
		}
		if err := s.repo.CreateBinding(ctx, res); err != nil {
			// The teardown only sees bound resources.
			_ = s.mcpSvc.DeleteMCPServiceUnguarded(ctx, tenantID, mcpEntity.ID)
			return fail(err)
		}
	}

	// 5. Create Skill Catalogs and trigger installation
	envVars := pluginSkillEnvs(plugin.EnvDeclarations, userEnv)
	var createdCatalogs []*types.TenantSkillCatalogEntity
	for _, skill := range plugin.Skills {
		bundle, skillArchive, err := s.loadPackageSkill(ctx, skill)
		if err != nil {
			return fail(fmt.Errorf("load skill %s: %w", skill.PrefixedName, err))
		}
		catEntity, err := s.skillSvc.UpsertCatalogFromBundle(ctx, tenantID, bundle, skillArchive, true)
		if err != nil {
			return fail(fmt.Errorf("upsert skill catalog %s: %w", skill.PrefixedName, err))
		}
		createdCatalogs = append(createdCatalogs, catEntity)

		res := &types.PluginTenantBindingEntity{
			ID:              uuid.NewString(),
			TenantID:        tenantID,
			PluginInstallID: installID,
			Kind:            types.PluginResourceKindSkillCatalog,
			ResourceID:      catEntity.ID,
			ResourceName:    catEntity.Name,
			CreatedAt:       now,
		}
		if err := s.repo.CreateBinding(ctx, res); err != nil {
			// The teardown only sees bound resources.
			_ = s.skillSvc.DeleteCatalogUnguarded(ctx, tenantID, catEntity.ID)
			return fail(err)
		}

		resInstall, err := s.skillSvc.InstallCatalogToConfigs(ctx, tenantID, catEntity.ID, sandboxConfigIDs)
		if resInstall != nil {
			s.injectPluginEnvs(ctx, tenantID, resInstall.Installs, envVars)
		}
		if iErr := catalogInstallError(resInstall, err); iErr != nil {
			return fail(fmt.Errorf("install skill %s: %w", skill.PrefixedName, iErr))
		}
	}

	// 6. Probe MCP OAuth in parallel (short timeout)
	mcpOAuthStates := make(map[string]string, len(createdMCPServices))
	s.probeMCPServicesOAuth(ctx, tenantID, createdMCPServices, mcpOAuthStates)

	// 7. Settle the status from what the sandbox installs report so far.
	// An MCP that was switched to OAuth and has no token yet is not ready.
	status := s.refreshPluginStatus(ctx, tenantID, installID)
	if status == "" {
		status = installEntity.Status
	}
	if status == types.PluginStatusReady && servicesAwaitingOAuth(createdMCPServices, mcpOAuthStates) {
		if err := s.repo.UpdateInstallStatus(ctx, tenantID, installID, func(
			*types.PluginInstall, []interfaces.PluginCatalogInstalls,
		) (string, string, bool) {
			return types.PluginStatusNeedsOAuth, "", true
		}); err != nil {
			logger.Warnf(ctx, "[plugin] mark install %s as needing oauth failed: %v", installID, err)
		} else {
			status = types.PluginStatusNeedsOAuth
		}
	}

	mcpNames := make([]string, 0, len(createdMCPServices))
	for _, m := range createdMCPServices {
		mcpNames = append(mcpNames, m.Name)
	}
	skillNames := make([]string, 0, len(createdCatalogs))
	for _, c := range createdCatalogs {
		skillNames = append(skillNames, c.Name)
	}

	return &PluginInstallResult{
		InstallID:         installID,
		Status:            status,
		MCPServers:        mcpNames,
		Skills:            skillNames,
		MCPOAuthStates:    mcpOAuthStates,
		Warnings:          plugin.MCPWarnings,
		AsyncInstallation: plugin.RequiresSandbox,
	}, nil
}

// catalogInstallError turns the per-sandbox failures of InstallCatalogToConfigs
// into one error. InstallCatalogToConfigs reports success as soon as one
// sandbox accepted the install, so the per-sandbox errors must be checked too.
// Their messages are already safe to show to the user.
func catalogInstallError(res *CatalogInstallResult, err error) error {
	if res != nil && len(res.Errors) > 0 {
		parts := make([]string, 0, len(res.Errors))
		for cfgID, msg := range res.Errors {
			parts = append(parts, fmt.Sprintf("%s: %s", cfgID, msg))
		}
		slices.Sort(parts)
		return apperrors.NewInternalServerError("沙箱安装失败: " + strings.Join(parts, "; "))
	}
	return err
}

// pluginSkillEnvs builds the plugin-sourced env vars written to every sandbox
// install of a plugin skill. The values entered at install become the
// workspace values; each member can still override them with their own.
func pluginSkillEnvs(decls []types.PluginEnvVar, values map[string]string) types.SkillEnvVars {
	if len(decls) == 0 {
		return nil
	}
	out := make(types.SkillEnvVars, 0, len(decls))
	for _, decl := range decls {
		out = append(out, types.SkillEnvVar{
			Name:        decl.Name,
			Description: decl.Description,
			Required:    decl.Required,
			Source:      SkillEnvVarSourcePlugin,
			Value:       values[decl.Name],
		})
	}
	return out
}

// injectPluginEnvs writes the plugin env vars onto sandbox installs, keyed by
// sandbox config ID.
func (s *PluginService) injectPluginEnvs(
	ctx context.Context, tenantID uint64, installs map[string]string, envs types.SkillEnvVars,
) {
	if len(envs) == 0 {
		return
	}
	for cfgID, skillID := range installs {
		if err := s.skillRepo.UpdateSkillEnvs(ctx, tenantID, cfgID, skillID, envs); err != nil {
			logger.Warnf(ctx, "[plugin] write env vars to skill %s on %s failed: %v", skillID, cfgID, err)
		}
	}
}

// teardownInstall removes everything an install owns, then deletes the install
// and its ledger rows. MCP services go now. Skill files leave the sandbox in
// the background; the catalog row is deleted once those installs are gone.
// Skill and MCP deletes do not write the ledger.
func (s *PluginService) teardownInstall(
	ctx context.Context, tenantID uint64, install *types.PluginInstall,
) error {
	resources, err := s.repo.ListBindingsByInstall(ctx, tenantID, install.ID)
	if err != nil {
		return err
	}
	for _, res := range resources {
		switch res.Kind {
		case types.PluginResourceKindMCPService:
			if dErr := s.mcpSvc.DeleteMCPServiceUnguarded(ctx, tenantID, res.ResourceID); dErr != nil {
				logger.Warnf(ctx, "[plugin] delete MCP service %s failed: %v", res.ResourceID, dErr)
			}
		case types.PluginResourceKindSkillCatalog:
			s.discardPluginCatalog(ctx, tenantID, res.ResourceID)
		}
	}
	if err := s.repo.DeleteInstall(ctx, tenantID, install.ID); err != nil {
		return err
	}
	logger.Infof(ctx, "[plugin] uninstalled %s", install.ID)
	return nil
}

// discardPluginCatalog asks the skill service to delete the catalog once its
// sandbox installs are gone, and starts those removals. The install row is
// deleted by the caller whether or not the files have left the sandbox yet.
func (s *PluginService) discardPluginCatalog(ctx context.Context, tenantID uint64, catalogID string) {
	if s.skillSvc == nil {
		logger.Warnf(ctx, "[plugin] skill service is not configured; catalog %s was not removed", catalogID)
		return
	}
	s.skillSvc.DiscardPluginCatalog(ctx, tenantID, catalogID)
	if s.skillRepo == nil {
		return
	}
	installs, err := s.skillRepo.ListSkillsByCatalog(ctx, tenantID, catalogID)
	if err != nil {
		logger.Warnf(ctx, "[plugin] list installs of catalog %s failed: %v", catalogID, err)
		return
	}
	for _, sk := range installs {
		if sk == nil {
			continue
		}
		if rErr := s.skillSvc.RemoveSkillUnguarded(ctx, tenantID, sk.SandboxConfigID, sk.ID); rErr != nil {
			logger.Warnf(ctx, "[plugin] remove skill %s from %s failed: %v", sk.ID, sk.SandboxConfigID, rErr)
		}
	}
}

// probeMCPServicesOAuth probes MCP services to see if RFC 9728 OAuth challenge
// is returned. A service without a static credential is switched to OAuth so
// each member authorizes on their own; a configured bearer or API key is kept.
func (s *PluginService) probeMCPServicesOAuth(
	ctx context.Context, tenantID uint64, services []*types.MCPService, statesOut map[string]string,
) {
	if len(services) == 0 {
		return
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, svc := range services {
		wg.Add(1)
		go func(service *types.MCPService) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()

			testRes, err := s.mcpSvc.TestMCPService(probeCtx, tenantID, service.ID)
			state := "ready"
			if err != nil {
				state = "unknown"
			} else if testRes != nil && testRes.OAuthRequired {
				state = "needs_oauth"
				if !hasStaticMCPCredential(service.AuthConfig) {
					if service.AuthConfig == nil {
						service.AuthConfig = &types.MCPAuthConfig{}
					}
					service.AuthConfig.AuthType = types.MCPAuthOAuth
					_ = s.mcpSvc.UpdateMCPService(ctx, service, map[string]bool{"auth_type": true})
				}
			} else if testRes != nil && !testRes.Success {
				state = "unknown"
			} else if testRes != nil {
				// The probe already listed tools. Keep that directory so the
				// service is usable without opening its detail page.
				s.rememberProbeTools(ctx, tenantID, service, testRes)
			}

			mu.Lock()
			statesOut[service.Name] = state
			mu.Unlock()
		}(svc)
	}

	wg.Wait()
}

func hasStaticMCPCredential(ac *types.MCPAuthConfig) bool {
	return ac != nil && (ac.Token != "" || ac.APIKey != "")
}

// rememberProbeTools stores the directory a successful install probe already
// listed. A failed save leaves the service installed; the detail page can
// fetch the directory again.
func (s *PluginService) rememberProbeTools(
	ctx context.Context, tenantID uint64, service *types.MCPService, result *types.MCPTestResult,
) {
	if s == nil || service == nil || result == nil || !result.Success || result.OAuthRequired {
		return
	}
	meta, ok := s.mcpSvc.(interface {
		PersistMCPMetadata(context.Context, uint64, string, []*types.MCPTool, string) error
	})
	if !ok || meta == nil {
		return
	}
	saveCtx := context.WithoutCancel(ctx)
	if err := meta.PersistMCPMetadata(saveCtx, tenantID, service.ID, result.Tools, ""); err != nil {
		logger.Warnf(ctx, "[plugin] save MCP tools for %s failed: %v", service.Name, err)
	}
}

// servicesAwaitingOAuth reports whether the probe switched a service to OAuth
// because it had no static credential. A needs_oauth probe on a service that
// kept its bearer or API key does not block the install.
func servicesAwaitingOAuth(services []*types.MCPService, states map[string]string) bool {
	for _, svc := range services {
		if svc == nil || states[svc.Name] != "needs_oauth" || !svc.AuthConfig.IsOAuth() {
			continue
		}
		return true
	}
	return false
}

// NoteMCPAuthorized recomputes the plugin install that owns serviceID after
// a token is stored or revoked. Skills that are still installing stay
// installing; once they are ready, a missing token keeps the install at
// needs_oauth and a stored token promotes it to ready.
func (s *PluginService) NoteMCPAuthorized(ctx context.Context, tenantID uint64, serviceID string) {
	if s == nil || s.repo == nil || serviceID == "" {
		return
	}
	info, err := s.repo.LookupPluginByResource(ctx, tenantID, types.PluginResourceKindMCPService, serviceID)
	if err != nil {
		logger.Warnf(ctx, "[plugin] look up owner of MCP %s failed: %v", serviceID, err)
		return
	}
	if info == nil {
		return
	}
	s.refreshPluginStatus(ctx, tenantID, info.PluginInstallID)
}

// mcpServiceTokenPresence is implemented by the OAuth repository. It is not
// part of MCPOAuthRepository so token storage can grow without forcing every
// test fake to add a method; the concrete repository is what dig injects.
type mcpServiceTokenPresence interface {
	ServiceHasToken(ctx context.Context, tenantID uint64, serviceID string) (bool, error)
}

// installOAuthPending reports whether an install still has an OAuth MCP
// service with no stored access token.
func (s *PluginService) installOAuthPending(ctx context.Context, tenantID uint64, installID string) bool {
	if s.repo == nil || s.mcpRepo == nil {
		return false
	}
	resources, err := s.repo.ListBindingsByInstall(ctx, tenantID, installID)
	if err != nil {
		logger.Warnf(ctx, "[plugin] list bindings of install %s failed: %v", installID, err)
		return false
	}
	for _, res := range resources {
		if res == nil || res.Kind != types.PluginResourceKindMCPService {
			continue
		}
		svc, err := s.mcpRepo.GetByID(ctx, tenantID, res.ResourceID)
		if err != nil || svc == nil || !svc.AuthConfig.IsOAuth() {
			continue
		}
		checker, ok := s.oauth.(mcpServiceTokenPresence)
		if !ok || checker == nil {
			return true
		}
		has, err := checker.ServiceHasToken(ctx, tenantID, svc.ID)
		if err != nil {
			logger.Warnf(ctx, "[plugin] look up oauth token for %s failed: %v", svc.ID, err)
			return true
		}
		if !has {
			return true
		}
	}
	return false
}

// ProbePluginMCPOAuth re-runs OAuth probe on installed MCP services of a plugin.
func (s *PluginService) ProbePluginMCPOAuth(
	ctx context.Context, tenantID uint64, marketplaceID, pluginName string,
) (map[string]string, error) {
	install, err := s.repo.GetInstallByName(ctx, tenantID, marketplaceID, pluginName)
	if err != nil {
		return nil, err
	}
	if install == nil {
		return nil, apperrors.NewNotFoundError("installed plugin not found")
	}

	resources, err := s.repo.ListBindingsByInstall(ctx, tenantID, install.ID)
	if err != nil {
		return nil, err
	}

	var mcpServices []*types.MCPService
	for _, res := range resources {
		if res.Kind == types.PluginResourceKindMCPService {
			svc, sErr := s.mcpRepo.GetByID(ctx, tenantID, res.ResourceID)
			if sErr == nil && svc != nil {
				mcpServices = append(mcpServices, svc)
			}
		}
	}

	states := make(map[string]string, len(mcpServices))
	s.probeMCPServicesOAuth(ctx, tenantID, mcpServices, states)
	return states, nil
}

// pluginMCPAuth turns the header templates of a plugin MCP entry into plain
// headers and an auth config. The variable header goes into the encrypted
// auth config, never into the plaintext headers column. ParsePluginMarketplace
// has already rejected every other template shape.
func pluginMCPAuth(templates map[string]string, env map[string]string) (types.MCPHeaders, *types.MCPAuthConfig) {
	auth := &types.MCPAuthConfig{AuthType: types.MCPAuthNone}
	headers := make(types.MCPHeaders)
	for name, tmpl := range templates {
		match := varRegex.FindStringSubmatch(tmpl)
		if match == nil {
			headers[name] = tmpl
			continue
		}
		varName := match[1]
		switch {
		case strings.EqualFold(name, "Authorization") && tmpl == "Bearer ${"+varName+"}":
			auth.AuthType = types.MCPAuthBearer
			auth.Token = env[varName]
		case tmpl == "${"+varName+"}":
			auth.AuthType = types.MCPAuthAPIKey
			auth.APIKeyHeader = name
			auth.APIKey = env[varName]
		}
	}
	return headers, auth
}

// UpdatePluginSandboxes moves an installed plugin's skills onto exactly the
// given sandboxes. Each sandbox is brought in line with every skill catalog
// (missing or failed installs are installed again) before any sandbox is
// removed. The first failure stops the update, and sandbox_config_ids records
// only the sandboxes that were actually reached.
func (s *PluginService) UpdatePluginSandboxes(
	ctx context.Context, tenantID uint64, marketplaceID, pluginName string, newSandboxConfigIDs []string,
) error {
	install, err := s.repo.GetInstallByName(ctx, tenantID, marketplaceID, pluginName)
	if err != nil {
		return err
	}
	if install == nil {
		return apperrors.NewNotFoundError("installed plugin not found")
	}
	if s.skillSvc == nil {
		return errors.New("skill service is not configured")
	}

	newIDs := uniqueNonEmptyStrings(newSandboxConfigIDs)

	if len(newIDs) == 0 {
		return apperrors.NewBadRequestError("沙箱列表不能为空，如需移除全部沙箱请直接卸载插件")
	}

	// Validate new sandboxes
	for _, cfgID := range newIDs {
		if reqErr := s.skillSvc.RequireSkillTarget(ctx, tenantID, cfgID); reqErr != nil {
			return apperrors.NewBadRequestError(fmt.Sprintf("沙箱目标 %q 无效或不存在: %v", cfgID, reqErr))
		}
	}

	resources, err := s.repo.ListBindingsByInstall(ctx, tenantID, install.ID)
	if err != nil {
		return err
	}
	installsByCatalog := make(map[string][]*types.TenantSkillEntity)
	var catalogIDs []string
	for _, res := range resources {
		if res.Kind != types.PluginResourceKindSkillCatalog {
			continue
		}
		rows, lErr := s.skillRepo.ListSkillsByCatalog(ctx, tenantID, res.ResourceID)
		if lErr != nil {
			return lErr
		}
		catalogIDs = append(catalogIDs, res.ResourceID)
		installsByCatalog[res.ResourceID] = rows
	}

	envs, err := s.pluginEnvsFromInstalls(ctx, install, installsByCatalog)
	if err != nil {
		return err
	}

	current := slices.Clone([]string(install.SandboxConfigIDs))
	changed := false
	save := func(cause error) error {
		if changed {
			err := s.repo.UpdateInstallSandboxes(
				ctx, tenantID, install.ID, types.PluginSandboxConfigIDs(current),
			)
			if err != nil {
				logger.Warnf(ctx, "[plugin] save sandboxes of install %s failed: %v", install.ID, err)
			}
			s.refreshPluginStatus(ctx, tenantID, install.ID)
		}
		return cause
	}

	for _, cfgID := range newIDs {
		for _, catID := range catalogIDs {
			row := installOnSandbox(installsByCatalog[catID], cfgID)
			if row != nil && row.Status != types.SkillStatusFailed {
				continue
			}
			res, iErr := s.skillSvc.InstallCatalogToConfigs(ctx, tenantID, catID, []string{cfgID})
			changed = true
			if res != nil {
				s.injectPluginEnvs(ctx, tenantID, res.Installs, envs)
			}
			if iErr = catalogInstallError(res, iErr); iErr != nil {
				return save(iErr)
			}
		}
		if !slices.Contains(current, cfgID) {
			current = append(current, cfgID)
			changed = true
		}
	}

	for _, cfgID := range slices.Clone(current) {
		if slices.Contains(newIDs, cfgID) {
			continue
		}
		for _, catID := range catalogIDs {
			row := installOnSandbox(installsByCatalog[catID], cfgID)
			if row == nil {
				continue
			}
			rErr := s.skillSvc.RemoveSkillUnguarded(ctx, tenantID, cfgID, row.ID)
			if rErr != nil && !isNotFoundError(rErr) {
				return save(fmt.Errorf("remove skill %s from sandbox %s: %w", row.Name, cfgID, rErr))
			}
		}
		current = slices.DeleteFunc(current, func(id string) bool { return id == cfgID })
		changed = true
	}

	return save(nil)
}

// installOnSandbox returns the install of a catalog on one sandbox, if any.
func installOnSandbox(rows []*types.TenantSkillEntity, cfgID string) *types.TenantSkillEntity {
	for _, row := range rows {
		if row != nil && row.SandboxConfigID == cfgID {
			return row
		}
	}
	return nil
}

func isNotFoundError(err error) bool {
	var appErr *apperrors.AppError
	return errors.As(err, &appErr) && appErr.Code == apperrors.ErrNotFound
}

// pluginEnvsFromInstalls rebuilds the plugin env vars for a new sandbox
// install. The values the user entered at install time live only on the
// existing sandbox installs, so they are copied from there.
func (s *PluginService) pluginEnvsFromInstalls(
	ctx context.Context, install *types.PluginInstall, installsByCatalog map[string][]*types.TenantSkillEntity,
) (types.SkillEnvVars, error) {
	pkg, err := s.repo.GetPackageByID(ctx, install.PackageID)
	if err != nil || pkg == nil || len(pkg.EnvDeclarations) == 0 {
		return nil, err
	}
	values := make(map[string]string, len(pkg.EnvDeclarations))
	for _, rows := range installsByCatalog {
		for _, row := range rows {
			if row == nil {
				continue
			}
			for _, env := range row.Envs {
				if env.Source == SkillEnvVarSourcePlugin && env.Value != "" && values[env.Name] == "" {
					values[env.Name] = env.Value
				}
			}
		}
	}
	return pluginSkillEnvs(pkg.EnvDeclarations, values), nil
}

// UninstallPlugin uninstalls a plugin. MCP services go immediately and the
// install row is deleted before this returns. Skill files leave the sandbox
// in the background.
func (s *PluginService) UninstallPlugin(ctx context.Context, tenantID uint64, marketplaceID, pluginName string) error {
	install, err := s.repo.GetInstallByName(ctx, tenantID, marketplaceID, pluginName)
	if err != nil {
		return err
	}
	if install == nil {
		return apperrors.NewNotFoundError("installed plugin not found")
	}
	return s.teardownInstall(ctx, tenantID, install)
}

// HandleSkillStateChange is the TenantSkillService listener. It recomputes
// the status of the plugin install owning catalogID; catalogs no plugin owns
// cost one indexed lookup.
func (s *PluginService) HandleSkillStateChange(ctx context.Context, tenantID uint64, catalogID string) {
	info, err := s.repo.LookupPluginByResource(ctx, tenantID, types.PluginResourceKindSkillCatalog, catalogID)
	if err != nil {
		logger.Warnf(ctx, "[plugin] look up owner of catalog %s failed: %v", catalogID, err)
		return
	}
	if info == nil {
		return
	}
	s.refreshPluginStatus(ctx, tenantID, info.PluginInstallID)
}

// refreshPluginStatus derives an install's status from its sandbox installs
// and writes it only when it changed. Every sandbox the install targets must
// carry every skill: a missing or failed install fails the plugin, one still
// running keeps it installing, and all ready makes it ready. Variables the
// installer found that plugin.json does not declare are reported as a
// warning on a ready plugin. The install row is locked while the status is
// computed, so a concurrent refresh is never overwritten. It returns the
// status the install has afterwards, or "" when it is unknown.
func (s *PluginService) refreshPluginStatus(ctx context.Context, tenantID uint64, installID string) string {
	declaredEnvSet := make(map[string]bool)
	if install, _ := s.repo.GetInstallByID(ctx, tenantID, installID); install != nil {
		if pkg, _ := s.repo.GetPackageByID(ctx, install.PackageID); pkg != nil {
			for _, d := range pkg.EnvDeclarations {
				declaredEnvSet[d.Name] = true
			}
		}
	}

	// Read OAuth tokens before the status lock. The derive callback runs
	// inside that transaction, and SQLite has one connection, so another
	// query from here would wait on the lock it is already holding.
	oauthPending := s.installOAuthPending(ctx, tenantID, installID)

	settled := ""
	err := s.repo.UpdateInstallStatus(ctx, tenantID, installID, func(
		install *types.PluginInstall, catalogs []interfaces.PluginCatalogInstalls,
	) (string, string, bool) {
		settled = install.Status
		status, msg := derivePluginStatus(install.SandboxConfigIDs, catalogs, declaredEnvSet)
		if status == types.PluginStatusReady && oauthPending {
			status = types.PluginStatusNeedsOAuth
			msg = ""
		}
		settled = status
		return status, msg, true
	})
	if err != nil {
		logger.Warnf(ctx, "[plugin] update status of install %s failed: %v", installID, err)
		return ""
	}
	return settled
}

func derivePluginStatus(
	sandboxIDs types.PluginSandboxConfigIDs, catalogs []interfaces.PluginCatalogInstalls, declared map[string]bool,
) (string, string) {
	var (
		firstError   string
		missing      []string
		pending      bool
		undeclaredEv []string
	)
	for _, cat := range catalogs {
		for _, cfgID := range sandboxIDs {
			row := installOnSandbox(cat.Installs, cfgID)
			switch {
			case row == nil:
				missing = append(missing, fmt.Sprintf("%s@%s", cat.CatalogName, cfgID))
			case row.Status == types.SkillStatusFailed:
				if firstError == "" {
					firstError = row.Error
					if firstError == "" {
						firstError = fmt.Sprintf("技能 %s 在沙箱 %s 上安装失败", cat.CatalogName, cfgID)
					}
				}
			case row.Status != types.SkillStatusReady:
				pending = true
			default:
				for _, envVar := range row.Envs {
					if envVar.Source != SkillEnvVarSourcePlugin && !declared[envVar.Name] &&
						!slices.Contains(undeclaredEv, envVar.Name) {
						undeclaredEv = append(undeclaredEv, envVar.Name)
					}
				}
			}
		}
	}

	switch {
	case firstError != "":
		return types.PluginStatusFailed, firstError
	case len(missing) > 0:
		return types.PluginStatusFailed,
			fmt.Sprintf("技能未安装在目标沙箱上: %s，请重新保存沙箱设置以恢复", strings.Join(missing, ", "))
	case pending:
		return types.PluginStatusInstalling, ""
	case len(undeclaredEv) > 0:
		return types.PluginStatusReady,
			fmt.Sprintf("技能需要未声明的变量 %s，请让插件作者在 plugin.json 的 env 中补充", strings.Join(undeclaredEv, ", "))
	}
	return types.PluginStatusReady, ""
}

// ListInstalledPlugins returns all installed plugins for the tenant.
func (s *PluginService) ListInstalledPlugins(ctx context.Context, tenantID uint64) ([]*types.PluginInstall, error) {
	return s.repo.ListInstalls(ctx, tenantID)
}

// GetPluginDetail returns detailed information for a single installed plugin.
func (s *PluginService) GetPluginDetail(
	ctx context.Context, tenantID uint64, marketplaceID, pluginName string,
) (*PluginDetailView, error) {
	install, err := s.repo.GetInstallByName(ctx, tenantID, marketplaceID, pluginName)
	if err != nil {
		return nil, err
	}
	if install == nil {
		return nil, apperrors.NewNotFoundError("installed plugin not found")
	}

	mp, _ := s.repo.GetMarketplaceByID(ctx, tenantID, marketplaceID)
	mpName := ""
	if mp != nil {
		mpName = mp.Name
	}

	resources, err := s.repo.ListBindingsByInstall(ctx, tenantID, install.ID)
	if err != nil {
		return nil, err
	}

	var mcpServices []*types.MCPService
	var skillViews []PluginSkillInstallView
	var skillRows []*types.TenantSkillEntity

	for _, res := range resources {
		switch res.Kind {
		case types.PluginResourceKindMCPService:
			svc, err := s.mcpRepo.GetByID(ctx, tenantID, res.ResourceID)
			if err == nil && svc != nil {
				mcpServices = append(mcpServices, svc)
			}
		case types.PluginResourceKindSkillCatalog:
			installs, err := s.skillRepo.ListSkillsByCatalog(ctx, tenantID, res.ResourceID)
			if err == nil {
				for _, sk := range installs {
					if sk != nil {
						skillRows = append(skillRows, sk)
						skillViews = append(skillViews, PluginSkillInstallView{
							CatalogID:       res.ResourceID,
							SkillName:       res.ResourceName,
							SkillID:         sk.ID,
							SandboxConfigID: sk.SandboxConfigID,
							Status:          sk.Status,
							Error:           sk.Error,
							Version:         sk.Version,
							UpdatedAt:       sk.UpdatedAt,
						})
					}
				}
			}
		}
	}

	pkg, err := s.repo.GetPackageByID(ctx, install.PackageID)
	if err != nil {
		return nil, err
	}
	var envStatuses []PluginEnvStatus
	latestVersion := ""
	if pkg != nil {
		latestVersion = pkg.Version
		for _, decl := range pkg.EnvDeclarations {
			envStatuses = append(envStatuses, PluginEnvStatus{
				Name:        decl.Name,
				Description: decl.Description,
				Required:    decl.Required,
				IsSet:       pluginEnvIsSet(decl.Name, install.PluginName, pkg, skillRows, mcpServices),
			})
		}
	}
	mcpViews := make([]PluginMCPServiceView, 0, len(mcpServices))
	for _, svc := range mcpServices {
		mcpViews = append(mcpViews, pluginMCPServiceView(svc))
	}

	return &PluginDetailView{
		ID:               install.ID,
		TenantID:         install.TenantID,
		MarketplaceID:    install.MarketplaceID,
		MarketplaceName:  mpName,
		PluginName:       install.PluginName,
		Version:          install.Version,
		LatestVersion:    latestVersion,
		Description:      install.Description,
		SandboxConfigIDs: []string(install.SandboxConfigIDs),
		Status:           install.Status,
		Error:            install.Error,
		CreatedAt:        install.CreatedAt,
		UpdatedAt:        install.UpdatedAt,
		MCPServices:      mcpViews,
		Skills:           skillViews,
		EnvStatus:        envStatuses,
	}, nil
}

// pluginEnvIsSet reports whether a declared variable has a workspace value:
// on any sandbox install of the plugin's skills, or as the credential of an
// MCP service whose header template references it.
func pluginEnvIsSet(
	name, pluginName string, pkg *types.PluginPackageEntity,
	skillRows []*types.TenantSkillEntity, mcpServices []*types.MCPService,
) bool {
	for _, sk := range skillRows {
		if ev, ok := sk.Envs.Get(name); ok && ev.Value != "" {
			return true
		}
	}
	ref := "${" + name + "}"
	for srvName, srvDef := range pkg.MCPServers {
		references := false
		for _, tmpl := range srvDef.Headers {
			if strings.Contains(tmpl, ref) {
				references = true
				break
			}
		}
		if !references {
			continue
		}
		for _, svc := range mcpServices {
			if svc.Name == pluginName+"__"+srvName && hasStaticMCPCredential(svc.AuthConfig) {
				return true
			}
		}
	}
	return false
}

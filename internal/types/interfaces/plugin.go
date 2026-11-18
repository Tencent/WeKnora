package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// PluginRepository defines data access methods for shared marketplaces, their
// plugin packages, and the per-tenant plugin_tenant_bindings ledger.
type PluginRepository interface {
	PluginOwnershipLookup

	// Shared marketplaces and packages. Rows are tenant-independent; a tenant
	// sees a marketplace only through a marketplace binding.
	CreateMarketplace(ctx context.Context, mp *types.PluginMarketplaceEntity, pkgs []*types.PluginPackageEntity) error
	GetSharedMarketplace(ctx context.Context, id string) (*types.PluginMarketplaceEntity, error)
	GetMarketplaceByNameSHA(ctx context.Context, name, sha256 string) (*types.PluginMarketplaceEntity, error)
	ListPackages(ctx context.Context, marketplaceID string) ([]*types.PluginPackageEntity, error)
	GetPackage(ctx context.Context, marketplaceID, name string) (*types.PluginPackageEntity, error)
	GetPackageByID(ctx context.Context, id string) (*types.PluginPackageEntity, error)
	// DeleteMarketplaceIfUnreferenced removes the shared marketplace and its
	// packages when no tenant binding points at them, and returns the stored
	// bundle refs (archive and skill zips) for the caller to delete.
	DeleteMarketplaceIfUnreferenced(ctx context.Context, id string) (deleted bool, bundleRefs []string, err error)

	// Tenant view of marketplaces
	GetMarketplaceByID(ctx context.Context, tenantID uint64, id string) (*types.PluginMarketplaceEntity, error)
	GetMarketplaceByName(ctx context.Context, tenantID uint64, name string) (*types.PluginMarketplaceEntity, error)
	ListMarketplaces(ctx context.Context, tenantID uint64) ([]*types.PluginMarketplaceEntity, error)
	// BindMarketplace and SwapMarketplaceBinding record source on the
	// tenant's own binding; the shared row carries no source.
	BindMarketplace(ctx context.Context, tenantID uint64, mp *types.PluginMarketplaceEntity) error
	// SwapMarketplaceBinding moves the tenant from oldID to mp, repointing
	// each of its installs at the same-named package of mp, in one
	// transaction. It fails with a *PluginMissingError when mp lacks a
	// plugin the tenant has installed.
	SwapMarketplaceBinding(ctx context.Context, tenantID uint64, oldID string, mp *types.PluginMarketplaceEntity) error
	// UnbindMarketplaceIfUnused removes the tenant's binding under the same
	// lock installs take, and fails with ErrPluginMarketplaceInUse while one of
	// its plugins is installed.
	UnbindMarketplaceIfUnused(ctx context.Context, tenantID uint64, id string) error

	// Installs (kind=plugin bindings joined with their package)
	CreateInstall(ctx context.Context, inst *types.PluginInstall) error
	// UpdateInstallStatus recomputes status and error under a row lock from a
	// freshly read install.
	UpdateInstallStatus(ctx context.Context, tenantID uint64, id string, derive PluginInstallStatusFunc) error
	UpdateInstallSandboxes(ctx context.Context, tenantID uint64, id string, ids types.PluginSandboxConfigIDs) error
	GetInstallByID(ctx context.Context, tenantID uint64, id string) (*types.PluginInstall, error)
	GetInstallByName(
		ctx context.Context,
		tenantID uint64,
		marketplaceID, pluginName string,
	) (*types.PluginInstall, error)
	GetInstallByPluginName(ctx context.Context, tenantID uint64, pluginName string) (*types.PluginInstall, error)
	ListInstalls(ctx context.Context, tenantID uint64) ([]*types.PluginInstall, error)
	ListInstallsByMarketplace(
		ctx context.Context,
		tenantID uint64,
		marketplaceID string,
	) ([]*types.PluginInstall, error)
	// DeleteInstall removes the install and every binding it owns.
	DeleteInstall(ctx context.Context, tenantID uint64, id string) error

	// Bindings owned by an install
	CreateBinding(ctx context.Context, b *types.PluginTenantBindingEntity) error
	ListBindingsByInstall(
		ctx context.Context,
		tenantID uint64,
		pluginInstallID string,
	) ([]*types.PluginTenantBindingEntity, error)
	ListBindingsByKind(ctx context.Context, tenantID uint64, kind string) ([]*types.PluginTenantBindingEntity, error)
	DeleteBinding(ctx context.Context, tenantID uint64, id string) error
}

// PluginCatalogInstalls is one skill catalog a plugin install owns, with its
// sandbox installs.
type PluginCatalogInstalls struct {
	CatalogID   string
	CatalogName string
	Installs    []*types.TenantSkillEntity
}

// PluginInstallStatusFunc derives an install's next status and error from the
// current row and its catalogs, all read under the install's row lock.
// ok=false leaves the row as it is.
type PluginInstallStatusFunc func(
	current *types.PluginInstall, catalogs []PluginCatalogInstalls,
) (status, msg string, ok bool)

// PluginOwnershipLookup is how MCP and skill pages learn which plugin owns a
// resource. The ledger itself is written only when a plugin is installed or
// uninstalled.
type PluginOwnershipLookup interface {
	LookupPluginByResource(
		ctx context.Context,
		tenantID uint64,
		kind, resourceID string,
	) (*types.PluginInfoForResource, error)
	ListPluginResourcesByKind(
		ctx context.Context,
		tenantID uint64,
		kind string,
	) (map[string]*types.PluginInfoForResource, error)
}

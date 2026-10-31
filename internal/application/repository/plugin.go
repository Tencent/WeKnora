package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrPluginMarketplaceGone is returned when binding to a shared marketplace
// that was removed concurrently.
var ErrPluginMarketplaceGone = errors.New("plugin marketplace no longer exists")

// ErrPluginInstallExists is returned by CreateInstall when a concurrent
// install of the same plugin name won.
var ErrPluginInstallExists = errors.New("plugin is already installed")

// ErrPluginMarketplaceNotBound is returned when the tenant holds no binding
// to the marketplace, for instance because it was deleted concurrently.
var ErrPluginMarketplaceNotBound = errors.New("plugin marketplace is not bound to the tenant")

// ErrPluginMarketplaceInUse is returned by UnbindMarketplaceIfUnused while a
// plugin of the marketplace is still installed in the tenant.
var ErrPluginMarketplaceInUse = errors.New("plugin marketplace still has installed plugins")

// ErrPluginMarketplaceNameTaken is returned when another binding of the same
// marketplace name was created concurrently for the tenant.
var ErrPluginMarketplaceNameTaken = errors.New("plugin marketplace name is already bound")

// PluginMissingError is returned by SwapMarketplaceBinding when the new
// marketplace no longer ships a plugin the tenant has installed.
type PluginMissingError struct {
	PluginName string
}

func (e *PluginMissingError) Error() string {
	return fmt.Sprintf("plugin %q is installed but missing from the new marketplace", e.PluginName)
}

type pluginRepository struct {
	db *gorm.DB
}

// NewPluginRepository creates a new PluginRepository instance.
func NewPluginRepository(db *gorm.DB) interfaces.PluginRepository {
	return &pluginRepository{db: db}
}

// NewPluginOwnershipLookup exposes ownership queries for the MCP and skill
// services. Those services only read the ledger; uninstall writes it.
func NewPluginOwnershipLookup(db *gorm.DB) interfaces.PluginOwnershipLookup {
	return &pluginRepository{db: db}
}

func takeOne[T any](query *gorm.DB) (*T, error) {
	var row T
	if err := query.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// --- Shared marketplaces and packages ---

func (r *pluginRepository) CreateMarketplace(
	ctx context.Context, mp *types.PluginMarketplaceEntity, pkgs []*types.PluginPackageEntity,
) error {
	if mp.ID == "" {
		mp.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(mp).Error; err != nil {
			return err
		}
		for _, pkg := range pkgs {
			if pkg.ID == "" {
				pkg.ID = uuid.New().String()
			}
			pkg.MarketplaceID = mp.ID
		}
		if len(pkgs) == 0 {
			return nil
		}
		return tx.Create(pkgs).Error
	})
}

func (r *pluginRepository) GetSharedMarketplace(
	ctx context.Context,
	id string,
) (*types.PluginMarketplaceEntity, error) {
	return takeOne[types.PluginMarketplaceEntity](r.db.WithContext(ctx).Where("id = ?", id))
}

func (r *pluginRepository) GetMarketplaceByNameSHA(
	ctx context.Context,
	name, sha256 string,
) (*types.PluginMarketplaceEntity, error) {
	return takeOne[types.PluginMarketplaceEntity](
		r.db.WithContext(ctx).Where("name = ? AND bundle_sha256 = ?", name, sha256),
	)
}

func (r *pluginRepository) ListPackages(
	ctx context.Context,
	marketplaceID string,
) ([]*types.PluginPackageEntity, error) {
	var list []*types.PluginPackageEntity
	err := r.db.WithContext(ctx).
		Where("marketplace_id = ?", marketplaceID).
		Order("position ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *pluginRepository) GetPackage(
	ctx context.Context,
	marketplaceID, name string,
) (*types.PluginPackageEntity, error) {
	return takeOne[types.PluginPackageEntity](
		r.db.WithContext(ctx).Where("marketplace_id = ? AND name = ?", marketplaceID, name),
	)
}

func (r *pluginRepository) GetPackageByID(ctx context.Context, id string) (*types.PluginPackageEntity, error) {
	return takeOne[types.PluginPackageEntity](r.db.WithContext(ctx).Where("id = ?", id))
}

func (r *pluginRepository) DeleteMarketplaceIfUnreferenced(ctx context.Context, id string) (bool, []string, error) {
	var refs []string
	deleted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The row lock serializes against BindMarketplace, so a tenant cannot
		// bind to a marketplace this transaction is about to remove.
		mp, err := takeOne[types.PluginMarketplaceEntity](
			tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id),
		)
		if err != nil || mp == nil {
			return err
		}
		var count int64
		if err := tx.Model(&types.PluginTenantBindingEntity{}).
			Where("(kind = ? AND resource_id = ?) OR (kind = ? AND resource_id IN (?))",
				types.PluginResourceKindMarketplace, id,
				types.PluginResourceKindPlugin,
				tx.Model(&types.PluginPackageEntity{}).Select("id").Where("marketplace_id = ?", id)).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		var pkgs []*types.PluginPackageEntity
		if err := tx.Where("marketplace_id = ?", id).Find(&pkgs).Error; err != nil {
			return err
		}
		for _, pkg := range pkgs {
			for _, skill := range pkg.Skills {
				if skill.BundleRef != "" {
					refs = append(refs, skill.BundleRef)
				}
			}
		}
		if mp.BundleRef != "" {
			refs = append(refs, mp.BundleRef)
		}
		if err := tx.Where("marketplace_id = ?", id).Delete(&types.PluginPackageEntity{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(mp).Error; err != nil {
			return err
		}
		deleted = true
		return nil
	})
	if err != nil || !deleted {
		return false, nil, err
	}
	return true, refs, nil
}

// --- Tenant view of marketplaces ---

func (r *pluginRepository) tenantMarketplaces(ctx context.Context, tenantID uint64) *gorm.DB {
	return r.db.WithContext(ctx).
		Model(&types.PluginMarketplaceEntity{}).
		Select("plugin_marketplaces.*").
		Joins(
			"JOIN plugin_tenant_bindings b ON b.resource_id = plugin_marketplaces.id "+
				"AND b.kind = ? AND b.tenant_id = ?",
			types.PluginResourceKindMarketplace, tenantID,
		)
}

func (r *pluginRepository) GetMarketplaceByID(
	ctx context.Context,
	tenantID uint64,
	id string,
) (*types.PluginMarketplaceEntity, error) {
	return takeOne[types.PluginMarketplaceEntity](
		r.tenantMarketplaces(ctx, tenantID).Where("plugin_marketplaces.id = ?", id),
	)
}

func (r *pluginRepository) GetMarketplaceByName(
	ctx context.Context,
	tenantID uint64,
	name string,
) (*types.PluginMarketplaceEntity, error) {
	return takeOne[types.PluginMarketplaceEntity](r.tenantMarketplaces(ctx, tenantID).
		Where("plugin_marketplaces.name = ?", name).
		Order("b.created_at DESC"))
}

func (r *pluginRepository) ListMarketplaces(
	ctx context.Context,
	tenantID uint64,
) ([]*types.PluginMarketplaceEntity, error) {
	var list []*types.PluginMarketplaceEntity
	err := r.tenantMarketplaces(ctx, tenantID).
		Order("b.created_at DESC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func lockLiveMarketplace(tx *gorm.DB, id string) error {
	mp, err := takeOne[types.PluginMarketplaceEntity](
		tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id),
	)
	if err != nil {
		return err
	}
	if mp == nil {
		return ErrPluginMarketplaceGone
	}
	return nil
}

func marketplaceBinding(tenantID uint64, mp *types.PluginMarketplaceEntity) *types.PluginTenantBindingEntity {
	now := time.Now()
	return &types.PluginTenantBindingEntity{
		ID:           uuid.New().String(),
		TenantID:     tenantID,
		Kind:         types.PluginResourceKindMarketplace,
		ResourceID:   mp.ID,
		ResourceName: mp.Name,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// lockMarketplaceBinding locks the tenant's binding to a marketplace. Installs
// and the marketplace delete both take it, so a plugin cannot be installed
// into a marketplace that is being unbound.
func lockMarketplaceBinding(tx *gorm.DB, tenantID uint64, marketplaceID string) error {
	binding, err := takeOne[types.PluginTenantBindingEntity](tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"tenant_id = ? AND kind = ? AND resource_id = ?",
			tenantID, types.PluginResourceKindMarketplace, marketplaceID,
		),
	)
	if err != nil {
		return err
	}
	if binding == nil {
		return ErrPluginMarketplaceNotBound
	}
	return nil
}

func createMarketplaceBinding(tx *gorm.DB, b *types.PluginTenantBindingEntity) error {
	err := tx.Create(b).Error
	if isUniqueViolation(err) {
		return ErrPluginMarketplaceNameTaken
	}
	return err
}

func (r *pluginRepository) BindMarketplace(
	ctx context.Context,
	tenantID uint64,
	mp *types.PluginMarketplaceEntity,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockLiveMarketplace(tx, mp.ID); err != nil {
			return err
		}
		return createMarketplaceBinding(tx, marketplaceBinding(tenantID, mp))
	})
}

func (r *pluginRepository) SwapMarketplaceBinding(
	ctx context.Context, tenantID uint64, oldID string, mp *types.PluginMarketplaceEntity,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMarketplaceBinding(tx, tenantID, oldID); err != nil {
			return err
		}
		if err := lockLiveMarketplace(tx, mp.ID); err != nil {
			return err
		}
		var installs []*types.PluginTenantBindingEntity
		if err := tx.Where("tenant_id = ? AND kind = ? AND resource_id IN (?)",
			tenantID, types.PluginResourceKindPlugin,
			tx.Model(&types.PluginPackageEntity{}).Select("id").Where("marketplace_id = ?", oldID)).
			Find(&installs).Error; err != nil {
			return err
		}
		for _, inst := range installs {
			pkg, err := takeOne[types.PluginPackageEntity](
				tx.Where("marketplace_id = ? AND name = ?", mp.ID, inst.ResourceName),
			)
			if err != nil {
				return err
			}
			if pkg == nil {
				return &PluginMissingError{PluginName: inst.ResourceName}
			}
			if err := tx.Model(&types.PluginTenantBindingEntity{}).Where("id = ?", inst.ID).
				Updates(map[string]any{"resource_id": pkg.ID, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
		}
		// The old binding goes first: both carry the marketplace name, which is
		// unique per tenant.
		if err := tx.Where("tenant_id = ? AND kind = ? AND resource_id = ?",
			tenantID, types.PluginResourceKindMarketplace, oldID).
			Delete(&types.PluginTenantBindingEntity{}).Error; err != nil {
			return err
		}
		return createMarketplaceBinding(tx, marketplaceBinding(tenantID, mp))
	})
}

func (r *pluginRepository) UnbindMarketplaceIfUnused(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMarketplaceBinding(tx, tenantID, id); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&types.PluginTenantBindingEntity{}).
			Where("tenant_id = ? AND kind = ? AND resource_id IN (?)",
				tenantID, types.PluginResourceKindPlugin,
				tx.Model(&types.PluginPackageEntity{}).Select("id").Where("marketplace_id = ?", id)).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrPluginMarketplaceInUse
		}
		return tx.Where("tenant_id = ? AND kind = ? AND resource_id = ?",
			tenantID, types.PluginResourceKindMarketplace, id).
			Delete(&types.PluginTenantBindingEntity{}).Error
	})
}

// --- Installs ---

// installs selects kind=plugin bindings shaped as types.PluginInstall. The
// package join is a LEFT JOIN so an install stays visible (and removable)
// even if its package went missing.
func (r *pluginRepository) installs(ctx context.Context) *gorm.DB {
	return r.installsOn(r.db.WithContext(ctx))
}

func (r *pluginRepository) installsOn(db *gorm.DB) *gorm.DB {
	return db.
		Table("plugin_tenant_bindings AS b").
		Select(`b.id, b.tenant_id, COALESCE(p.marketplace_id, '') AS marketplace_id, b.resource_id AS package_id,
			b.resource_name AS plugin_name, b.version, COALESCE(p.description, '') AS description,
			b.sandbox_config_ids, b.status, b.error, b.created_at, b.updated_at`).
		Joins("LEFT JOIN plugin_packages p ON p.id = b.resource_id").
		Where("b.kind = ?", types.PluginResourceKindPlugin)
}

func scanInstalls(query *gorm.DB) ([]*types.PluginInstall, error) {
	var list []*types.PluginInstall
	if err := query.Scan(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func scanInstall(query *gorm.DB) (*types.PluginInstall, error) {
	list, err := scanInstalls(query.Limit(1))
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return list[0], nil
}

// CreateInstall returns ErrPluginInstallExists when the tenant already holds
// an install with the same plugin name, and ErrPluginMarketplaceNotBound when
// the tenant no longer holds the install's marketplace.
func (r *pluginRepository) CreateInstall(ctx context.Context, inst *types.PluginInstall) error {
	if inst.ID == "" {
		inst.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMarketplaceBinding(tx, inst.TenantID, inst.MarketplaceID); err != nil {
			return err
		}
		err := tx.Create(&types.PluginTenantBindingEntity{
			ID:               inst.ID,
			TenantID:         inst.TenantID,
			Kind:             types.PluginResourceKindPlugin,
			ResourceID:       inst.PackageID,
			ResourceName:     inst.PluginName,
			Version:          inst.Version,
			Status:           inst.Status,
			Error:            inst.Error,
			SandboxConfigIDs: inst.SandboxConfigIDs,
			CreatedAt:        inst.CreatedAt,
			UpdatedAt:        inst.UpdatedAt,
		}).Error
		if isUniqueViolation(err) {
			return ErrPluginInstallExists
		}
		return err
	})
}

// isUniqueViolation matches gorm's translated sentinel and, when error
// translation is off, the Postgres and SQLite messages.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique constraint")
}

func (r *pluginRepository) GetInstallByID(
	ctx context.Context,
	tenantID uint64,
	id string,
) (*types.PluginInstall, error) {
	return scanInstall(r.installs(ctx).Where("b.tenant_id = ? AND b.id = ?", tenantID, id))
}

func (r *pluginRepository) updateInstallColumns(
	db *gorm.DB, tenantID uint64, id string, columns map[string]any,
) error {
	columns["updated_at"] = time.Now()
	return db.Model(&types.PluginTenantBindingEntity{}).
		Where("tenant_id = ? AND id = ? AND kind = ?", tenantID, id, types.PluginResourceKindPlugin).
		Updates(columns).Error
}

// UpdateInstallStatus locks the install row, reads it back and lets derive
// compute the next status from that fresh copy, so two concurrent refreshes
// (or a refresh racing an uninstall) cannot overwrite each other with values
// computed from stale reads.
func (r *pluginRepository) UpdateInstallStatus(
	ctx context.Context, tenantID uint64, id string, derive interfaces.PluginInstallStatusFunc,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The lock is taken on the bare row: Postgres refuses FOR UPDATE on
		// the nullable side of the LEFT JOIN that installs() reads through.
		locked, err := takeOne[types.PluginTenantBindingEntity](tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND id = ? AND kind = ?", tenantID, id, types.PluginResourceKindPlugin))
		if err != nil || locked == nil {
			return err
		}
		current, err := scanInstall(r.installsOn(tx).Where("b.tenant_id = ? AND b.id = ?", tenantID, id))
		if err != nil || current == nil {
			return err
		}
		// Skill rows are read on tx as well: SQLite runs on one connection,
		// and a read through another handle would wait on this transaction.
		var bindings []*types.PluginTenantBindingEntity
		if err := tx.Where("tenant_id = ? AND plugin_install_id = ? AND kind = ?",
			tenantID, id, types.PluginResourceKindSkillCatalog).
			Order("created_at ASC").Find(&bindings).Error; err != nil {
			return err
		}
		catalogs := make([]interfaces.PluginCatalogInstalls, 0, len(bindings))
		for _, b := range bindings {
			var rows []*types.TenantSkillEntity
			if err := tx.Where("tenant_id = ? AND catalog_id = ?", tenantID, b.ResourceID).
				Order("created_at ASC").Find(&rows).Error; err != nil {
				return err
			}
			catalogs = append(catalogs, interfaces.PluginCatalogInstalls{
				CatalogID: b.ResourceID, CatalogName: b.ResourceName, Installs: rows,
			})
		}
		status, msg, ok := derive(current, catalogs)
		if !ok || (status == current.Status && msg == current.Error) {
			return nil
		}
		return r.updateInstallColumns(tx, tenantID, id, map[string]any{"status": status, "error": msg})
	})
}

func (r *pluginRepository) UpdateInstallSandboxes(
	ctx context.Context, tenantID uint64, id string, ids types.PluginSandboxConfigIDs,
) error {
	return r.updateInstallColumns(r.db.WithContext(ctx), tenantID, id, map[string]any{"sandbox_config_ids": ids})
}

func (r *pluginRepository) GetInstallByName(
	ctx context.Context,
	tenantID uint64,
	marketplaceID, pluginName string,
) (*types.PluginInstall, error) {
	return scanInstall(r.installs(ctx).
		Where("b.tenant_id = ? AND b.resource_name = ? AND p.marketplace_id = ?", tenantID, pluginName, marketplaceID))
}

func (r *pluginRepository) GetInstallByPluginName(
	ctx context.Context,
	tenantID uint64,
	pluginName string,
) (*types.PluginInstall, error) {
	return scanInstall(r.installs(ctx).Where("b.tenant_id = ? AND b.resource_name = ?", tenantID, pluginName))
}

func (r *pluginRepository) ListInstalls(ctx context.Context, tenantID uint64) ([]*types.PluginInstall, error) {
	return scanInstalls(r.installs(ctx).Where("b.tenant_id = ?", tenantID).Order("b.created_at DESC"))
}

func (r *pluginRepository) ListInstallsByMarketplace(
	ctx context.Context,
	tenantID uint64,
	marketplaceID string,
) ([]*types.PluginInstall, error) {
	return scanInstalls(r.installs(ctx).
		Where("b.tenant_id = ? AND p.marketplace_id = ?", tenantID, marketplaceID).
		Order("b.created_at DESC"))
}

func (r *pluginRepository) DeleteInstall(ctx context.Context, tenantID uint64, id string) error {
	if id == "" {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND (plugin_install_id = ? OR (id = ? AND kind = ?))",
			tenantID, id, id, types.PluginResourceKindPlugin).
		Delete(&types.PluginTenantBindingEntity{}).Error
}

// --- Bindings owned by an install ---

func (r *pluginRepository) CreateBinding(ctx context.Context, b *types.PluginTenantBindingEntity) error {
	if b.ID == "" {
		b.ID = uuid.New().String()
	}
	if b.UpdatedAt.IsZero() {
		b.UpdatedAt = b.CreatedAt
	}
	return r.db.WithContext(ctx).Create(b).Error
}

func (r *pluginRepository) ListBindingsByInstall(
	ctx context.Context,
	tenantID uint64,
	pluginInstallID string,
) ([]*types.PluginTenantBindingEntity, error) {
	var list []*types.PluginTenantBindingEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND plugin_install_id = ?", tenantID, pluginInstallID).
		Order("created_at ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *pluginRepository) ListBindingsByKind(
	ctx context.Context,
	tenantID uint64,
	kind string,
) ([]*types.PluginTenantBindingEntity, error) {
	var list []*types.PluginTenantBindingEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND kind = ?", tenantID, kind).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *pluginRepository) DeleteBinding(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&types.PluginTenantBindingEntity{}).Error
}

// --- PluginOwnershipLookup Implementation ---

type pluginOwnershipRow struct {
	ResourceID      string
	PluginInstallID string
	PluginName      string
	MarketplaceID   string
	MarketplaceName string
}

func (r *pluginRepository) ownership(ctx context.Context, tenantID uint64, kind string) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("plugin_tenant_bindings AS r").
		Select(`r.resource_id, r.plugin_install_id, i.resource_name AS plugin_name,
			COALESCE(p.marketplace_id, '') AS marketplace_id, COALESCE(m.name, '') AS marketplace_name`).
		Joins(
			"JOIN plugin_tenant_bindings i ON i.id = r.plugin_install_id AND i.kind = ?",
			types.PluginResourceKindPlugin,
		).
		Joins("LEFT JOIN plugin_packages p ON p.id = i.resource_id").
		Joins("LEFT JOIN plugin_marketplaces m ON m.id = p.marketplace_id").
		Where("r.tenant_id = ? AND r.kind = ?", tenantID, kind)
}

func (row pluginOwnershipRow) info() *types.PluginInfoForResource {
	return &types.PluginInfoForResource{
		PluginInstallID: row.PluginInstallID,
		PluginName:      row.PluginName,
		MarketplaceID:   row.MarketplaceID,
		MarketplaceName: row.MarketplaceName,
	}
}

func (r *pluginRepository) LookupPluginByResource(
	ctx context.Context,
	tenantID uint64,
	kind, resourceID string,
) (*types.PluginInfoForResource, error) {
	var rows []pluginOwnershipRow
	err := r.ownership(ctx, tenantID, kind).
		Where("r.resource_id = ?", resourceID).
		Limit(1).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0].info(), nil
}

func (r *pluginRepository) ListPluginResourcesByKind(
	ctx context.Context,
	tenantID uint64,
	kind string,
) (map[string]*types.PluginInfoForResource, error) {
	var rows []pluginOwnershipRow
	if err := r.ownership(ctx, tenantID, kind).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]*types.PluginInfoForResource, len(rows))
	for _, row := range rows {
		out[row.ResourceID] = row.info()
	}
	return out, nil
}

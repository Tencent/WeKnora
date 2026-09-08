package semantic

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// Repository provides data access for the semantic module. All queries are
// tenant-scoped unless explicitly noted (publish verification needs the
// cross-tenant model name set because one Cube deployment is shared).
type Repository struct {
	db *gorm.DB
}

// NewRepository creates the module repository.
func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// ErrNotFound marks a missing row.
var ErrNotFound = errors.New("resource not found")

func translateNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

// ---- connections ----

// ListConnections lists the tenant's connections.
func (r *Repository) ListConnections(ctx context.Context, tenantID uint64) ([]*CubeConnection, error) {
	var out []*CubeConnection
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// AllConnections lists connections across tenants (datasources.yaml is
// deployment-wide; v1 targets single-org deployments).
func (r *Repository) AllConnections(ctx context.Context) ([]*CubeConnection, error) {
	var out []*CubeConnection
	err := r.db.WithContext(ctx).Order("created_at ASC").Find(&out).Error
	return out, err
}

// FindConnection retrieves one connection by ID.
func (r *Repository) FindConnection(ctx context.Context, tenantID uint64, id string) (*CubeConnection, error) {
	var c CubeConnection
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&c).Error; err != nil {
		return nil, translateNotFound(err)
	}
	return &c, nil
}

// FindConnectionByName retrieves one connection by slug.
func (r *Repository) FindConnectionByName(ctx context.Context, tenantID uint64, name string) (*CubeConnection, error) {
	var c CubeConnection
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND name = ?", tenantID, name).First(&c).Error; err != nil {
		return nil, translateNotFound(err)
	}
	return &c, nil
}

// SaveConnection upserts a connection.
func (r *Repository) SaveConnection(ctx context.Context, c *CubeConnection) error {
	return r.db.WithContext(ctx).Save(c).Error
}

// DeleteConnection soft-deletes a connection.
func (r *Repository) DeleteConnection(ctx context.Context, c *CubeConnection) error {
	return r.db.WithContext(ctx).Delete(c).Error
}

// CountModelsUsingConnection guards connection deletion.
func (r *Repository) CountModelsUsingConnection(ctx context.Context, connectionID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&SemanticModel{}).
		Where("connection_id = ? AND deleted_at IS NULL", connectionID).
		Count(&n).Error
	return n, err
}

// ---- models ----

// ListModels lists the tenant's models.
func (r *Repository) ListModels(ctx context.Context, tenantID uint64) ([]*SemanticModel, error) {
	var out []*SemanticModel
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// PublishedModelYAMLs returns name → published YAML across the deployment:
// model files land in one shared Cube instance, so join-target validation
// must consider every published model, not just the caller's tenant.
func (r *Repository) PublishedModelYAMLs(ctx context.Context) (map[string]string, error) {
	var rows []struct {
		Name          string
		PublishedYAML string
	}
	err := r.db.WithContext(ctx).Model(&SemanticModel{}).
		Select("name, published_yaml").
		Where("status = ?", ModelStatusPublished).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Name] = row.PublishedYAML
	}
	return out, nil
}

// AllModelNames returns every non-deleted model name of the deployment
// (any status). Publish-time join validation uses this: mutually-joined
// model pairs must be publishable in any order — compile verification via
// /v1/meta remains the authoritative check.
func (r *Repository) AllModelNames(ctx context.Context, excludeID string) (map[string]bool, error) {
	var names []string
	q := r.db.WithContext(ctx).Model(&SemanticModel{}).Select("name").Where("deleted_at IS NULL")
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.Pluck("name", &names).Error
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out, err
}

// FindModel retrieves one model by ID.
func (r *Repository) FindModel(ctx context.Context, tenantID uint64, id string) (*SemanticModel, error) {
	var m SemanticModel
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&m).Error; err != nil {
		return nil, translateNotFound(err)
	}
	return &m, nil
}

// SaveModel upserts a model.
func (r *Repository) SaveModel(ctx context.Context, m *SemanticModel) error {
	return r.db.WithContext(ctx).Save(m).Error
}

// DeleteModel soft-deletes a model and its version history.
func (r *Repository) DeleteModel(ctx context.Context, m *SemanticModel) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(m).Error; err != nil {
			return err
		}
		return tx.Where("model_id = ?", m.ID).Delete(&SemanticModelVersion{}).Error
	})
}

// SaveModelVersion appends a publish snapshot.
func (r *Repository) SaveModelVersion(ctx context.Context, v *SemanticModelVersion) error {
	return r.db.WithContext(ctx).Create(v).Error
}

// ListModelVersions lists publish snapshots (newest first).
func (r *Repository) ListModelVersions(
	ctx context.Context,
	tenantID uint64,
	modelID string,
) ([]*SemanticModelVersion, error) {
	var out []*SemanticModelVersion
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND model_id = ?", tenantID, modelID).
		Order("version DESC").Limit(50).Find(&out).Error
	return out, err
}

// FindModelVersion retrieves one snapshot for rollback.
func (r *Repository) FindModelVersion(
	ctx context.Context,
	tenantID uint64,
	versionID string,
) (*SemanticModelVersion, error) {
	var v SemanticModelVersion
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, versionID).First(&v).Error; err != nil {
		return nil, translateNotFound(err)
	}
	return &v, nil
}

// ---- data groups ----

// ListGroups lists the tenant's data groups.
func (r *Repository) ListGroups(ctx context.Context, tenantID uint64) ([]*DataGroup, error) {
	var out []*DataGroup
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// FindGroup retrieves one group by ID.
func (r *Repository) FindGroup(ctx context.Context, tenantID uint64, id string) (*DataGroup, error) {
	var g DataGroup
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&g).Error; err != nil {
		return nil, translateNotFound(err)
	}
	return &g, nil
}

// SaveGroup upserts a group.
func (r *Repository) SaveGroup(ctx context.Context, g *DataGroup) error {
	return r.db.WithContext(ctx).Save(g).Error
}

// DeleteGroup soft-deletes a group and its memberships.
func (r *Repository) DeleteGroup(ctx context.Context, g *DataGroup) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(g).Error; err != nil {
			return err
		}
		return tx.Where("group_id = ?", g.ID).Delete(&DataGroupMember{}).Error
	})
}

// ReplaceGroupMembers atomically rewrites a group's membership.
func (r *Repository) ReplaceGroupMembers(ctx context.Context, tenantID uint64, groupID string, userIDs []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", groupID).Delete(&DataGroupMember{}).Error; err != nil {
			return err
		}
		for _, uid := range userIDs {
			m := &DataGroupMember{TenantID: tenantID, GroupID: groupID, UserID: uid}
			if err := tx.Create(m).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListGroupMembers lists one group's memberships.
func (r *Repository) ListGroupMembers(
	ctx context.Context,
	tenantID uint64,
	groupID string,
) ([]*DataGroupMember, error) {
	var out []*DataGroupMember
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND group_id = ?", tenantID, groupID).Find(&out).Error
	return out, err
}

// UserGroupNames resolves the data group slugs of one user.
func (r *Repository) UserGroupNames(ctx context.Context, tenantID uint64, userID string) ([]string, error) {
	var names []string
	err := r.db.WithContext(ctx).Raw(`
		SELECT g.name FROM semantic_data_groups g
		JOIN semantic_data_group_members m ON m.group_id = g.id
		WHERE m.tenant_id = ? AND m.user_id = ? AND g.deleted_at IS NULL`,
		tenantID, userID).Scan(&names).Error
	return names, err
}

// IsSystemAdmin looks up the platform admin flag on the users table.
func (r *Repository) IsSystemAdmin(ctx context.Context, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	var admin bool
	err := r.db.WithContext(ctx).Raw(`SELECT is_system_admin FROM users WHERE id = ?`, userID).Scan(&admin).Error
	return admin, err
}

// ---- audit ----

// SaveAudit appends one audit record.
func (r *Repository) SaveAudit(ctx context.Context, a *AuditLog) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// ListAudits lists recent audit records.
func (r *Repository) ListAudits(ctx context.Context, tenantID uint64, limit int) ([]*AuditLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var out []*AuditLog
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).
		Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

var _ = types.JSON(nil)

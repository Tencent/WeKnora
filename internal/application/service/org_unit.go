package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type orgUnitService struct {
	repo interfaces.OrgUnitRepository
}

func NewOrgUnitService(repo interfaces.OrgUnitRepository) interfaces.OrgUnitService {
	return &orgUnitService{repo: repo}
}

func (s *orgUnitService) Create(
	ctx context.Context,
	tenantID uint64,
	req *types.CreateOrgUnitRequest,
) (*types.OrgUnit, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.NewValidationError("name is required")
	}
	parentID := strings.TrimSpace(req.ParentID)
	// Top-level (parent empty) org units are reserved for platform
	// system admins — tenant Owner/Admin may only add child nodes.
	if parentID == "" && !types.IsSystemAdminActor(ctx) {
		return nil, apperrors.NewForbiddenError(
			"only system admin can create a top-level organization",
		)
	}
	// Scoped admins may only create under self or descendants.
	if parentID != "" {
		if err := s.assertCanManageOrgUnitTarget(
			ctx, tenantID, parentID, true,
		); err != nil {
			return nil, err
		}
	}

	depth := 0
	pathPrefix := ""
	if parentID == "" && types.IsSystemAdminActor(ctx) {
		// Platform catalog roots always use tenant_id=0.
		tenantID = types.PlatformOrgTenantID
	} else if parentID != "" {
		// Platform catalog parents (tenant_id=0) are shared across
		// workspaces; resolveUnit falls back there so tenant admins can
		// add children under catalog nodes they manage.
		parent, err := s.resolveUnit(ctx, tenantID, parentID)
		if err != nil {
			if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
				return nil, apperrors.NewNotFoundError("parent org unit not found")
			}
			return nil, err
		}
		tenantID = parent.TenantID
		depth = parent.Depth + 1
		pathPrefix = parent.Path
	}

	id := uuid.New().String()
	now := time.Now()
	path := "/" + id + "/"
	if parentID != "" {
		path = pathPrefix + id + "/"
	}
	unit := &types.OrgUnit{
		ID:        id,
		TenantID:  tenantID,
		ParentID:  parentID,
		Name:      name,
		Code:      strings.TrimSpace(req.Code),
		Path:      path,
		Depth:     depth,
		SortOrder: req.SortOrder,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.Create(ctx, unit); err != nil {
		return nil, fmt.Errorf("create org unit: %w", err)
	}
	return unit, nil
}

func (s *orgUnitService) Get(
	ctx context.Context,
	tenantID uint64,
	id string,
) (*types.OrgUnit, error) {
	unit, err := s.resolveUnit(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return nil, apperrors.NewNotFoundError("org unit not found")
		}
		return nil, err
	}
	return unit, nil
}

// resolveUnit loads an OrgUnit by tenant scope. Platform catalog nodes
// (tenant_id=0) are shared across workspaces, so a miss on the business
// tenant falls back there before the system-admin global lookup.
func (s *orgUnitService) resolveUnit(
	ctx context.Context,
	tenantID uint64,
	id string,
) (*types.OrgUnit, error) {
	unit, err := s.repo.GetByID(ctx, tenantID, id)
	if err == nil {
		return unit, nil
	}
	if !errors.Is(err, apprepo.ErrOrgUnitNotFound) {
		return nil, err
	}
	if tenantID != types.PlatformOrgTenantID {
		platformUnit, platformErr := s.repo.GetByID(
			ctx, types.PlatformOrgTenantID, id,
		)
		if platformErr == nil {
			return platformUnit, nil
		}
		if !errors.Is(platformErr, apprepo.ErrOrgUnitNotFound) {
			return nil, platformErr
		}
	}
	if types.IsSystemAdminActor(ctx) {
		return s.repo.GetByIDGlobal(ctx, id)
	}
	return nil, err
}

func (s *orgUnitService) Update(
	ctx context.Context,
	tenantID uint64,
	id string,
	req *types.UpdateOrgUnitRequest,
) (*types.OrgUnit, error) {
	// Scoped admins may rename self or descendants; ancestors/peers blocked.
	if err := s.assertCanManageOrgUnitTarget(ctx, tenantID, id, true); err != nil {
		return nil, err
	}
	unit, err := s.resolveUnit(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return nil, apperrors.NewNotFoundError("org unit not found")
		}
		return nil, err
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, apperrors.NewValidationError("name cannot be empty")
		}
		unit.Name = name
	}
	if req.Code != nil {
		unit.Code = strings.TrimSpace(*req.Code)
	}
	if req.SortOrder != nil {
		unit.SortOrder = *req.SortOrder
	}
	unit.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, unit); err != nil {
		return nil, err
	}
	return unit, nil
}

func (s *orgUnitService) Delete(
	ctx context.Context,
	tenantID uint64,
	id string,
) error {
	// Scoped admins may delete descendants only — not their own node.
	if err := s.assertCanManageOrgUnitTarget(ctx, tenantID, id, false); err != nil {
		return err
	}
	unit, err := s.resolveUnit(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return apperrors.NewNotFoundError("org unit not found")
		}
		return err
	}
	childCount, err := s.repo.CountChildren(ctx, unit.TenantID, id)
	if err != nil {
		return err
	}
	if childCount > 0 {
		return apperrors.NewBadRequestError(
			"cannot delete org unit with children; move or delete children first",
		)
	}
	if err := s.repo.Delete(ctx, unit.TenantID, id); err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return apperrors.NewNotFoundError("org unit not found")
		}
		return err
	}
	return nil
}

func (s *orgUnitService) ListFlat(
	ctx context.Context,
	tenantID uint64,
) ([]*types.OrgUnit, error) {
	return s.listUnitsForActor(ctx, tenantID)
}

func (s *orgUnitService) ListTree(
	ctx context.Context,
	tenantID uint64,
) ([]*types.OrgUnit, error) {
	units, err := s.listUnitsForActor(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return buildOrgUnitTree(units), nil
}

// listUnitsForActor returns the full tenant tree for unscoped actors
// (system admin / cross-tenant superuser / tenant Owner). Scoped admins
// and lower roles only see their home OrgUnit subtree (self + 下级).
func (s *orgUnitService) listUnitsForActor(
	ctx context.Context,
	tenantID uint64,
) ([]*types.OrgUnit, error) {
	if isUnscopedOrgInviter(ctx) {
		return s.listUnitsIncludingPlatform(ctx, tenantID)
	}
	home, err := s.resolveActorHomeOrgUnit(ctx, tenantID)
	if err != nil {
		if errors.Is(err, ErrActorOrgUnitRequired) {
			return []*types.OrgUnit{}, nil
		}
		return nil, err
	}
	if home == nil {
		return s.listUnitsIncludingPlatform(ctx, tenantID)
	}
	return s.repo.ListByPathPrefix(ctx, home.TenantID, home.Path)
}

// listUnitsIncludingPlatform merges the workspace tenant tree with the
// shared platform catalog (tenant_id=0). Super-admin roots land in the
// catalog; invite/member UIs still run under a business tenant_id and
// must see those nodes or 所属组织 disappears.
func (s *orgUnitService) listUnitsIncludingPlatform(
	ctx context.Context,
	tenantID uint64,
) ([]*types.OrgUnit, error) {
	units, err := s.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if tenantID == types.PlatformOrgTenantID {
		return units, nil
	}
	platform, err := s.repo.ListByTenant(ctx, types.PlatformOrgTenantID)
	if err != nil {
		return nil, err
	}
	if len(platform) == 0 {
		return units, nil
	}
	seen := make(map[string]struct{}, len(units)+len(platform))
	out := make([]*types.OrgUnit, 0, len(units)+len(platform))
	for _, unit := range units {
		if unit == nil {
			continue
		}
		seen[unit.ID] = struct{}{}
		out = append(out, unit)
	}
	for _, unit := range platform {
		if unit == nil {
			continue
		}
		if _, exists := seen[unit.ID]; exists {
			continue
		}
		out = append(out, unit)
	}
	return out, nil
}

// ListPlatformTree returns the full admin forest: platform catalog roots
// (tenant_id=0) and legacy in-tenant trees created before the catalog
// convention. Without the legacy merge, Settings → 组织层级 with
// scope=platform would hide existing trees that still live under a
// business tenant_id.
func (s *orgUnitService) ListPlatformTree(
	ctx context.Context,
) ([]*types.OrgUnit, error) {
	units, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	return buildOrgUnitTree(units), nil
}

func (s *orgUnitService) ListPlatformFlat(
	ctx context.Context,
) ([]*types.OrgUnit, error) {
	return s.repo.ListAll(ctx)
}

func buildOrgUnitTree(units []*types.OrgUnit) []*types.OrgUnit {
	byID := make(map[string]*types.OrgUnit, len(units))
	for _, unit := range units {
		if unit == nil {
			continue
		}
		unit.Children = nil
		byID[unit.ID] = unit
	}
	roots := make([]*types.OrgUnit, 0)
	for _, unit := range units {
		if unit == nil {
			continue
		}
		if unit.ParentID == "" {
			roots = append(roots, unit)
			continue
		}
		parent, ok := byID[unit.ParentID]
		if !ok {
			roots = append(roots, unit)
			continue
		}
		parent.Children = append(parent.Children, unit)
	}
	return roots
}

func (s *orgUnitService) Move(
	ctx context.Context,
	tenantID uint64,
	id string,
	newParentID string,
) (*types.OrgUnit, error) {
	// Moved node must be a descendant; new parent may be self or descendant.
	if err := s.assertCanManageOrgUnitTarget(ctx, tenantID, id, false); err != nil {
		return nil, err
	}
	unit, err := s.resolveUnit(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return nil, apperrors.NewNotFoundError("org unit not found")
		}
		return nil, err
	}
	tenantID = unit.TenantID
	newParentID = strings.TrimSpace(newParentID)
	if newParentID == unit.ID {
		return nil, apperrors.NewBadRequestError("cannot move org unit under itself")
	}
	if newParentID != "" {
		if err := s.assertCanManageOrgUnitTarget(
			ctx, tenantID, newParentID, true,
		); err != nil {
			return nil, err
		}
	}
	// Promoting a node to top-level (empty parent) is the same privilege
	// boundary as creating a root org unit.
	if newParentID == "" && unit.ParentID != "" && !types.IsSystemAdminActor(ctx) {
		return nil, apperrors.NewForbiddenError(
			"only system admin can move an organization to the top level",
		)
	}

	newDepth := 0
	newPathPrefix := "/"
	if newParentID != "" {
		parent, parentErr := s.resolveUnit(ctx, tenantID, newParentID)
		if parentErr != nil {
			if errors.Is(parentErr, apprepo.ErrOrgUnitNotFound) {
				return nil, apperrors.NewNotFoundError("parent org unit not found")
			}
			return nil, parentErr
		}
		// Prevent cycles: new parent must not be under the moved node.
		if strings.HasPrefix(parent.Path, unit.Path) {
			return nil, apperrors.NewBadRequestError(
				"cannot move org unit under its descendant",
			)
		}
		newDepth = parent.Depth + 1
		newPathPrefix = parent.Path
	}

	oldPath := unit.Path
	newPath := newPathPrefix + unit.ID + "/"
	depthDelta := newDepth - unit.Depth

	unit.ParentID = newParentID
	unit.Path = newPath
	unit.Depth = newDepth
	unit.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, unit); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateSubtreePaths(
		ctx, tenantID, oldPath, newPath, depthDelta,
	); err != nil {
		return nil, err
	}
	return unit, nil
}

func (s *orgUnitService) AddMember(
	ctx context.Context,
	tenantID uint64,
	orgUnitID string,
	userID string,
	isPrimary bool,
) (*types.OrgUnitMember, error) {
	_ = isPrimary // product semantics: new memberships are always primary
	unit, err := s.resolveUnit(ctx, tenantID, orgUnitID)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return nil, apperrors.NewNotFoundError("org unit not found")
		}
		return nil, err
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, apperrors.NewValidationError("user_id is required")
	}

	// Single-membership model spans platform catalog + workspace tenant.
	memberships, err := s.repo.ListUserMembershipsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, membership := range memberships {
		if membership != nil && membership.OrgUnitID == unit.ID {
			return membership, nil
		}
	}
	for _, membership := range memberships {
		if membership != nil {
			return nil, apperrors.NewConflictError(
				"user already belongs to another org unit; use transfer",
			)
		}
	}

	now := time.Now()
	member := &types.OrgUnitMember{
		ID:        uuid.New().String(),
		OrgUnitID: unit.ID,
		TenantID:  unit.TenantID,
		UserID:    userID,
		IsPrimary: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.AddMember(ctx, member); err != nil {
		return nil, err
	}
	return member, nil
}

func (s *orgUnitService) TransferMember(
	ctx context.Context,
	tenantID uint64,
	userID string,
	toOrgUnitID string,
) (*types.OrgUnitMember, error) {
	toOrgUnitID = strings.TrimSpace(toOrgUnitID)
	unit, err := s.resolveUnit(ctx, tenantID, toOrgUnitID)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return nil, apperrors.NewNotFoundError("org unit not found")
		}
		return nil, err
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, apperrors.NewValidationError("user_id is required")
	}

	memberships, err := s.repo.ListUserMembershipsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(memberships) == 1 && memberships[0] != nil &&
		memberships[0].OrgUnitID == unit.ID {
		return memberships[0], nil
	}

	now := time.Now()
	member := &types.OrgUnitMember{
		ID:        uuid.New().String(),
		OrgUnitID: unit.ID,
		TenantID:  unit.TenantID,
		UserID:    userID,
		IsPrimary: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.TransferMember(ctx, member); err != nil {
		return nil, err
	}
	return member, nil
}

func (s *orgUnitService) RemoveMember(
	ctx context.Context,
	tenantID uint64,
	orgUnitID string,
	userID string,
) error {
	if _, err := s.resolveUnit(ctx, tenantID, orgUnitID); err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return apperrors.NewNotFoundError("org unit not found")
		}
		return err
	}
	if err := s.repo.RemoveMember(ctx, orgUnitID, userID); err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitMemberNotFound) {
			return apperrors.NewNotFoundError("org unit member not found")
		}
		return err
	}
	return nil
}

func (s *orgUnitService) ListMembers(
	ctx context.Context,
	tenantID uint64,
	orgUnitID string,
) ([]*types.OrgUnitMember, error) {
	if _, err := s.resolveUnit(ctx, tenantID, orgUnitID); err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return nil, apperrors.NewNotFoundError("org unit not found")
		}
		return nil, err
	}
	return s.repo.ListMembers(ctx, orgUnitID)
}

func (s *orgUnitService) ListUserMemberships(
	ctx context.Context,
	tenantID uint64,
	userID string,
) ([]*types.OrgUnitMember, error) {
	memberships, err := s.repo.ListUserMemberships(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	// Platform-catalog bindings (tenant_id=0) must surface when browsing a
	// business workspace — otherwise 空间成员 has no 所属组织 values.
	if tenantID == types.PlatformOrgTenantID {
		return memberships, nil
	}
	platformMembers, platformErr := s.repo.ListUserMemberships(
		ctx, types.PlatformOrgTenantID, userID,
	)
	if platformErr != nil {
		return nil, platformErr
	}
	if len(platformMembers) == 0 {
		return memberships, nil
	}
	seen := make(map[string]struct{}, len(memberships)+len(platformMembers))
	out := make([]*types.OrgUnitMember, 0, len(memberships)+len(platformMembers))
	for _, membership := range memberships {
		if membership == nil {
			continue
		}
		seen[membership.OrgUnitID] = struct{}{}
		out = append(out, membership)
	}
	for _, membership := range platformMembers {
		if membership == nil {
			continue
		}
		if _, exists := seen[membership.OrgUnitID]; exists {
			continue
		}
		out = append(out, membership)
	}
	return out, nil
}

func (s *orgUnitService) SetPrimary(
	ctx context.Context,
	tenantID uint64,
	userID string,
	orgUnitID string,
) error {
	unit, err := s.resolveUnit(ctx, tenantID, orgUnitID)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return apperrors.NewNotFoundError("org unit not found")
		}
		return err
	}
	if err := s.repo.SetPrimary(ctx, unit.TenantID, userID, unit.ID); err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitMemberNotFound) {
			return apperrors.NewNotFoundError("org unit member not found")
		}
		return err
	}
	return nil
}

func (s *orgUnitService) ResolveActiveOrgUnit(
	ctx context.Context,
	tenantID uint64,
	userID string,
	requestedID string,
) (string, error) {
	has, err := s.HasHierarchy(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if !has {
		return "", nil
	}

	requestedID = strings.TrimSpace(requestedID)
	if requestedID != "" {
		requested, err := s.resolveUnit(ctx, tenantID, requestedID)
		if err != nil {
			if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
				return "", apperrors.NewBadRequestError("invalid X-Org-Unit-ID")
			}
			return "", err
		}
		// Members may only use units they belong to. Scoped admins may
		// activate home + descendants (本级/下级) — never peers/ancestors,
		// otherwise KB lists would expose sibling or parent-sibling trees.
		// Unscoped browsers (system admin / cross-tenant) keep any-unit.
		if userID != "" && !types.IsSyntheticUserID(userID) {
			role := types.TenantRoleFromContext(ctx)
			if role != types.TenantRoleAdmin && role != types.TenantRoleOwner {
				if _, memberErr := s.repo.GetMember(ctx, requestedID, userID); memberErr != nil {
					if errors.Is(memberErr, apprepo.ErrOrgUnitMemberNotFound) {
						return "", apperrors.NewForbiddenError(
							"not a member of the requested org unit",
						)
					}
					return "", memberErr
				}
			} else if !isUnscopedOrgBrowser(ctx) {
				if err := s.assertRequestedOrgUnitInHomeSubtree(
					ctx, tenantID, requested,
				); err != nil {
					return "", err
				}
			}
		}
		return requestedID, nil
	}

	// Super-admins browsing without an explicit unit stay unscoped so
	// list APIs return all org units ("所有"). Do not fall back to their
	// primary membership — that would silently shrink the "all" view.
	if isUnscopedOrgBrowser(ctx) {
		return "", nil
	}

	if userID == "" || types.IsSyntheticUserID(userID) {
		return "", nil
	}
	memberships, err := s.ListUserMemberships(ctx, tenantID, userID)
	if err != nil {
		return "", err
	}
	// Single-membership model: usually one row. Prefer IsPrimary when
	// multiple rows still exist during rollout, else the first.
	for _, membership := range memberships {
		if membership != nil && membership.IsPrimary {
			return membership.OrgUnitID, nil
		}
	}
	if len(memberships) > 0 && memberships[0] != nil {
		return memberships[0].OrgUnitID, nil
	}
	return "", nil
}

func (s *orgUnitService) ResolveVisibility(
	ctx context.Context,
	tenantID uint64,
	orgUnitID string,
) (*types.OrgUnitVisibility, error) {
	has, err := s.HasHierarchy(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	vis := &types.OrgUnitVisibility{
		CurrentID:    orgUnitID,
		ReadableIDs:  nil,
		WritableID:   orgUnitID,
		HasHierarchy: has,
	}
	if !has || orgUnitID == "" {
		return vis, nil
	}
	ancestors, err := s.ListAncestorIDs(ctx, tenantID, orgUnitID)
	if err != nil {
		return nil, err
	}
	vis.ReadableIDs = ancestors
	return vis, nil
}

func (s *orgUnitService) ListAncestorIDs(
	ctx context.Context,
	tenantID uint64,
	orgUnitID string,
) ([]string, error) {
	// Platform catalog nodes live under tenant_id=0; resolveUnit falls
	// back so descendant KB/MCP share checks still see the path.
	unit, err := s.resolveUnit(ctx, tenantID, orgUnitID)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return nil, apperrors.NewNotFoundError("org unit not found")
		}
		return nil, err
	}
	// path = /a/b/c/ → ["a","b","c"] with self last; return self-first
	// for readability (current first, then ancestors).
	parts := strings.Split(strings.Trim(unit.Path, "/"), "/")
	ids := make([]string, 0, len(parts))
	for index := len(parts) - 1; index >= 0; index-- {
		part := strings.TrimSpace(parts[index])
		if part != "" {
			ids = append(ids, part)
		}
	}
	return ids, nil
}

func (s *orgUnitService) CanReadKB(
	ctx context.Context,
	tenantID uint64,
	activeOrgUnitID string,
	kbOrgUnitID string,
	shareWithDescendants bool,
) (bool, error) {
	has, err := s.HasHierarchy(ctx, tenantID)
	if err != nil {
		return false, err
	}
	if !has {
		return true, nil
	}
	// Unbound KBs stay tenant-wide readable.
	if kbOrgUnitID == "" {
		return true, nil
	}
	if activeOrgUnitID == "" {
		// No active unit: only true unscoped browsers (system admin /
		// cross-tenant) and tenant Owners (bootstrap) may browse all
		// bound KBs. Scoped tenant Admins must operate under a home
		// OrgUnit — otherwise peer/descendant KBs would leak.
		if isUnscopedOrgBrowser(ctx) {
			return true, nil
		}
		return types.TenantRoleFromContext(ctx) == types.TenantRoleOwner, nil
	}
	if kbOrgUnitID == activeOrgUnitID {
		return true, nil
	}
	// Descendants may read an ancestor KB only when the owner opted in.
	if !shareWithDescendants {
		return false, nil
	}
	ancestors, err := s.ListAncestorIDs(ctx, tenantID, activeOrgUnitID)
	if err != nil {
		return false, err
	}
	for _, ancestorID := range ancestors {
		if ancestorID == kbOrgUnitID {
			return true, nil
		}
	}
	return false, nil
}

func (s *orgUnitService) CanWriteKB(
	ctx context.Context,
	tenantID uint64,
	activeOrgUnitID string,
	kbOrgUnitID string,
) (bool, error) {
	has, err := s.HasHierarchy(ctx, tenantID)
	if err != nil {
		return false, err
	}
	if !has {
		return true, nil
	}
	// Unbound KBs remain writable via normal tenant RBAC when hierarchy
	// exists (Admin managing legacy content). Callers still enforce RBAC.
	if kbOrgUnitID == "" {
		return true, nil
	}
	if activeOrgUnitID == "" {
		return false, nil
	}
	return kbOrgUnitID == activeOrgUnitID, nil
}

func (s *orgUnitService) HasHierarchy(
	ctx context.Context,
	tenantID uint64,
) (bool, error) {
	count, err := s.repo.CountByTenant(ctx, tenantID)
	if err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	// Platform catalog is the shared forest for all workspaces.
	if tenantID == types.PlatformOrgTenantID {
		return false, nil
	}
	platformCount, err := s.repo.CountByTenant(ctx, types.PlatformOrgTenantID)
	if err != nil {
		return false, err
	}
	return platformCount > 0, nil
}

// ListInviteableOrgUnits returns units the actor may assign when inviting.
// Scope is always 本级 + 下级 (self + descendants). Granting admin/owner
// further restricts to descendants only (同级不可再任命管理员).
//
// When the actor has no current OrgUnit, system admins / cross-tenant
// superusers / tenant Owners (bootstrap first user) may list the full
// tree — they are not bound to a hierarchy node yet.
func (s *orgUnitService) ListInviteableOrgUnits(
	ctx context.Context,
	tenantID uint64,
	actorOrgUnitID string,
	role types.TenantRole,
) ([]*types.OrgUnit, error) {
	actorOrgUnitID = strings.TrimSpace(actorOrgUnitID)
	if actorOrgUnitID == "" {
		if !isUnscopedOrgInviter(ctx) {
			return nil, apperrors.NewValidationError(
				"current org unit is required to list inviteable organizations",
			)
		}
		all, err := s.listUnitsIncludingPlatform(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		sort.SliceStable(all, func(left, right int) bool {
			if all[left].Depth != all[right].Depth {
				return all[left].Depth < all[right].Depth
			}
			if all[left].SortOrder != all[right].SortOrder {
				return all[left].SortOrder < all[right].SortOrder
			}
			return all[left].Name < all[right].Name
		})
		return all, nil
	}
	actor, err := s.resolveUnit(ctx, tenantID, actorOrgUnitID)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return nil, apperrors.NewNotFoundError("org unit not found")
		}
		return nil, err
	}

	// 管理员/所有者只能挂到下级，避免同级再产生管理员。
	descendantsOnly := role == types.TenantRoleOwner ||
		role == types.TenantRoleAdmin
	out := make([]*types.OrgUnit, 0)

	// 编辑/访客：本级 + 下级（不再包含平级）。
	if !descendantsOnly {
		out = append(out, actor)
	}

	descendants, err := s.repo.ListByPathPrefix(ctx, actor.TenantID, actor.Path)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(out))
	for _, unit := range out {
		if unit != nil {
			seen[unit.ID] = struct{}{}
		}
	}
	for _, unit := range descendants {
		if unit == nil || unit.ID == actor.ID {
			continue
		}
		if _, ok := seen[unit.ID]; ok {
			continue
		}
		out = append(out, unit)
		seen[unit.ID] = struct{}{}
	}

	// Stable order: depth then sort_order then name.
	sort.SliceStable(out, func(left, right int) bool {
		if out[left].Depth != out[right].Depth {
			return out[left].Depth < out[right].Depth
		}
		if out[left].SortOrder != out[right].SortOrder {
			return out[left].SortOrder < out[right].SortOrder
		}
		return out[left].Name < out[right].Name
	})
	return out, nil
}

// isUnscopedOrgBrowser reports callers whose default OrgUnit scope is
// "all units" when no X-Org-Unit-ID is sent: platform system admins,
// cross-tenant superusers, and tenant Owners (product treats Owner as
// the workspace-level equivalent of system admin for tree browse).
func isUnscopedOrgBrowser(ctx context.Context) bool {
	if types.IsSystemAdminActor(ctx) {
		return true
	}
	if user, ok := ctx.Value(types.UserContextKey).(*types.User); ok && user != nil {
		if user.CanAccessAllTenants {
			return true
		}
	}
	return types.TenantRoleFromContext(ctx) == types.TenantRoleOwner
}

// isUnscopedOrgInviter reports callers who may invite without binding to
// a current OrgUnit: platform system admins, cross-tenant operators,
// and tenant Owners (covers the first-install bootstrap user).
func isUnscopedOrgInviter(ctx context.Context) bool {
	if isUnscopedOrgBrowser(ctx) {
		return true
	}
	return types.TenantRoleFromContext(ctx) == types.TenantRoleOwner
}

func (s *orgUnitService) CanInviteToOrgUnit(
	ctx context.Context,
	tenantID uint64,
	actorOrgUnitID string,
	targetOrgUnitID string,
	role types.TenantRole,
) (bool, error) {
	targetOrgUnitID = strings.TrimSpace(targetOrgUnitID)
	if targetOrgUnitID == "" {
		return false, nil
	}
	actorOrgUnitID = strings.TrimSpace(actorOrgUnitID)
	if actorOrgUnitID == "" {
		if !isUnscopedOrgInviter(ctx) {
			return false, nil
		}
		if _, err := s.resolveUnit(ctx, tenantID, targetOrgUnitID); err != nil {
			if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	units, err := s.ListInviteableOrgUnits(ctx, tenantID, actorOrgUnitID, role)
	if err != nil {
		return false, err
	}
	for _, unit := range units {
		if unit != nil && unit.ID == targetOrgUnitID {
			return true, nil
		}
	}
	return false, nil
}

// Member-management scope errors for org-scoped admins.
var (
	// ErrMemberOutsideManageScope is returned when the target member is
	// neither a peer non-admin nor a subordinate.
	ErrMemberOutsideManageScope = errors.New(
		"can only manage same-level non-admins or subordinate members",
	)

	// ErrCannotManagePeerAdmin is returned when the target is another
	// admin/owner at the same org level (本级/平级).
	ErrCannotManagePeerAdmin = errors.New(
		"cannot manage another same-level admin",
	)

	// ErrCannotPromotePeerToAdmin is returned when promoting a peer
	// non-admin to admin (or owner).
	ErrCannotPromotePeerToAdmin = errors.New(
		"cannot promote a same-level member to admin",
	)

	// ErrActorOrgUnitRequired is returned when a scoped admin has no
	// active OrgUnit selected while the tenant has a hierarchy.
	ErrActorOrgUnitRequired = errors.New(
		"current org unit is required to manage members",
	)

	// ErrOrgUnitOutsideManageScope is returned when a scoped admin
	// tries to mutate an OrgUnit outside self+descendants (or delete
	// their own home node).
	ErrOrgUnitOutsideManageScope = errors.New(
		"can only manage subordinate organizations from your own unit",
	)
)

// assertRequestedOrgUnitInHomeSubtree allows requested only when it is
// the actor's home unit or a descendant. Peers and ancestors are denied
// so scoped admins cannot pivot X-Org-Unit-ID to browse sibling KBs.
func (s *orgUnitService) assertRequestedOrgUnitInHomeSubtree(
	ctx context.Context,
	tenantID uint64,
	requested *types.OrgUnit,
) error {
	if requested == nil {
		return apperrors.NewBadRequestError("invalid X-Org-Unit-ID")
	}
	home, err := s.resolveActorHomeOrgUnit(ctx, tenantID)
	if err != nil {
		return apperrors.NewForbiddenError(err.Error())
	}
	if home == nil {
		// Owner/bootstrap without a home unit: keep any-unit (invite path).
		return nil
	}
	if requested.ID == home.ID {
		return nil
	}
	if home.Path == "" ||
		!strings.HasPrefix(requested.Path, home.Path) ||
		requested.ID == home.ID {
		return apperrors.NewForbiddenError(
			"can only activate your organization or a subordinate unit",
		)
	}
	return nil
}

// resolveActorHomeOrgUnit returns the caller's membership OrgUnit
// (primary, else first). Unscoped actors (system admin / Owner / …)
// and callers without a real user principal return (nil, nil).
// A real user with no membership returns ErrActorOrgUnitRequired.
func (s *orgUnitService) resolveActorHomeOrgUnit(
	ctx context.Context,
	tenantID uint64,
) (*types.OrgUnit, error) {
	if isUnscopedOrgInviter(ctx) {
		return nil, nil
	}
	userID, _ := types.UserIDFromContext(ctx)
	userID = strings.TrimSpace(userID)
	// No human principal (tests / internal jobs): leave unscoped.
	// HTTP auth always attaches a real user id for interactive admins.
	if userID == "" || types.IsSyntheticUserID(userID) {
		return nil, nil
	}
	memberships, err := s.ListUserMemberships(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	homeID := primaryOrgUnitIDFromMemberships(memberships)
	if homeID == "" {
		return nil, ErrActorOrgUnitRequired
	}
	unit, err := s.resolveUnit(ctx, tenantID, homeID)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return nil, ErrActorOrgUnitRequired
		}
		return nil, err
	}
	return unit, nil
}

// assertCanManageOrgUnitTarget enforces subtree limits for OrgUnit CRUD.
// allowSelf=true permits the actor's home unit (create-under / rename);
// allowSelf=false requires a strict descendant (delete / move source).
func (s *orgUnitService) assertCanManageOrgUnitTarget(
	ctx context.Context,
	tenantID uint64,
	targetID string,
	allowSelf bool,
) error {
	if isUnscopedOrgInviter(ctx) {
		return nil
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return apperrors.NewForbiddenError(ErrOrgUnitOutsideManageScope.Error())
	}
	home, err := s.resolveActorHomeOrgUnit(ctx, tenantID)
	if err != nil {
		return apperrors.NewForbiddenError(err.Error())
	}
	if home == nil {
		return nil
	}
	if targetID == home.ID {
		if allowSelf {
			return nil
		}
		return apperrors.NewForbiddenError(ErrOrgUnitOutsideManageScope.Error())
	}
	target, err := s.resolveUnit(ctx, tenantID, targetID)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return apperrors.NewNotFoundError("org unit not found")
		}
		return err
	}
	if home.Path == "" ||
		!strings.HasPrefix(target.Path, home.Path) ||
		target.ID == home.ID {
		return apperrors.NewForbiddenError(ErrOrgUnitOutsideManageScope.Error())
	}
	return nil
}

func isAdminOrHigherRole(role types.TenantRole) bool {
	return role == types.TenantRoleAdmin || role == types.TenantRoleOwner
}

func primaryOrgUnitIDFromMemberships(
	memberships []*types.OrgUnitMember,
) string {
	for _, membership := range memberships {
		if membership != nil && membership.IsPrimary &&
			strings.TrimSpace(membership.OrgUnitID) != "" {
			return membership.OrgUnitID
		}
	}
	for _, membership := range memberships {
		if membership != nil && strings.TrimSpace(membership.OrgUnitID) != "" {
			return membership.OrgUnitID
		}
	}
	return ""
}

// orgUnitRelation classifies target relative to actor.
type orgUnitRelation int

const (
	orgUnitRelationOutside orgUnitRelation = iota
	orgUnitRelationPeer
	orgUnitRelationDescendant
)

func classifyOrgUnitRelation(
	actor *types.OrgUnit,
	target *types.OrgUnit,
) orgUnitRelation {
	if actor == nil || target == nil {
		return orgUnitRelationOutside
	}
	if target.ID == actor.ID || target.ParentID == actor.ParentID {
		return orgUnitRelationPeer
	}
	if actor.Path != "" &&
		strings.HasPrefix(target.Path, actor.Path) &&
		target.ID != actor.ID {
		return orgUnitRelationDescendant
	}
	return orgUnitRelationOutside
}

// AssertCanManageTenantMember enforces org-scoped admin member limits.
// newRole may be "" for remove-only operations.
func (s *orgUnitService) AssertCanManageTenantMember(
	ctx context.Context,
	tenantID uint64,
	targetUserID string,
	targetRole types.TenantRole,
	newRole types.TenantRole,
) error {
	if isUnscopedOrgInviter(ctx) {
		return nil
	}
	hasHierarchy, err := s.HasHierarchy(ctx, tenantID)
	if err != nil {
		return err
	}
	if !hasHierarchy {
		return nil
	}

	actorOrgUnitID, _ := types.OrgUnitIDFromContext(ctx)
	actorOrgUnitID = strings.TrimSpace(actorOrgUnitID)
	if actorOrgUnitID == "" {
		return ErrActorOrgUnitRequired
	}

	actorUnit, err := s.resolveUnit(ctx, tenantID, actorOrgUnitID)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return ErrActorOrgUnitRequired
		}
		return err
	}

	targetUserID = strings.TrimSpace(targetUserID)
	memberships, err := s.ListUserMemberships(ctx, tenantID, targetUserID)
	if err != nil {
		return err
	}
	targetOrgUnitID := primaryOrgUnitIDFromMemberships(memberships)
	if targetOrgUnitID == "" {
		return ErrMemberOutsideManageScope
	}
	targetUnit, err := s.resolveUnit(ctx, tenantID, targetOrgUnitID)
	if err != nil {
		if errors.Is(err, apprepo.ErrOrgUnitNotFound) {
			return ErrMemberOutsideManageScope
		}
		return err
	}

	relation := classifyOrgUnitRelation(actorUnit, targetUnit)
	switch relation {
	case orgUnitRelationDescendant:
		return nil
	case orgUnitRelationPeer:
		if isAdminOrHigherRole(targetRole) {
			return ErrCannotManagePeerAdmin
		}
		if newRole != "" && isAdminOrHigherRole(newRole) {
			return ErrCannotPromotePeerToAdmin
		}
		return nil
	default:
		return ErrMemberOutsideManageScope
	}
}

// ResolveMemberListScope limits the members list to users whose OrgUnit
// is the actor's home unit or a descendant (本级 + 下级). Ancestors and
// peer units are excluded. Unscoped actors / tenants without hierarchy
// return restricted=false.
func (s *orgUnitService) ResolveMemberListScope(
	ctx context.Context,
	tenantID uint64,
) ([]string, bool, error) {
	if isUnscopedOrgInviter(ctx) {
		return nil, false, nil
	}
	hasHierarchy, err := s.HasHierarchy(ctx, tenantID)
	if err != nil {
		return nil, false, err
	}
	if !hasHierarchy {
		return nil, false, nil
	}

	home, err := s.resolveActorHomeOrgUnit(ctx, tenantID)
	if err != nil {
		if errors.Is(err, ErrActorOrgUnitRequired) {
			return []string{}, true, nil
		}
		return nil, false, err
	}
	if home == nil {
		return nil, false, nil
	}

	units, err := s.repo.ListByPathPrefix(ctx, home.TenantID, home.Path)
	if err != nil {
		return nil, false, err
	}
	orgUnitIDs := make([]string, 0, len(units))
	for _, unit := range units {
		if unit != nil && unit.ID != "" {
			orgUnitIDs = append(orgUnitIDs, unit.ID)
		}
	}
	if len(orgUnitIDs) == 0 {
		orgUnitIDs = []string{home.ID}
	}

	orgMembers, err := s.repo.ListMembersByOrgUnitIDs(ctx, orgUnitIDs)
	if err != nil {
		return nil, false, err
	}
	seen := make(map[string]struct{}, len(orgMembers)+1)
	out := make([]string, 0, len(orgMembers)+1)
	for _, membership := range orgMembers {
		if membership == nil {
			continue
		}
		uid := strings.TrimSpace(membership.UserID)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	// Always include the caller so the list is never empty of "me".
	if callerID, ok := types.UserIDFromContext(ctx); ok {
		callerID = strings.TrimSpace(callerID)
		if callerID != "" {
			if _, exists := seen[callerID]; !exists {
				out = append(out, callerID)
			}
		}
	}
	return out, true, nil
}

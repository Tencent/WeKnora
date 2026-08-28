package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// SkillCatalogInstallView is one installation of a catalog skill onto a sandbox.
type SkillCatalogInstallView struct {
	SkillID           string    `json:"skill_id"`
	SandboxConfigID   string    `json:"sandbox_config_id"`
	SandboxConfigName string    `json:"sandbox_config_name,omitempty"`
	SandboxType       string    `json:"sandbox_type,omitempty"`
	Status            string    `json:"status"`
	Enabled           bool      `json:"enabled"`
	Error             string    `json:"error,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// SkillCatalogView is a tenant skill definition plus its sandbox installations.
type SkillCatalogView struct {
	ID            string                     `json:"id"`
	Name          string                     `json:"name"`
	Version       string                     `json:"version,omitempty"`
	Description   string                     `json:"description,omitempty"`
	BundleSHA256  string                     `json:"bundle_sha256,omitempty"`
	CreatedAt     time.Time                  `json:"created_at"`
	UpdatedAt     time.Time                  `json:"updated_at"`
	Installations []SkillCatalogInstallView  `json:"installations"`
}

// ListCatalog returns every workspace skill definition and which sandbox
// configs currently carry an installation of it.
func (s *TenantSkillService) ListCatalog(
	ctx context.Context, tenantID uint64,
) ([]SkillCatalogView, error) {
	catalogs, err := s.skills.ListCatalogsByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	installs, err := s.skills.ListSkillsByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var configs []*types.TenantSandboxConfigEntity
	if s.configs != nil {
		var cfgErr error
		configs, cfgErr = s.configs.ListByTenant(ctx, tenantID)
		if cfgErr != nil {
			return nil, cfgErr
		}
	}
	configByID := make(map[string]*types.TenantSandboxConfigEntity, len(configs))
	for _, cfg := range configs {
		if cfg != nil {
			configByID[cfg.ID] = cfg
		}
	}

	byCatalog := make(map[string][]*types.TenantSkillEntity, len(catalogs))
	unattached := make([]*types.TenantSkillEntity, 0)
	for _, row := range installs {
		if row == nil {
			continue
		}
		if row.CatalogID != "" {
			byCatalog[row.CatalogID] = append(byCatalog[row.CatalogID], row)
			continue
		}
		unattached = append(unattached, row)
	}

	out := make([]SkillCatalogView, 0, len(catalogs)+len(unattached))
	seenName := make(map[string]struct{}, len(catalogs))
	for _, cat := range catalogs {
		if cat == nil {
			continue
		}
		seenName[cat.Name] = struct{}{}
		out = append(out, catalogView(cat, byCatalog[cat.ID], configByID))
	}
	// Rows that predate catalog_id (or tests that insert installs directly)
	// still need to show up as definitions so the settings page is complete.
	for _, row := range unattached {
		if _, exists := seenName[row.Name]; exists {
			for i := range out {
				if out[i].Name != row.Name {
					continue
				}
				out[i].Installations = append(out[i].Installations, installView(row, configByID))
			}
			continue
		}
		seenName[row.Name] = struct{}{}
		synthetic := &types.TenantSkillCatalogEntity{
			ID: row.ID, TenantID: row.TenantID, Name: row.Name, Version: row.Version,
			Description: row.Description, BundleSHA256: row.BundleSHA256,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
		out = append(out, catalogView(synthetic, []*types.TenantSkillEntity{row}, configByID))
	}
	return out, nil
}

func catalogView(
	cat *types.TenantSkillCatalogEntity,
	installs []*types.TenantSkillEntity,
	configByID map[string]*types.TenantSandboxConfigEntity,
) SkillCatalogView {
	view := SkillCatalogView{
		ID:            cat.ID,
		Name:          cat.Name,
		Version:       cat.Version,
		Description:   cat.Description,
		BundleSHA256:  cat.BundleSHA256,
		CreatedAt:     cat.CreatedAt,
		UpdatedAt:     cat.UpdatedAt,
		Installations: make([]SkillCatalogInstallView, 0, len(installs)),
	}
	for _, row := range installs {
		view.Installations = append(view.Installations, installView(row, configByID))
	}
	return view
}

func installView(
	row *types.TenantSkillEntity,
	configByID map[string]*types.TenantSandboxConfigEntity,
) SkillCatalogInstallView {
	v := SkillCatalogInstallView{
		SkillID:         row.ID,
		SandboxConfigID: row.SandboxConfigID,
		Status:          row.Status,
		Enabled:         row.Enabled,
		Error:           row.Error,
		UpdatedAt:       row.UpdatedAt,
	}
	if cfg := configByID[row.SandboxConfigID]; cfg != nil {
		v.SandboxConfigName = cfg.Name
		v.SandboxType = cfg.SandboxType
	}
	return v
}

// RegisterCatalogFromArchive records a skill definition without installing it
// onto any sandbox. Re-uploading the same name updates the stored bundle.
func (s *TenantSkillService) RegisterCatalogFromArchive(
	ctx context.Context, tenantID uint64, archive []byte,
) (*types.TenantSkillCatalogEntity, error) {
	bundle, err := ParseSkillBundle(archive)
	if err != nil {
		return nil, err
	}
	return s.upsertCatalogFromBundle(ctx, tenantID, bundle, archive, true)
}

// RegisterCatalogFromSource fetches a public skill and records it in the catalog.
func (s *TenantSkillService) RegisterCatalogFromSource(
	ctx context.Context, tenantID uint64, source string,
) (*types.TenantSkillCatalogEntity, error) {
	archive, err := fetchSkillArchive(ctx, source, s.sourceHTTP)
	if err != nil {
		return nil, err
	}
	return s.RegisterCatalogFromArchive(ctx, tenantID, archive)
}

// InstallCatalogToConfigs copies a catalog skill onto each named sandbox using
// the existing image-install pipeline. Missing configs are skipped with an
// error; partial success is returned so the caller can show per-config status.
func (s *TenantSkillService) InstallCatalogToConfigs(
	ctx context.Context, tenantID uint64, catalogID string, configIDs []string,
) (map[string]string, error) {
	catalog, err := s.resolveCatalog(ctx, tenantID, catalogID)
	if err != nil {
		return nil, err
	}
	archive, err := s.catalogBundleArchive(ctx, tenantID, catalog)
	if err != nil {
		return nil, err
	}

	ids := uniqueNonEmptyStrings(configIDs)
	if len(ids) == 0 {
		return nil, apperrors.NewBadRequestError("at least one sandbox is required")
	}

	accepted := make(map[string]string, len(ids))
	var firstErr error
	for _, configID := range ids {
		skillID, installErr := s.InstallSkill(ctx, tenantID, configID, archive)
		if installErr != nil {
			logger.Warnf(ctx, "[skill] install catalog %s onto config %s failed: %v",
				catalogID, configID, installErr)
			if firstErr == nil {
				firstErr = installErr
			}
			continue
		}
		accepted[configID] = skillID
	}
	if len(accepted) == 0 {
		return nil, firstErr
	}
	return accepted, nil
}

// DeleteCatalog removes a definition that has no remaining installations.
func (s *TenantSkillService) DeleteCatalog(
	ctx context.Context, tenantID uint64, catalogID string,
) error {
	catalog, err := s.skills.GetCatalog(ctx, tenantID, catalogID)
	if err != nil {
		return err
	}
	if catalog == nil {
		return apperrors.NewNotFoundError("skill not found")
	}
	installs, err := s.skills.ListSkillsByCatalog(ctx, tenantID, catalogID)
	if err != nil {
		return err
	}
	live := 0
	for _, row := range installs {
		if row != nil && row.Status != types.SkillStatusRemoving {
			live++
		}
	}
	if live > 0 {
		return apperrors.NewConflictError(
			"remove this skill from every sandbox before deleting it from the catalog")
	}
	return s.skills.DeleteCatalog(ctx, tenantID, catalogID)
}

func (s *TenantSkillService) upsertCatalogFromBundle(
	ctx context.Context, tenantID uint64, bundle *SkillBundle, archive []byte, requireStore bool,
) (*types.TenantSkillCatalogEntity, error) {
	existing, err := s.skills.GetCatalogByName(ctx, tenantID, bundle.Name)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if existing != nil {
		existing.Version = bundle.Version
		existing.Description = bundle.Description
		existing.Instructions = bundle.Instructions
		existing.BundleSHA256 = bundle.SHA256
		existing.UpdatedAt = now
		if err := s.storeCatalogBundle(ctx, tenantID, existing, archive, requireStore); err != nil {
			return nil, err
		}
		if err := s.skills.UpdateCatalog(ctx, existing); err != nil {
			return nil, err
		}
		return existing, nil
	}

	row := &types.TenantSkillCatalogEntity{
		ID: uuid.NewString(), TenantID: tenantID, Name: bundle.Name,
		Version: bundle.Version, Description: bundle.Description,
		Instructions: bundle.Instructions, BundleSHA256: bundle.SHA256,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.storeCatalogBundle(ctx, tenantID, row, archive, requireStore); err != nil {
		return nil, err
	}
	if err := s.skills.CreateCatalog(ctx, row); err != nil {
		if !isSkillNameConflict(err) {
			return nil, err
		}
		winner, lookupErr := s.skills.GetCatalogByName(ctx, tenantID, bundle.Name)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if winner == nil {
			return nil, err
		}
		return s.upsertCatalogFromBundle(ctx, tenantID, bundle, archive, requireStore)
	}
	return row, nil
}

func (s *TenantSkillService) storeCatalogBundle(
	ctx context.Context, tenantID uint64, catalog *types.TenantSkillCatalogEntity, archive []byte, requireStore bool,
) error {
	if catalog == nil || len(archive) == 0 {
		return nil
	}
	fs, err := s.fileServiceForTenant(ctx, tenantID)
	if err != nil {
		if requireStore {
			return err
		}
		logger.Warnf(ctx, "[skill] catalog bundle storage unavailable: %v", err)
		return nil
	}
	ref, err := fs.SaveBytes(ctx, archive, tenantID,
		fmt.Sprintf("tenant-skills/catalog/%s.zip", catalog.ID), false)
	if err != nil {
		if requireStore {
			return fmt.Errorf("store catalog bundle: %w", err)
		}
		logger.Warnf(ctx, "[skill] catalog bundle store failed: %v", err)
		return nil
	}
	catalog.BundleRef = ref
	return nil
}

func (s *TenantSkillService) resolveCatalog(
	ctx context.Context, tenantID uint64, id string,
) (*types.TenantSkillCatalogEntity, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, apperrors.NewNotFoundError("skill not found")
	}
	cat, err := s.skills.GetCatalog(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if cat != nil {
		return cat, nil
	}
	installs, err := s.skills.ListSkillsByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var match *types.TenantSkillEntity
	for _, row := range installs {
		if row == nil {
			continue
		}
		if row.ID == id || row.CatalogID == id {
			match = row
			break
		}
	}
	if match == nil {
		return nil, apperrors.NewNotFoundError("skill not found")
	}
	if cid := strings.TrimSpace(match.CatalogID); cid != "" && cid != id {
		cat, err = s.skills.GetCatalog(ctx, tenantID, cid)
		if err != nil {
			return nil, err
		}
		if cat != nil {
			return cat, nil
		}
	}
	return catalogProjectionFromSkill(match), nil
}

func catalogProjectionFromSkill(row *types.TenantSkillEntity) *types.TenantSkillCatalogEntity {
	return &types.TenantSkillCatalogEntity{
		ID: row.ID, TenantID: row.TenantID, Name: row.Name, Version: row.Version,
		Description: row.Description, Instructions: row.Instructions,
		BundleRef: row.BundleRef, BundleSHA256: row.BundleSHA256,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func (s *TenantSkillService) ListCatalogFiles(
	ctx context.Context, tenantID uint64, catalogID string,
) ([]SkillFileEntry, error) {
	archive, err := s.loadCatalogArchive(ctx, tenantID, catalogID)
	if err != nil {
		return nil, err
	}
	return listSkillZipFiles(archive)
}

func (s *TenantSkillService) ReadCatalogFile(
	ctx context.Context, tenantID uint64, catalogID, relativePath string,
) (*SkillFileContent, error) {
	clean, err := safeSkillFilePath(relativePath)
	if err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	archive, err := s.loadCatalogArchive(ctx, tenantID, catalogID)
	if err != nil {
		return nil, err
	}
	body, err := readSkillZipFile(archive, clean)
	if err != nil {
		if errors.Is(err, errSkillFileMissing) {
			return nil, apperrors.NewNotFoundError("skill file not found")
		}
		return nil, err
	}
	return projectSkillFileContent(clean, body), nil
}

func (s *TenantSkillService) loadCatalogArchive(
	ctx context.Context, tenantID uint64, catalogID string,
) ([]byte, error) {
	catalog, err := s.resolveCatalog(ctx, tenantID, catalogID)
	if err != nil {
		return nil, err
	}
	return s.catalogBundleArchive(ctx, tenantID, catalog)
}

func (s *TenantSkillService) catalogBundleArchive(
	ctx context.Context, tenantID uint64, catalog *types.TenantSkillCatalogEntity,
) ([]byte, error) {
	if catalog == nil {
		return nil, apperrors.NewNotFoundError("skill not found")
	}
	tryRow := func(row *types.TenantSkillEntity) ([]byte, bool) {
		if row == nil || strings.TrimSpace(row.BundleRef) == "" {
			return nil, false
		}
		archive, err := s.downloadSkillBundle(ctx, tenantID, row)
		if err != nil || len(archive) == 0 {
			return nil, false
		}
		return archive, true
	}

	if ref := strings.TrimSpace(catalog.BundleRef); ref != "" {
		if archive, ok := tryRow(&types.TenantSkillEntity{
			Name: catalog.Name, BundleRef: catalog.BundleRef, BundleSHA256: catalog.BundleSHA256,
		}); ok {
			return archive, nil
		}
	}
	installs, err := s.skills.ListSkillsByCatalog(ctx, tenantID, catalog.ID)
	if err != nil {
		return nil, err
	}
	for _, row := range installs {
		if archive, ok := tryRow(row); ok {
			return archive, nil
		}
	}
	all, err := s.skills.ListSkillsByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, row := range all {
		if row == nil {
			continue
		}
		if row.ID != catalog.ID && row.Name != catalog.Name {
			continue
		}
		if archive, ok := tryRow(row); ok {
			return archive, nil
		}
	}
	return nil, apperrors.NewBadRequestError(
		"the archive of this skill is no longer stored; add it again from the original bundle")
}

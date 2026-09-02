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
	BundleSHA256      string    `json:"bundle_sha256,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// SkillCatalogView is a tenant skill definition plus its sandbox installations.
type SkillCatalogView struct {
	ID            string                    `json:"id"`
	Name          string                    `json:"name"`
	Version       string                    `json:"version,omitempty"`
	Description   string                    `json:"description,omitempty"`
	BundleSHA256  string                    `json:"bundle_sha256,omitempty"`
	CreatedAt     time.Time                 `json:"created_at"`
	UpdatedAt     time.Time                 `json:"updated_at"`
	Installations []SkillCatalogInstallView `json:"installations"`
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
	seenCatalog := make(map[string]struct{}, len(catalogs))
	for _, cat := range catalogs {
		if cat == nil {
			continue
		}
		seenName[cat.Name] = struct{}{}
		seenCatalog[cat.ID] = struct{}{}
		out = append(out, catalogView(cat, byCatalog[cat.ID], configByID))
	}
	// Installs whose catalog row was deleted (or never existed) would otherwise
	// vanish: they are not unattached (catalog_id is set) and they are not
	// rendered under any live definition.
	for catalogID, rows := range byCatalog {
		if _, ok := seenCatalog[catalogID]; ok {
			continue
		}
		unattached = append(unattached, rows...)
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
		BundleSHA256:    row.BundleSHA256,
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
	bundle, archive, err := fetchNormalizedSkillBundle(ctx, source, s.sourceHTTP)
	if err != nil {
		return nil, err
	}
	return s.upsertCatalogFromBundle(ctx, tenantID, bundle, archive, true)
}

// CatalogInstallResult is the per-sandbox outcome of one catalog install call.
// Partial success is a successful HTTP accept with Errors filled in, not a
// silent 202: the caller has to be able to tell which configs never started.
type CatalogInstallResult struct {
	Installs map[string]string `json:"installs"`
	Errors   map[string]string `json:"errors,omitempty"`
}

// InstallCatalogToConfigs copies a catalog skill onto each named sandbox using
// the existing image-install pipeline. Missing configs are skipped with an
// error; partial success is returned so the caller can show per-config status.
func (s *TenantSkillService) InstallCatalogToConfigs(
	ctx context.Context, tenantID uint64, catalogID string, configIDs []string,
) (*CatalogInstallResult, error) {
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

	result := &CatalogInstallResult{
		Installs: make(map[string]string, len(ids)),
		Errors:   make(map[string]string),
	}
	var firstErr error
	for _, configID := range ids {
		skillID, installErr := s.InstallSkill(ctx, tenantID, configID, archive)
		if installErr != nil {
			logger.Warnf(ctx, "[skill] install catalog %s onto config %s failed: %v",
				catalogID, configID, installErr)
			result.Errors[configID] = skillUserErrorMessage(installErr)
			if firstErr == nil {
				firstErr = installErr
			}
			continue
		}
		result.Installs[configID] = skillID
	}
	if len(result.Errors) == 0 {
		result.Errors = nil
	}
	if len(result.Installs) == 0 {
		return result, firstErr
	}
	return result, nil
}

// DeleteCatalog removes a definition that has no remaining installations
// and drops the stored zip. Sandbox uninstalls never delete that zip.
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
	for _, row := range installs {
		if row == nil {
			continue
		}
		return apperrors.NewConflictError(
			"remove this skill from every sandbox before deleting it from the catalog")
	}
	ref := strings.TrimSpace(catalog.BundleRef)
	if err := s.skills.DeleteCatalog(ctx, tenantID, catalogID); err != nil {
		return err
	}
	if ref != "" {
		s.deleteBundleBestEffort(ctx, tenantID, ref)
	}
	return nil
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
		stored, releaseReplaced, err := s.storeCatalogBundle(ctx, tenantID, existing, archive, requireStore)
		if err != nil {
			return nil, err
		}
		if !stored {
			return existing, nil
		}
		existing.Version = bundle.Version
		existing.Description = bundle.Description
		existing.Instructions = bundle.Instructions
		existing.BundleSHA256 = bundle.SHA256
		existing.UpdatedAt = now
		if err := s.skills.UpdateCatalog(ctx, existing); err != nil {
			// The stored definition still names the old archive, so leave that
			// archive alone. The object just written is the leak, and it is the
			// harmless one: the next re-register overwrites that same key.
			return nil, err
		}
		releaseReplaced()
		return existing, nil
	}

	row := &types.TenantSkillCatalogEntity{
		ID: uuid.NewString(), TenantID: tenantID, Name: bundle.Name,
		Version: bundle.Version, Description: bundle.Description,
		Instructions: bundle.Instructions,
		CreatedAt:    now, UpdatedAt: now,
	}
	stored, releaseReplaced, err := s.storeCatalogBundle(ctx, tenantID, row, archive, requireStore)
	if err != nil {
		return nil, err
	}
	if stored {
		row.BundleSHA256 = bundle.SHA256
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
		// The row that lost the race is never written, and the object above is
		// keyed by its ID, so nothing will ever name it again — the retry below
		// stores its own copy under the winner's key.
		if ref := strings.TrimSpace(row.BundleRef); ref != "" {
			s.deleteBundleBestEffort(ctx, tenantID, ref)
		}
		return s.upsertCatalogFromBundle(ctx, tenantID, bundle, archive, requireStore)
	}
	releaseReplaced()
	return row, nil
}

// storeCatalogBundle writes the archive and reports whether the definition now
// names it, along with the work that settles the archive it replaced.
//
// That second return value has to be called by whoever commits the row, and
// only once the commit succeeds. Retiring the old object here instead would
// delete bytes the stored definition still points at the moment the write below
// fails: the row would name a ref that no longer resolves, and the object just
// saved would be unreferenced, which takes the definition's files down for good
// rather than leaving the failed re-register as a no-op.
func (s *TenantSkillService) storeCatalogBundle(
	ctx context.Context, tenantID uint64, catalog *types.TenantSkillCatalogEntity, archive []byte, requireStore bool,
) (bool, func(), error) {
	noop := func() {}
	if catalog == nil || len(archive) == 0 {
		return false, noop, nil
	}
	digest := skillArchiveSHA256(archive)
	if strings.TrimSpace(catalog.BundleRef) != "" &&
		strings.TrimSpace(catalog.BundleSHA256) != "" &&
		catalog.BundleSHA256 == digest {
		// Same bytes already on the definition. Installing onto another
		// sandbox must not mint a second object.
		return true, noop, nil
	}
	fs, err := s.fileServiceForTenant(ctx, tenantID)
	if err != nil {
		if requireStore {
			return false, noop, err
		}
		logger.Warnf(ctx, "[skill] catalog bundle storage unavailable: %v", err)
		return false, noop, nil
	}
	oldRef := strings.TrimSpace(catalog.BundleRef)
	oldSHA := strings.TrimSpace(catalog.BundleSHA256)
	ref, err := fs.SaveBytes(ctx, archive, tenantID,
		fmt.Sprintf("tenant-skills/catalog/%s.zip", catalog.ID), false)
	if err != nil {
		if requireStore {
			return false, noop, fmt.Errorf("store catalog bundle: %w", err)
		}
		logger.Warnf(ctx, "[skill] catalog bundle store failed: %v", err)
		return false, noop, nil
	}
	catalog.BundleRef = ref
	if oldRef == "" || oldRef == ref {
		return true, noop, nil
	}
	return true, func() {
		if !s.pinInstallsToReplacedBundle(ctx, tenantID, catalog, oldRef, oldSHA) {
			s.deleteBundleBestEffort(ctx, tenantID, oldRef)
		}
	}, nil
}

// pinInstallsToReplacedBundle hands the archive a definition just replaced to
// the installs that are still serving it, and reports whether the object has to
// be kept.
//
// A definition is mutable; an installation is not. Re-registering a skill from
// a newer zip leaves every sandbox running the image built from the old one, so
// deleting those bytes would take read_skill and the admin file browser down
// for installs that are working perfectly. The row is what pins the object:
// once it names it, nothing else has to remember which version it was.
func (s *TenantSkillService) pinInstallsToReplacedBundle(
	ctx context.Context, tenantID uint64, catalog *types.TenantSkillCatalogEntity, oldRef, oldSHA string,
) bool {
	installs, err := s.skills.ListSkillsByCatalog(ctx, tenantID, catalog.ID)
	if err != nil {
		// Keeping an archive nothing reads costs storage; dropping one an
		// install still needs costs that install its files.
		logger.Warnf(ctx, "[skill] list installs of catalog %s before replacing its bundle failed: %v",
			catalog.ID, err)
		return true
	}
	keep := false
	for _, row := range installs {
		if row == nil || strings.TrimSpace(row.BundleRef) != "" {
			continue
		}
		if oldSHA == "" {
			// The definition never recorded what those bytes were, so no row
			// can be matched against them. Keep rather than orphan.
			keep = true
			continue
		}
		if strings.TrimSpace(row.BundleSHA256) != oldSHA {
			continue
		}
		// Best-effort: a row that could not be stamped still reads the object
		// through the catalog until the next write, and keep is set either way
		// so the bytes it needs stay where they are.
		if err := s.updateSkillFields(ctx, row.TenantID, row.SandboxConfigID, row.ID,
			func(e *types.TenantSkillEntity) { e.BundleRef = oldRef }); err != nil {
			logger.Warnf(ctx, "[skill] pin install %s to the replaced archive of catalog %s failed: %v",
				row.ID, catalog.ID, err)
		}
		keep = true
	}
	return keep
}

func skillUserErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var app *apperrors.AppError
	if errors.As(err, &app) && strings.TrimSpace(app.Message) != "" {
		return app.Message
	}
	return err.Error()
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
	wantSHA := strings.TrimSpace(catalog.BundleSHA256)
	tryRow := func(row *types.TenantSkillEntity) ([]byte, bool) {
		if row == nil || strings.TrimSpace(row.BundleRef) == "" {
			return nil, false
		}
		// Through the same cache the install rows use: the file drawer lists a
		// tree and then opens files out of it, and a definition-owned archive
		// is no cheaper to pull twice than a row-owned one.
		archive, ok := s.trySkillBundle(ctx, tenantID, row)
		if !ok || len(archive) == 0 {
			return nil, false
		}
		if wantSHA != "" && !archiveMatchesSHA(archive, wantSHA) {
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

package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// RegisterBuiltin is create-only: discovering an official package must never
// replace a user's same-name definition, including a concurrent registration.
func (s *TenantSkillService) RegisterBuiltin(
	ctx context.Context,
	tenantID uint64,
	id string,
) (*types.TenantSkillCatalogEntity, error) {
	archive, err := builtin.Archive(id)
	if err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	bundle, err := ParseSkillBundle(archive)
	if err != nil {
		return nil, err
	}
	existing, err := s.skills.GetCatalogByName(ctx, tenantID, bundle.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return matchingBuiltinCatalog(existing, bundle.SHA256)
	}
	row := &types.TenantSkillCatalogEntity{
		ID: uuid.NewString(), TenantID: tenantID, Name: bundle.Name, Version: bundle.Version,
		Description: bundle.Description, Instructions: bundle.Instructions,
		BundleSHA256: bundle.SHA256, CreatedAt: s.now(), UpdatedAt: s.now(),
	}
	if _, _, err := s.storeCatalogBundle(ctx, tenantID, row, archive, true); err != nil {
		return nil, err
	}
	if err := s.skills.CreateCatalog(ctx, row); err != nil {
		s.deleteBundleBestEffort(ctx, tenantID, row.BundleRef)
		if !isSkillNameConflict(err) {
			return nil, err
		}
		winner, lookupErr := s.skills.GetCatalogByName(ctx, tenantID, bundle.Name)
		if lookupErr != nil {
			return nil, lookupErr
		}
		return matchingBuiltinCatalog(winner, bundle.SHA256)
	}
	return row, nil
}

func matchingBuiltinCatalog(
	row *types.TenantSkillCatalogEntity,
	digest string,
) (*types.TenantSkillCatalogEntity, error) {
	if row != nil && row.BundleSHA256 == digest {
		return row, nil
	}
	return nil, apperrors.NewConflictError("a different skill already uses this name; the existing skill was preserved")
}

// tryPreinstalledBuiltin reuses only the reviewed, byte-identical package's
// environment. Tenant resources are still seeded and a real verified snapshot
// is committed by the normal lifecycle. No synthetic ready records are made.
func (s *TenantSkillService) tryPreinstalledBuiltin(ctx context.Context, job installerJob) (bool, error) {
	if job.bundle == nil || !builtin.Matches(job.bundle.Name, job.bundle.Files) {
		return false, nil
	}
	reader, ok := job.mgr.(sandbox.SessionFileReader)
	if !ok {
		return false, nil
	}
	base := path.Join(builtin.ImageRoot, job.bundle.Name)
	marker, err := reader.ReadSessionFile(ctx, job.sess.ID, path.Join(base, ".bundle-digest"))
	if err != nil || strings.TrimSpace(string(marker)) != builtin.DigestFiles(job.bundle.Files) {
		return false, nil // Legacy/custom images retain the existing install path.
	}
	command := "test -x " + sandbox.ShellQuote(base+"/.venv/bin/python") +
		" && ln -s " + sandbox.ShellQuote(base+"/.venv") + " " + sandbox.ShellQuote(job.skillDir+"/.venv") +
		" && " + sandbox.ShellQuote(job.skillDir+"/.venv/bin/python") + " " +
		sandbox.ShellQuote(job.skillDir+"/scripts/weknora_smoke.py") + " --report"
	_, err = s.execInstall(ctx, job.mgr, job.sess.ID, command)
	if err == nil {
		var notes []string
		notes, err = s.verifySkill(ctx, job.mgr, job.sess.ID, job.skillDir, job.bundle)
		s.reportVerificationNotes(ctx, job, notes)
	}
	if err == nil {
		s.publishProgress(ctx, job.tenantID, job.configID, job.skillID, SkillProgress{
			Percent: 80, Stage: "agent_done",
			Log: "Verified the preinstalled builtin environment; no dependency installation needed.",
		})
	}
	job.transcript.Finish(context.WithoutCancel(ctx), err)
	return true, err
}

// SkillMigrationItem describes a pinned source bundle and any migration blocker.
type SkillMigrationItem struct {
	SkillID string `json:"skill_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Blocker string `json:"blocker,omitempty"`
}

// PlanSkillMigration pins each old installation's bytes. No fallback to a
// newer catalog archive is permitted, even for legacy rows without a digest.
func (s *TenantSkillService) PlanSkillMigration(
	ctx context.Context,
	tenantID uint64,
	sourceID, targetID string,
) ([]SkillMigrationItem, error) {
	if sourceID == "" || targetID == "" || sourceID == targetID {
		return nil, apperrors.NewBadRequestError("select two different sandbox configurations")
	}
	for _, id := range []string{sourceID, targetID} {
		cfg, err := s.configs.GetByID(ctx, tenantID, id)
		if err != nil {
			return nil, err
		}
		if cfg == nil {
			return nil, apperrors.NewNotFoundError("sandbox config not found")
		}
	}
	rows, err := s.skills.ListSkillsByConfig(ctx, tenantID, sourceID)
	if err != nil {
		return nil, err
	}
	targets, err := s.skills.ListSkillsByConfig(ctx, tenantID, targetID)
	if err != nil {
		return nil, err
	}
	occupied := make(map[string]bool)
	for _, row := range targets {
		if row != nil {
			occupied[row.Name] = true
		}
	}
	items := make([]SkillMigrationItem, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		item := SkillMigrationItem{SkillID: row.ID, Name: row.Name, Version: row.Version, SHA256: row.BundleSHA256}
		switch {
		case row.Status != types.SkillStatusReady:
			item.Blocker = "source_not_ready"
		case !row.Enabled:
			item.Blocker = "source_disabled"
		case occupied[row.Name]:
			item.Blocker = "target_name_conflict"
		case row.BundleSHA256 == "":
			item.Blocker = "missing_digest"
		default:
			archive, err := s.skillBundleArchive(ctx, tenantID, sourceID, row.ID)
			if err != nil || !archiveMatchesSHA(archive, row.BundleSHA256) {
				item.Blocker = "missing_original_archive"
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// MigrateSkills validates the selected bundle versions before installing them into the target.
func (s *TenantSkillService) MigrateSkills(
	ctx context.Context,
	tenantID uint64,
	sourceID, targetID string,
	selection []SkillMigrationItem,
) (*CatalogInstallResult, error) {
	if len(selection) == 0 || len(selection) > 100 {
		return nil, apperrors.NewBadRequestError("select between 1 and 100 verified skills")
	}
	expected := make(map[string]string, len(selection))
	for _, item := range selection {
		expected[item.SkillID] = item.SHA256
	}
	items, err := s.PlanSkillMigration(ctx, tenantID, sourceID, targetID)
	if err != nil {
		return nil, err
	}
	result := &CatalogInstallResult{Installs: map[string]string{}, Errors: map[string]string{}}
	for _, item := range items {
		digest, selected := expected[item.SkillID]
		if !selected {
			continue
		}
		delete(expected, item.SkillID)
		if digest == "" || digest != item.SHA256 {
			result.Errors[item.Name] = "source_changed"
			continue
		}
		if item.Blocker != "" {
			result.Errors[item.Name] = item.Blocker
			continue
		}
		archive, err := s.skillBundleArchive(ctx, tenantID, sourceID, item.SkillID)
		if err == nil && !archiveMatchesSHA(archive, item.SHA256) {
			err = errors.New("original archive changed")
		}
		if err == nil {
			var skillID string
			var bundle *SkillBundle
			bundle, err = ParseSkillBundle(archive)
			if err == nil {
				original, lookupErr := s.skills.GetSkill(ctx, tenantID, sourceID, item.SkillID)
				err = lookupErr
				if err == nil &&
					(original == nil || original.BundleSHA256 != item.SHA256 ||
						!original.Enabled || original.Status != types.SkillStatusReady) {
					err = errors.New("source installation changed; preview again")
				}
				if err == nil {
					skillID, err = s.installParsedSkillFrom(ctx, tenantID, targetID, bundle, archive, original)
				}
			}
			if err == nil {
				result.Installs[item.Name] = skillID
			}
		}
		if err != nil {
			result.Errors[item.Name] = fmt.Sprint(err)
		}
	}
	for id := range expected {
		result.Errors[id] = "source_not_found"
	}
	return result, nil
}

// Store a separate archive reference for the migrated install. The workspace
// catalog can already contain a newer version and must remain untouched.
func (s *TenantSkillService) pinMigratedBundle(
	ctx context.Context,
	tenantID uint64,
	configID, skillID string,
	original *types.TenantSkillEntity,
	archive []byte,
) error {
	holder := &types.TenantSkillCatalogEntity{ID: uuid.NewString(), TenantID: tenantID}
	if _, _, err := s.storeCatalogBundle(ctx, tenantID, holder, archive, true); err != nil {
		return err
	}
	row, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil || row == nil {
		s.deleteBundleBestEffort(ctx, tenantID, holder.BundleRef)
		if err != nil {
			return err
		}
		return errors.New("target installation disappeared")
	}
	row.BundleRef = holder.BundleRef
	row.CatalogID = original.CatalogID
	row.Envs = original.Envs
	if err := s.skills.UpdateSkill(ctx, row); err != nil {
		s.deleteBundleBestEffort(ctx, tenantID, holder.BundleRef)
		return err
	}
	return nil
}

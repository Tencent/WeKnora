package service

import (
	"context"
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
	return s.createCatalogFromValidatedBundle(ctx, tenantID, bundle, archive)
}

func (s *TenantSkillService) createCatalogFromValidatedBundle(
	ctx context.Context, tenantID uint64, bundle *SkillBundle, archive []byte,
) (*types.TenantSkillCatalogEntity, error) {
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
		" && ln -s " + sandbox.ShellQuote(base+"/.venv") + " " + sandbox.ShellQuote(job.skillDir+"/.venv")
	if _, needsNode := job.bundle.Files["package-lock.json"]; needsNode {
		command += " && test -d " + sandbox.ShellQuote(base+"/node_modules") +
			" && ln -s " + sandbox.ShellQuote(base+"/node_modules") + " " +
			sandbox.ShellQuote(job.skillDir+"/node_modules")
	}
	command += " && " + sandbox.ShellQuote(job.skillDir+"/.venv/bin/python") + " " +
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

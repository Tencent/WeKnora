package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// RemoveSkill deletes a skill from the image. It is not a DB-only operation:
// the files really do leave the image, via a new snapshot taken after the
// directory is gone. Removal therefore shares the install's per-config lock —
// both grow a new snapshot from the current one, and interleaving them would
// discard whichever wrote the pointer first.
func (s *TenantSkillService) RemoveSkill(
	ctx context.Context, tenantID uint64, configID, skillID string,
) error {
	skill, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return err
	}
	if skill == nil {
		return apperrors.NewNotFoundError("skill not found")
	}

	now := s.now()
	skill.Status = types.SkillStatusRemoving
	skill.Error = ""
	skill.InstallingSince = &now
	if err := s.skills.UpdateSkill(ctx, skill); err != nil {
		return err
	}
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
		Percent: 5, Stage: "accepted", Status: types.SkillStatusRemoving,
	})

	// Like the install, the removal outlives the HTTP request and must not
	// inherit its cancellation. It is not durable across a restart either;
	// that is the stuck-run reaper's job (Task 17).
	go func() {
		bgCtx := context.WithoutCancel(ctx)
		if err := s.withConfigLock(bgCtx, tenantID, configID, func(lockCtx context.Context) error {
			return s.runRemove(lockCtx, tenantID, configID, skillID)
		}); err != nil {
			logger.Errorf(bgCtx, "[skill] remove %s failed: %v", skillID, err)
		}
	}()
	return nil
}

func (s *TenantSkillService) runRemove(
	ctx context.Context, tenantID uint64, configID, skillID string,
) (err error) {
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	ctx = types.WithSandboxTenantID(ctx, tenantID)

	// An empty id never names a row; refusing here keeps the later GetSkill
	// from being the only thing standing between us and a no-op that looks
	// like success.
	if strings.TrimSpace(skillID) == "" {
		return fmt.Errorf("skill id is required")
	}

	// RemoveSkill validates the skill outside this lock, so two submissions
	// queue two runs. The second has nothing to remove, and doing it anyway
	// would boot a sandbox, wipe a directory that is not there and spend a
	// snapshot plus a generation bump to produce an identical image.
	existing, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return fmt.Errorf("load skill %s: %w", skillID, err)
	}
	if existing == nil {
		return nil
	}
	// RemoveSkill queues this run before the per-config lock. A newer upload
	// of the same name already flipped the row back to installing; deleting
	// it (or wiping its directory) would discard that install.
	if existing.Status != types.SkillStatusRemoving {
		return nil
	}

	// The image directory is the skill name, and SkillDirFor is what refuses a
	// name that would escape the skills root. Resolved before any sandbox work
	// so a bad name costs nothing; the guard at the rm itself is what keeps
	// that an invariant rather than a convention.
	skillDir, err := sandbox.SkillDirFor(existing.Name)
	if err != nil {
		return err
	}

	// Cleanup runs on a context that cannot be cancelled by whatever it is
	// compensating for: withConfigLock cancels ctx the moment lock renewal
	// fails, and both the sandbox destroy (a provider call) and the terminal
	// row write (a DB call) still have to happen then. Only cancellation is
	// detached here — each consumer calls cleanupContext so its budget starts
	// when its work does, because a removal can run for minutes.
	cleanupBase := context.WithoutCancel(ctx)

	// imageChanged marks the point of no return. Past it the skill is gone
	// from the image every new session boots, so restoring the row to ready
	// would advertise files that no longer exist.
	imageChanged := false
	defer func() {
		if err == nil {
			return
		}
		if imageChanged {
			logger.Errorf(cleanupBase,
				"[skill] %s is gone from the image but its bookkeeping is incomplete: %v",
				skillID, err)
			return
		}
		// A half-removed skill is worse than a kept one: the image still has
		// it, so put the row back the way the operator can retry from.
		restoreCtx, cancelRestore := s.cleanupContext(cleanupBase)
		defer cancelRestore()
		s.restoreSkillAfterFailedRemoval(restoreCtx, tenantID, configID, skillID, err)
	}()

	cfgEntity, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return fmt.Errorf("load sandbox config %s: %w", configID, err)
	}
	if cfgEntity == nil {
		return fmt.Errorf("sandbox config %s not found", configID)
	}

	// A skill that never made it into any image needs no sandbox at all, and
	// no live sandbox can be out of date over a skill it never carried.
	if currentSnapshotID(cfgEntity) == "" {
		return s.finishRemoval(cleanupBase, tenantID, configID, skillID, false)
	}

	// Everything below either creates provider resources or moves the image
	// pointer, and the fallback moves it without taking a snapshot first. A
	// cancelled context means the lock's renewal failed and another run may
	// already be building on this config, so stop rather than race it.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("remove lock lost before the image changes: %w", err)
	}

	remaining, err := s.countRemainingSkills(ctx, tenantID, configID, skillID)
	if err != nil {
		return err
	}
	if remaining == 0 {
		// Nothing would be left to carry, and the base template carries no
		// skills by construction, so pointing back at it removes the files as
		// surely as a wipe would. Checked before the sandbox is started: the
		// snapshot that work produced would be discarded unread.
		if err := s.clearImagePointer(
			ctx, tenantID, configID, currentGeneration(cfgEntity)+1,
		); err != nil {
			return err
		}
		imageChanged = true
		// No new ledger row exists to keep active: nothing points at a
		// snapshot any more.
		s.markPreviousSnapshotsSuperseded(ctx, tenantID, configID, "")
		return s.finishRemoval(cleanupBase, tenantID, configID, skillID, true)
	}

	// Remaining skills still live in the current snapshot, so it must be an
	// image the live credentials can resolve. A last-skill clear above does
	// not boot that snapshot — it only drops the pointer — and must stay
	// possible after a credential rotation so the operator can fall back to
	// the base template.
	if err := ensureUsableImage(cfgEntity); err != nil {
		return err
	}
	builtFingerprint := skillOwnerFingerprint(cfgEntity.Config)

	// ResolveEffectiveConfig has already turned the current snapshot into the
	// template, so this sandbox boots from the image the skill is in.
	sess, mgr, err := s.startMaintenanceSession(ctx, tenantID, configID, "remove")
	if err != nil {
		return err
	}
	// Only the sandbox is released; the session and its messages stay, the
	// same way an install keeps its transcript.
	defer func() {
		releaseCtx, cancelRelease := s.cleanupContext(cleanupBase)
		defer cancelRelease()
		s.releaseSandbox(releaseCtx, mgr, sess.ID)
	}()
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{Percent: 30, Stage: "sandbox_ready"})

	if err := s.removeSkillDirectory(ctx, mgr, sess.ID, skillDir); err != nil {
		return err
	}
	if err := s.removeManifestEntry(ctx, mgr, sess.ID, skillID); err != nil {
		return err
	}
	// Same reason as the install: the per-session workspace and the package
	// caches must not reach the snapshot.
	if err := s.cleanImageScratch(ctx, mgr, sess.ID); err != nil {
		return err
	}
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{Percent: 60, Stage: "removed"})

	// Checked again: everything above is confined to a sandbox we are about
	// to destroy, and the wipe itself takes minutes, so the lock may have been
	// lost since the check before the fallback branch.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("remove lock lost before snapshot: %w", err)
	}
	owned, err := s.removeStillOwnsTheRow(ctx, tenantID, configID, skillID)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	ledger, err := s.skills.ListSnapshotsByConfig(ctx, tenantID, configID)
	if err != nil {
		return fmt.Errorf("list snapshots of config %s: %w", configID, err)
	}
	generation := nextSnapshotGeneration(currentGeneration(cfgEntity), ledger)
	// The ledger row is written before the snapshot: a snapshot with no ledger
	// entry is a provider resource nobody knows exists.
	removeRowID := uuid.NewString()
	snapshotName := skillSnapshotBuildName(tenantID, configID, generation, removeRowID)
	if err := s.skills.CreateSnapshotRow(ctx, &types.TenantSkillSnapshotEntity{
		ID: removeRowID, TenantID: tenantID, SandboxConfigID: configID, SkillID: skillID,
		ParentSnapshotID: currentSnapshotID(cfgEntity), Generation: generation,
		Trigger: types.SkillSnapshotTriggerRemove, State: types.SkillSnapshotStateBuilding,
		PlannedName: snapshotName,
	}); err != nil {
		return err
	}
	ref, err := s.createSnapshot(ctx, mgr, sess.ID, snapshotName)
	if err != nil {
		return err
	}
	if err := s.skills.MarkSnapshotState(
		ctx, tenantID, removeRowID, types.SkillSnapshotStateActive, ref.ID,
	); err != nil {
		// The snapshot's ID exists nowhere but this function's locals now, so
		// it is as unreachable as one nothing points at.
		s.abandonSnapshot(cleanupBase, tenantID, mgr, removeRowID, ref.ID)
		return err
	}

	if err := s.switchImagePointer(ctx, tenantID, configID, ref.ID, generation, builtFingerprint); err != nil {
		s.abandonSnapshot(cleanupBase, tenantID, mgr, removeRowID, ref.ID)
		return err
	}
	imageChanged = true
	s.markPreviousSnapshotsSuperseded(ctx, tenantID, configID, removeRowID)
	return s.finishRemoval(cleanupBase, tenantID, configID, skillID, true)
}

// removeSkillDirectory wipes one skill's tree from the image. The per-skill
// environment discipline is what makes a single rm enough: the venv,
// node_modules and any local bin live under that tree, so they go with it.
//
// It reclaims only what lives under that tree. Anything the installer agent
// put elsewhere - an apt package, a globally linked npm binary - stays behind,
// because there is no record of what a given install changed outside its own
// directory and guessing would mean uninstalling a system library another
// skill now depends on. The cost is bounded image growth, which the rebuild
// flow can reclaim from the base template; the alternative risks breaking
// skills that still work.
func (s *TenantSkillService) removeSkillDirectory(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string,
) error {
	if err := guardSkillDir(skillDir); err != nil {
		return err
	}
	if _, err := s.execInstall(
		ctx, mgr, sessionID, "rm -rf "+sandbox.ShellQuote(skillDir),
	); err != nil {
		return fmt.Errorf("remove skill directory %s: %w", skillDir, err)
	}
	return nil
}

// removeManifestEntry drops the skill from the image's manifest.
//
// A manifest that cannot be read or parsed is not a reason to fail the
// removal: it is a troubleshooting aid, never the source of truth for
// execution, and rewriting it from an empty value would erase the entries of
// every other skill in the image.
func (s *TenantSkillService) removeManifestEntry(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillID string,
) error {
	reader, ok := mgr.(sandbox.SessionFileReader)
	if !ok {
		return nil
	}
	raw, err := reader.ReadSessionFile(ctx, sessionID, sandbox.SkillsManifestPath)
	if err != nil {
		logger.Warnf(ctx, "[skill] read image manifest failed: %v", err)
		return nil
	}
	var manifest skillImageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		logger.Warnf(ctx, "[skill] image manifest is unreadable: %v", err)
		return nil
	}

	kept := make([]skillImageManifestEntry, 0, len(manifest.Skills))
	for _, entry := range manifest.Skills {
		if entry.ID != skillID {
			kept = append(kept, entry)
		}
	}
	if len(kept) == len(manifest.Skills) {
		return nil
	}
	manifest.Skills = kept

	store, err := installFileStore(mgr)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return store.WriteSessionFile(ctx, sessionID, sandbox.SkillsManifestPath, payload)
}

// countRemainingSkills counts what the image would still have to carry.
//
// Every row counts, not just the ready ones: a row left failed by a re-install
// still has the previously installed version's files in the image, and
// dropping back to the base template would silently take those away too.
func (s *TenantSkillService) countRemainingSkills(
	ctx context.Context, tenantID uint64, configID, removedSkillID string,
) (int, error) {
	skills, err := s.skills.ListSkillsByConfig(ctx, tenantID, configID)
	if err != nil {
		return 0, fmt.Errorf("list skills of config %s: %w", configID, err)
	}
	remaining := 0
	for _, skill := range skills {
		if skill != nil && skill.ID != removedSkillID {
			remaining++
		}
	}
	return remaining, nil
}

// clearImagePointer points the config back at its base template. It re-reads
// the config for the same reason switchImagePointer does: the entity read at
// the top of the run is minutes old, and writing it back would revert whatever
// the config service persisted in the meantime.
func (s *TenantSkillService) clearImagePointer(
	ctx context.Context, tenantID uint64, configID string, generation int,
) error {
	cfgEntity, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return fmt.Errorf("re-read sandbox config %s: %w", configID, err)
	}
	if cfgEntity == nil || cfgEntity.Config == nil {
		return fmt.Errorf("sandbox config %s disappeared during the removal", configID)
	}

	// The metadata is kept and the generation still advances: an empty
	// SnapshotID already means "boot the base template", and the rest is what
	// tells an operator which generation emptied the image.
	cfgEntity.Config.SkillImage = &types.SkillImageConfig{
		Generation:       generation,
		BuiltAt:          s.now(),
		BaseTemplateID:   effectiveBaseTemplate(cfgEntity),
		OwnerFingerprint: skillOwnerFingerprint(cfgEntity.Config),
	}
	cfgEntity.TenantID = tenantID
	return s.configs.Update(ctx, cfgEntity)
}

// finishRemoval drops the DB row, then invalidates bound sandboxes. The
// catalog archive is left alone: uninstalling from a sandbox must not
// destroy the definition. It runs only once the image no longer contains
// the skill, which is also why the row delete is retried rather than
// reported as a failed removal.
func (s *TenantSkillService) finishRemoval(
	cleanupBase context.Context, tenantID uint64, configID, skillID string, imageChanged bool,
) error {
	ctx, cancel := s.cleanupContext(cleanupBase)
	defer cancel()

	owned, err := s.removeStillOwnsTheRow(ctx, tenantID, configID, skillID)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}

	// Read before the delete: past it the only record of what this row named is
	// gone, and a pinned archive would be left behind with nothing to reclaim it.
	pinned := ""
	if row, err := s.skills.GetSkill(ctx, tenantID, configID, skillID); err == nil && row != nil {
		pinned = strings.TrimSpace(row.BundleRef)
	}
	if err := s.retrySkillBookkeeping(ctx, func() error {
		return s.skills.DeleteSkill(ctx, tenantID, configID, skillID)
	}); err != nil {
		return fmt.Errorf("skill %s no longer exists in the image but its row remains: %w",
			skillID, err)
	}
	// The definition's archive survives this; only an object no catalog and no
	// other install names is reclaimed.
	s.releaseInstallBundle(ctx, tenantID, pinned)
	// Only an image that actually changed can leave a bound sandbox out of
	// date. Marking after a removal that moved no pointer would destroy and
	// rebuild every live sandbox of the config - throwing away each session's
	// /workspace scratch - for a change no sandbox can observe.
	if imageChanged {
		s.markConfigSandboxesStale(ctx, tenantID, configID)
	}
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
		Percent: 100, Stage: "done", Status: "removed",
	})
	return nil
}

// restoreSkillAfterFailedRemoval puts back the row a failed removal borrowed.
func (s *TenantSkillService) restoreSkillAfterFailedRemoval(
	ctx context.Context, tenantID uint64, configID, skillID string, cause error,
) {
	owned, err := s.removeStillOwnsTheRow(ctx, tenantID, configID, skillID)
	if err != nil || !owned {
		return
	}
	status := types.SkillStatusFailed
	_ = s.updateSkillFields(ctx, tenantID, configID, skillID,
		func(e *types.TenantSkillEntity) {
			// A skill that reached an image is still in it, so it goes back to
			// ready and the operator can retry. One that never did has nothing
			// to be ready for, and calling it ready would point the agent at
			// files no image carries.
			if e.InstalledSnapshotID != "" {
				status = types.SkillStatusReady
			}
			e.Status = status
			e.Error = cause.Error()
			e.InstallingSince = nil
		})
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
		Percent: 100, Stage: "failed", Status: status, Log: cause.Error(),
	})
}

// removeStillOwnsTheRow is the lock-side counterpart of RemoveSkill's
// optimistic status flip. A newer upload of the same name already set the
// row back to installing; deleting it (or restoring it to ready/failed)
// would discard that install.
func (s *TenantSkillService) removeStillOwnsTheRow(
	ctx context.Context, tenantID uint64, configID, skillID string,
) (bool, error) {
	current, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return false, fmt.Errorf("load skill %s: %w", skillID, err)
	}
	if current == nil {
		return false, nil
	}
	return current.Status == types.SkillStatusRemoving, nil
}

// releaseInstallBundle drops an archive an install row has stopped naming,
// unless something else still names it.
//
// The reference is not owned by one row. A definition's own object is shared by
// every install of it, and one replaced archive is pinned by each install that
// was built from it, so "this row let go" is not the same question as "nobody
// needs these bytes". Both tables are checked before the object is deleted, and
// an unreadable table means it is kept: a leaked archive costs storage, a
// deleted one costs an install its files.
func (s *TenantSkillService) releaseInstallBundle(
	ctx context.Context, tenantID uint64, bundleRef string,
) {
	ref := strings.TrimSpace(bundleRef)
	if ref == "" {
		return
	}
	catalogs, err := s.skills.ListCatalogsByTenant(ctx, tenantID)
	if err != nil {
		logger.Warnf(ctx, "[skill] list catalogs before releasing bundle %s failed: %v", ref, err)
		return
	}
	for _, cat := range catalogs {
		if cat != nil && strings.TrimSpace(cat.BundleRef) == ref {
			return
		}
	}
	installs, err := s.skills.ListSkillsByTenant(ctx, tenantID)
	if err != nil {
		logger.Warnf(ctx, "[skill] list installs before releasing bundle %s failed: %v", ref, err)
		return
	}
	for _, row := range installs {
		if row != nil && strings.TrimSpace(row.BundleRef) == ref {
			return
		}
	}
	s.deleteBundleBestEffort(ctx, tenantID, ref)
}

func (s *TenantSkillService) deleteBundleBestEffort(
	ctx context.Context, tenantID uint64, bundleRef string,
) {
	fs, err := s.fileServiceForTenant(ctx, tenantID)
	if err != nil || fs == nil {
		logger.Warnf(ctx, "[skill] resolve file service to delete bundle %s failed: %v",
			bundleRef, err)
		return
	}
	if err := fs.DeleteFile(ctx, bundleRef); err != nil {
		logger.Warnf(ctx, "[skill] delete bundle %s failed: %v", bundleRef, err)
	}
}

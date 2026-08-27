package service

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	installCommandTimeout = 10 * time.Minute

	// installCleanupTimeout bounds the compensating work that must outlive the
	// install context: destroying the sandbox and writing the terminal row.
	installCleanupTimeout = 30 * time.Second

	// The terminal "ready" write happens after the pointer has moved, so it is
	// retried: the skill is already installed and serving, and the only thing
	// still missing is the row that says so.
	readySkillWriteAttempts = 3
	readySkillWriteDelay    = 100 * time.Millisecond

	// skillSeedArchivePath is the single remote write used to land a skill's
	// files. Writing each file with MakeDir+WriteFile is two round trips per
	// entry, which is why a 50-file skill crawled through "seeding 12/56".
	skillSeedArchivePath = sandbox.SkillsImageRoot + "/.weknora-seed.tar"
)

// InstallSkill validates an uploaded archive, records it, and kicks off the
// install in the background. It returns the skill ID so the caller can answer
// 202 and let the UI subscribe to progress.
func (s *TenantSkillService) InstallSkill(
	ctx context.Context, tenantID uint64, configID string, archive []byte,
) (string, error) {
	cfgEntity, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return "", err
	}
	if cfgEntity == nil {
		return "", apperrors.NewNotFoundError("sandbox config not found")
	}

	bundle, err := ParseSkillBundle(archive)
	if err != nil {
		return "", err
	}

	// Re-uploading a skill by the same name is an upgrade of that skill, not a
	// second row: the unique (config, name) index would reject the insert, and
	// the image directory is the name, so it stays put.
	existing, err := s.skills.GetSkillByName(ctx, tenantID, configID, bundle.Name)
	if err != nil {
		return "", err
	}
	if s.canSkipInstall(ctx, existing, bundle) {
		if err := s.refreshSkippedBundle(ctx, existing, archive); err != nil {
			return "", fmt.Errorf("store bundle for skill %s: %w", existing.ID, err)
		}
		return existing.ID, nil
	}

	skillID := uuid.NewString()
	now := s.now()
	if existing != nil {
		skillID = existing.ID
		takeSkillRowForInstall(existing, bundle, now)
		if err := s.skills.UpdateSkill(ctx, existing); err != nil {
			return "", err
		}
	} else {
		if err := s.skills.CreateSkill(ctx, &types.TenantSkillEntity{
			ID: skillID, TenantID: tenantID, SandboxConfigID: configID,
			Name: bundle.Name, Version: bundle.Version,
			Description: bundle.Description, Instructions: bundle.Instructions,
			BundleSHA256: bundle.SHA256, Enabled: true,
			Status: types.SkillStatusInstalling, InstallingSince: &now,
		}); err != nil {
			if !isSkillNameConflict(err) {
				return "", err
			}
			// Two first-time uploads of the same name raced the unique index.
			// Take the row that won rather than surfacing a 500.
			winner, lookupErr := s.skills.GetSkillByName(ctx, tenantID, configID, bundle.Name)
			if lookupErr != nil {
				return "", lookupErr
			}
			if winner == nil {
				return "", err
			}
			skillID = winner.ID
			takeSkillRowForInstall(winner, bundle, now)
			if err := s.skills.UpdateSkill(ctx, winner); err != nil {
				return "", err
			}
		}
	}

	// Store the archive before the long-running part: read_skill serves file
	// contents from it, and a re-install after a crash needs it too. A missing
	// archive is a failed install, not a warning — otherwise the row says
	// "installing" and later reads have nothing to serve.
	ref, err := s.saveBundle(ctx, tenantID, skillID, archive)
	if err != nil {
		failCtx, cancelFail := s.cleanupContext(ctx)
		defer cancelFail()
		storeErr := fmt.Errorf("store bundle: %w", err)
		logger.Errorf(ctx, "[skill] store bundle failed tenant=%d config=%s skill=%s name=%s: %v",
			tenantID, configID, skillID, bundle.Name, err)
		s.failSkill(failCtx, tenantID, configID, skillID, bundle, storeErr)
		return "", fmt.Errorf("store bundle for skill %s: %w", skillID, err)
	}
	_ = s.updateSkillFields(ctx, tenantID, configID, skillID, func(e *types.TenantSkillEntity) {
		e.BundleRef = ref
	})

	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
		Percent: 10, Stage: "accepted", Status: types.SkillStatusInstalling,
	})

	// The install outlives the HTTP request, so it must not inherit its
	// cancellation. It is not durable across a restart either - the stuck-run
	// reaper (Task 17) is what closes that gap.
	go func() {
		bgCtx := context.WithoutCancel(ctx)
		if err := s.withConfigLock(bgCtx, tenantID, configID, func(lockCtx context.Context) error {
			return s.runInstall(lockCtx, tenantID, configID, skillID, bundle)
		}); err != nil {
			logger.Errorf(bgCtx, "[skill] install %s failed: %v", skillID, err)
		}
	}()

	return skillID, nil
}

// takeSkillRowForInstall hands an existing row to the run about to start.
//
// The transcript locators are cleared along with the error. They name the
// session and message of the run that just ended, and a row that says
// "installing" while still pointing at them tells every reader that the
// finished conversation is this one's live output — which is how a retry came
// to replay the previous attempt's report before its own agent had started.
// They are rewritten by beginInstallTranscript once this run has a message of
// its own; until then the honest answer is that there is nothing to show yet.
func takeSkillRowForInstall(row *types.TenantSkillEntity, bundle *SkillBundle, now time.Time) {
	row.Version = bundle.Version
	row.Description = bundle.Description
	row.Instructions = bundle.Instructions
	row.BundleSHA256 = bundle.SHA256
	row.Status = types.SkillStatusInstalling
	row.Error = ""
	row.InstallingSince = &now
	row.InstallSessionID = ""
	row.InstallMessageID = ""
}

// ReinstallSkill runs the install again from the archive already stored for
// this skill. Most failed installs have nothing to do with the archive — an
// unreachable sandbox, a package index that timed out, a checker that has
// since been corrected — and making the operator find the original zip again
// (or the registry URL it came from) is a poor answer to any of them.
//
// It deliberately goes through InstallSkill rather than jumping to runInstall.
// That path owns the in-flight check, the row ownership handover and the
// per-config lock, and a retry is exactly the moment two installs of one
// config are most likely to overlap.
func (s *TenantSkillService) ReinstallSkill(
	ctx context.Context, tenantID uint64, configID, skillID string,
) (string, error) {
	skill, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return "", err
	}
	if skill == nil {
		return "", apperrors.NewNotFoundError("skill not found")
	}
	// Reported apart from a generic read failure: nothing a retry does can
	// recover a skill whose archive is gone, and the operator needs to be told
	// to upload it rather than to press the button again.
	if strings.TrimSpace(skill.BundleRef) == "" {
		return "", apperrors.NewBadRequestError(
			"the archive of this skill is no longer stored; install it again from the original bundle",
		)
	}
	archive, err := s.skillBundleArchive(ctx, tenantID, configID, skillID)
	if err != nil {
		return "", err
	}
	return s.InstallSkill(ctx, tenantID, configID, archive)
}

func (s *TenantSkillService) runInstall(
	ctx context.Context, tenantID uint64, configID, skillID string, bundle *SkillBundle,
) (err error) {
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	ctx = types.WithSandboxTenantID(ctx, tenantID)

	// Cleanup runs on a context that cannot be cancelled by whatever it is
	// compensating for. withConfigLock cancels ctx the moment lock renewal
	// fails, and the two things that must still happen then are a provider
	// call (destroy the sandbox, or it stays running and billed) and a DB write
	// (mark the skill failed, or the row sits at "installing" with the cause
	// only in a log line).
	//
	// Only cancellation is detached here; the deadline is NOT. An install runs
	// for minutes — a single agent command may take installCommandTimeout — so
	// a timeout started at this line would already be spent by the time any
	// compensating work begins. Each consumer calls cleanupContext to start its
	// own budget at the moment it needs one.
	cleanupBase := context.WithoutCancel(ctx)

	// pointerSwitched marks the point of no return. Past it the skill is
	// installed, snapshotted and serving every new session, so a later failure
	// is a bookkeeping failure and must not be reported as a failed install.
	pointerSwitched := false
	defer func() {
		if err == nil {
			return
		}
		if pointerSwitched {
			logger.Errorf(cleanupBase,
				"[skill] %s is installed and serving but its bookkeeping is incomplete: %v",
				skillID, err)
			return
		}
		// The image pointer is deliberately untouched on failure: the previous
		// snapshot keeps serving every session.
		failCtx, cancelFail := s.cleanupContext(cleanupBase)
		defer cancelFail()
		s.failSkill(failCtx, tenantID, configID, skillID, bundle, err)
	}()

	// InstallSkill queues this run before the per-config lock. A remove that
	// won the lock (or a newer upload of the same name) already owns the row;
	// snapshotting anyway would bake a skill the ledger no longer names.
	owned, err := s.installStillOwnsTheRow(ctx, tenantID, configID, skillID, bundle)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}

	// From here on this run is the row's owner, and everything below can take
	// minutes. The heartbeat is what tells a second upload of the same archive
	// (and the reaper) that those minutes are work rather than a dead process.
	// It is deferred before it is stopped explicitly below, so a failure path
	// still stops it ahead of the deferred failSkill.
	stopHeartbeat := s.startInstallHeartbeat(ctx, tenantID, configID, skillID)
	defer stopHeartbeat()

	// The name comes from SKILL.md and is already validated on parse, so a
	// rejection here means the bundle was accepted by a looser rule than the
	// one the image path enforces. Failing before any sandbox work keeps that
	// disagreement cheap.
	skillDir, err := sandbox.SkillDirFor(bundle.Name)
	if err != nil {
		return err
	}

	cfgEntity, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return fmt.Errorf("load sandbox config %s: %w", configID, err)
	}
	if cfgEntity == nil {
		return fmt.Errorf("sandbox config %s not found", configID)
	}
	if err := ensureUsableImage(cfgEntity); err != nil {
		return err
	}
	// Snapshots are private to the provider account that created them. The
	// fingerprint is captured here, from the credentials this sandbox will
	// actually use, and switchImagePointer refuses to stamp a later rotation
	// onto that ID — that would make the session layer trust a snapshot the
	// live key cannot resolve.
	builtFingerprint := skillOwnerFingerprint(cfgEntity.Config)
	if strings.TrimSpace(builtFingerprint) == "" {
		return fmt.Errorf(
			"sandbox config %s has no usable owner fingerprint for a skill image", configID,
		)
	}

	// 1. Install session + sandbox. ResolveEffectiveConfig has already turned
	//    the current snapshot into the template, so this sandbox boots from the
	//    existing image and this install stacks on top of it.
	sess, mgr, err := s.startMaintenanceSession(ctx, tenantID, configID, "install")
	if err != nil {
		return err
	}
	// Only the sandbox is released. The session and its messages stay so the
	// agent's install transcript can be read back when something goes wrong.
	defer func() {
		releaseCtx, cancelRelease := s.cleanupContext(cleanupBase)
		defer cancelRelease()
		s.releaseSandbox(releaseCtx, mgr, sess.ID)
	}()

	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{Percent: 25, Stage: "sandbox_ready"})

	// 2. Provision the target directory, then seed the source files
	//    server-side. The agent only installs dependencies; it never has to
	//    reconstruct the skill itself.
	if err := s.resetSkillDir(ctx, mgr, sess.ID, skillDir); err != nil {
		return err
	}

	// Locators must land before the file seed. A large skill is copied file by
	// file over the sandbox API and can take minutes; the console attaches to
	// the transcript as soon as the directory is ready, not after that copy.
	transcript, prompt := s.beginInstallTranscript(ctx, tenantID, skillID, sess, mgr, skillDir, bundle)

	fileCount := 0
	if bundle != nil {
		fileCount = len(bundle.Files)
	}
	if fileCount > 0 {
		logger.Infof(ctx, "[skill] seeding %d files for %s as one archive", fileCount, skillID)
		s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
			Percent: 28, Stage: "seeding",
			Log: fmt.Sprintf("seeding %d files", fileCount),
		})
	}
	if err := s.seedSkillFiles(ctx, mgr, sess.ID, skillDir, bundle); err != nil {
		return err
	}
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{Percent: 35, Stage: "seeded"})

	// 3. Let the installer agent install dependencies.
	if err := s.driveInstallerAgent(ctx, tenantID, skillID, sess, transcript, prompt); err != nil {
		return err
	}
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{Percent: 80, Stage: "agent_done"})

	// 4. Hand the tree to the execution user BEFORE verifying it. The agent
	//    created these files as root, and the language passes below
	//    deliberately run as the ordinary user: verifying first would test
	//    permissions that never reach the image, and a restrictive root umask
	//    would fail a perfectly good install because the .venv interpreter was
	//    unreadable.
	if err := s.normalizeSkillPermissions(ctx, mgr, sess.ID, skillDir); err != nil {
		return err
	}

	// 5. Verify ourselves. "The agent said it worked" is a sentence, not
	//    evidence, and this is the last gate before the image is switched.
	if err := s.verifySkill(ctx, mgr, sess.ID, skillDir, bundle); err != nil {
		return err
	}
	if err := s.writeManifestEntry(ctx, mgr, sess.ID, skillID, bundle); err != nil {
		return err
	}
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{Percent: 90, Stage: "verified"})

	// 6. Wipe the scratch state. It must happen BEFORE the snapshot, or the
	//    per-session workspace and every package cache land in the image.
	if err := s.cleanImageScratch(ctx, mgr, sess.ID); err != nil {
		return err
	}

	// 7. Snapshot. This is the point of no return: everything above is
	//    confined to a sandbox we are about to destroy, everything below
	//    creates provider resources and moves the pointer. withConfigLock
	//    cancels this context when the lock's renewal fails, and a lost lock
	//    means another install may already be building on the same config, so
	//    stop here rather than race it. The ledger row is written before the
	//    snapshot: a snapshot with no ledger entry is a resource nobody knows
	//    exists.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("install lock lost before snapshot: %w", err)
	}
	// Re-check after the minutes-long agent run: InstallSkill writes the row
	// outside this lock, so a newer upload or a queued remove may already own
	// it. Snapshotting anyway would bake a tree the ledger no longer names.
	owned, err = s.installStillOwnsTheRow(ctx, tenantID, configID, skillID, bundle)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	// Generation is max(live pointer, ledger)+1 so a build that died after
	// the commit but before the pointer moved cannot share a name with the
	// next install. withConfigLock still serialises writers of SkillImage;
	// the ledger read is what closes the crash window the lock cannot see.
	ledger, err := s.skills.ListSnapshotsByConfig(ctx, tenantID, configID)
	if err != nil {
		return fmt.Errorf("list snapshots of config %s: %w", configID, err)
	}
	generation := nextSnapshotGeneration(currentGeneration(cfgEntity), ledger)
	installRowID := uuid.NewString()
	// The name is recorded with the row rather than derived at the call below,
	// so a run that dies during the commit still leaves the ledger able to
	// name what it was building. Without it the snapshot would be a provider
	// resource nothing could ever address, let alone reclaim. The row id is
	// in the name so two rows of the same generation cannot share a tag.
	snapshotName := skillSnapshotBuildName(tenantID, configID, generation, installRowID)
	if err := s.skills.CreateSnapshotRow(ctx, &types.TenantSkillSnapshotEntity{
		ID: installRowID, TenantID: tenantID, SandboxConfigID: configID, SkillID: skillID,
		ParentSnapshotID: currentSnapshotID(cfgEntity), Generation: generation,
		Trigger: types.SkillSnapshotTriggerInstall, State: types.SkillSnapshotStateBuilding,
		PlannedName: snapshotName,
	}); err != nil {
		return err
	}
	ref, err := s.createSnapshot(ctx, mgr, sess.ID, snapshotName)
	if err != nil {
		return err
	}
	if err := s.skills.MarkSnapshotState(
		ctx, tenantID, installRowID, types.SkillSnapshotStateActive, ref.ID,
	); err != nil {
		// The snapshot's ID exists nowhere but this function's locals now, so
		// it is as unreachable as one nothing points at.
		s.abandonSnapshot(cleanupBase, tenantID, mgr, installRowID, ref.ID)
		return err
	}

	// 8. Switch the pointer. One DB write; everything after this is cleanup.
	if err := s.switchImagePointer(ctx, tenantID, configID, ref.ID, generation, builtFingerprint); err != nil {
		s.abandonSnapshot(cleanupBase, tenantID, mgr, installRowID, ref.ID)
		return err
	}
	pointerSwitched = true
	// The heartbeat writes the whole row, so it must be gone before the
	// terminal "ready" write below: a beat landing after it would put the row
	// back to installing and have the reaper fail a skill that is serving.
	stopHeartbeat()
	s.markPreviousSnapshotsSuperseded(ctx, tenantID, configID, installRowID)

	// The terminal write is the one that must not be best-effort: the pointer
	// already moved, so a row left at "installing" would be reaped as failed
	// even though the skill is installed and serving.
	readyCtx, cancelReady := s.cleanupContext(cleanupBase)
	defer cancelReady()
	if err := s.writeReadySkillState(readyCtx, tenantID, configID, skillID, ref.ID, bundle); err != nil {
		return err
	}
	s.markConfigSandboxesStale(ctx, tenantID, configID)
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
		Percent: 100, Stage: "done", Status: types.SkillStatusReady,
	})
	return nil
}

// ensureUsableImage refuses to grow the image when the stored snapshot is one
// the live credentials cannot resolve.
//
// Snapshots are private to a provider account, so a rotated credential turns
// the recorded pointer into an ID that no longer exists for us: the session
// layer notices via the fingerprint and boots the BASE TEMPLATE instead. A run
// that ignored that would do its work in a sandbox carrying none of the
// tenant's skills, snapshot that, and switch the pointer to it with a
// fingerprint that now matches - so it sticks, while every skill row still
// claims ready. Until then the old image is still recoverable by restoring the
// credential, which is why both flows stop here instead of committing the loss.
func ensureUsableImage(cfgEntity *types.TenantSandboxConfigEntity) error {
	if cfgEntity == nil || cfgEntity.Config == nil {
		return nil
	}
	image := cfgEntity.Config.SkillImage
	if image == nil || strings.TrimSpace(image.SnapshotID) == "" {
		return nil
	}
	// An empty fingerprint on either side is the same condition: the session
	// layer keeps the base template for both.
	live := skillOwnerFingerprint(cfgEntity.Config)
	if live != "" && image.OwnerFingerprint == live {
		return nil
	}
	return fmt.Errorf(
		"skill image %s of sandbox config %s belongs to another provider account; "+
			"restore the credentials it was built with or rebuild the image",
		image.SnapshotID, cfgEntity.ID,
	)
}

// abandonSnapshot disposes of a snapshot that was created but never became
// reachable - either because the ledger could not be told its ID or because
// the pointer switch failed. Both leave a provider resource that is billed and
// that nothing can ever boot again, and this is the only snapshot delete
// either flow performs: an ordinary switch leaves the previous image reachable
// for the ledger.
//
// It runs on a detached context because a cancelled one is among the reasons
// the caller failed, and that is exactly when a leaked snapshot is most likely.
func (s *TenantSkillService) abandonSnapshot(
	cleanupBase context.Context, tenantID uint64, mgr sandbox.Manager, rowID, snapshotID string,
) {
	ctx, cancel := s.cleanupContext(cleanupBase)
	defer cancel()
	s.deleteSnapshotBestEffort(ctx, mgr, snapshotID)
	_ = s.skills.MarkSnapshotState(
		ctx, tenantID, rowID, types.SkillSnapshotStateDeleted, snapshotID)
}

// cleanupContext bounds one piece of compensating work, starting the budget
// when that work starts. Anchoring it earlier would hand every consumer an
// already-expired deadline, because an install takes minutes to reach them.
func (s *TenantSkillService) cleanupContext(
	base context.Context,
) (context.Context, context.CancelFunc) {
	timeout := s.cleanupTimeout
	if timeout <= 0 {
		timeout = installCleanupTimeout
	}
	return context.WithTimeout(base, timeout)
}

// resetSkillDir makes the target directory an empty, writable directory owned
// by the execution user.
//
// Emptying it is what makes a re-install an install of exactly the uploaded
// bundle: the sandbox boots from the inherited image, so a file that the
// previous version shipped and this one dropped would otherwise live on in
// every later snapshot while BundleSHA256 claims the tree equals the archive.
//
// Creating and chowning it is what makes the FIRST install work at all:
// seeding goes through the provider file API, which runs as the default exec
// user, and the skills root is not part of the base image.
func (s *TenantSkillService) resetSkillDir(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string,
) error {
	if err := guardSkillDir(skillDir); err != nil {
		return err
	}
	root := sandbox.ShellQuote(sandbox.SkillsImageRoot)
	dir := sandbox.ShellQuote(skillDir)
	user := sandbox.DefaultSandboxExecUser
	cmd := fmt.Sprintf(
		"rm -rf %s && mkdir -p %s %s && chown %s:%s %s %s && chmod 755 %s %s",
		dir, root, dir, user, user, root, dir, root, dir,
	)
	if _, err := s.execInstall(ctx, mgr, sessionID, cmd); err != nil {
		return fmt.Errorf("reset skill directory %s: %w", skillDir, err)
	}
	return nil
}

// guardSkillDir refuses the one path no destructive skill command may target.
// SkillDirFor("") collapses to the skills root, so an empty skill name would
// turn any "rm -rf <dir>" into a wipe of every skill in the image. No caller
// can pass an empty name today; this is the invariant that keeps that true,
// because the failure is unrecoverable.
func guardSkillDir(skillDir string) error {
	if path.Clean(skillDir) == path.Clean(sandbox.SkillsImageRoot) {
		return fmt.Errorf("refusing to use the skills root %q as a skill directory", skillDir)
	}
	return nil
}

// seedSkillFiles writes the archive contents into the image. Doing it
// server-side rather than asking the agent to unpack keeps the source of truth
// byte-identical to what was uploaded. Provisioning the directory is
// runInstall's step 2, not this function's job.
//
// The files travel as one tar: each remote WriteFile is a MakeDir plus an
// upload, and a skill with dozens of files was spending minutes on that
// round-trip tax. Extracting inside the sandbox is one local untar.
func (s *TenantSkillService) seedSkillFiles(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string, bundle *SkillBundle,
) error {
	if bundle == nil || len(bundle.Files) == 0 {
		return nil
	}
	store, err := installFileStore(mgr)
	if err != nil {
		return err
	}
	archive, err := packSkillTar(bundle)
	if err != nil {
		return err
	}
	if err := store.WriteSessionFile(ctx, sessionID, skillSeedArchivePath, archive); err != nil {
		return fmt.Errorf("seed skill archive: %w", err)
	}
	if _, err := s.execInstall(ctx, mgr, sessionID, seedExtractCommand(skillDir)); err != nil {
		return fmt.Errorf("extract skill archive: %w", err)
	}
	return nil
}

func seedExtractCommand(skillDir string) string {
	tarPath := sandbox.ShellQuote(skillSeedArchivePath)
	dir := sandbox.ShellQuote(skillDir)
	return fmt.Sprintf("tar -xf %s -C %s && rm -f %s", tarPath, dir, tarPath)
}

func packSkillTar(bundle *SkillBundle) ([]byte, error) {
	if bundle == nil {
		return nil, nil
	}
	names := make([]string, 0, len(bundle.Files))
	for rel := range bundle.Files {
		names = append(names, rel)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, rel := range names {
		name := path.Clean(rel)
		if name == "." || name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return nil, fmt.Errorf("skill file %q escapes the archive root", rel)
		}
		content := bundle.Files[rel]
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// beginInstallTranscript writes the locators and opening prompt after the
// skill directory is reset, so a console that opens during the file seed
// finds something to follow instead of 404-polling for minutes.
func (s *TenantSkillService) beginInstallTranscript(
	ctx context.Context, tenantID uint64, skillID string,
	sess *types.Session, mgr sandbox.Manager, skillDir string, bundle *SkillBundle,
) (*installTranscript, string) {
	assistantMessageID := uuid.NewString()
	prompt := buildInstallPrompt(skillDir, bundle, s.probeUv(ctx, mgr, sess.ID))
	transcript := newInstallTranscript(ctx, event.NewEventBus(), s.streams, s.messages, sess.ID, assistantMessageID)
	if err := transcript.Create(ctx, prompt); err != nil {
		logger.Warnf(ctx, "[skill] seed install transcript for %s failed: %v", skillID, err)
	}
	transcript.Subscribe()
	if err := s.updateSkillFields(ctx, tenantID, sess.SandboxConfigID, skillID,
		func(e *types.TenantSkillEntity) {
			e.InstallSessionID = sess.ID
			e.InstallMessageID = assistantMessageID
		}); err != nil {
		logger.Warnf(ctx, "[skill] record transcript locators for %s failed: %v", skillID, err)
	}
	return transcript, prompt
}

// driveInstallerAgent runs one installer conversation. It calls the engine
// directly instead of going through sessionService.AgentQA, because AgentQA
// swallows engine failures (it emits an error event and returns nil) and we
// need a reliable signal before switching the image.
func (s *TenantSkillService) driveInstallerAgent(
	ctx context.Context, tenantID uint64, skillID string, sess *types.Session,
	transcript *installTranscript, prompt string,
) error {
	if s.installerAgents == nil {
		return errors.New("custom agent service is not configured")
	}
	if transcript == nil {
		return errors.New("install transcript was not seeded")
	}
	// The record is tenant-writable: updateBuiltinAgent lets a tenant persist a
	// Config for any built-in ID, this one included. It is therefore read for
	// the model choice only. What the root shell is told to do comes from the
	// platform's own registry entry.
	record, err := s.installerAgents.GetAgentByID(ctx, types.BuiltinSkillInstallerID)
	if err != nil {
		return fmt.Errorf("load installer agent: %w", err)
	}
	agentConfig := installerAgentConfig(installerAgentDefaults(ctx, tenantID), sess.SandboxConfigID)

	chatModel, err := s.resolveInstallerModel(ctx, tenantID, record)
	if err != nil {
		return err
	}

	engine, err := s.agents.CreateAgentEngine(
		ctx, agentConfig, chatModel, nil, transcript.bus, sess.ID, transcript.assistantMessageID,
	)
	if err != nil {
		runErr := fmt.Errorf("create installer engine: %w", err)
		transcript.Finish(ctx, runErr)
		return runErr
	}

	state, err := engine.Execute(ctx, sess.ID, transcript.assistantMessageID, prompt, nil)
	runErr := err
	if runErr == nil && (state == nil || !state.IsComplete) {
		runErr = errors.New("installer agent stopped without completing")
	}
	// Detached from cancellation: when the install lock's renewal fails the
	// context dies, and the transcript of the run that just died is precisely
	// what someone will want to read.
	transcript.Finish(context.WithoutCancel(ctx), runErr)
	if runErr != nil {
		if err != nil {
			return fmt.Errorf("installer agent failed: %w", err)
		}
		return runErr
	}
	return nil
}

// normalizeSkillPermissions makes the skill tree readable and executable by
// the non-root execution user, and writable by nobody. Installs run as root,
// so without the mode change the user that actually runs skills could not read
// them at all — which is also why this runs before verification: the checks
// read every script as that user, so they have to see the permissions the
// snapshot will carry.
//
// Ownership stays with root rather than moving to the execution user because
// this tree is baked into an image every session of the config inherits. A
// session that could write here would be editing the skills every other
// session runs. 555 leaves reads and execs to the "other" bits, which is all a
// skill run needs; the cost is that Python cannot write __pycache__ into the
// tree and silently skips bytecode caching.
func (s *TenantSkillService) normalizeSkillPermissions(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillDir string,
) error {
	cmds := []string{
		fmt.Sprintf("chmod -R 555 %s", sandbox.ShellQuote(skillDir)),
		fmt.Sprintf("chown -R root:root %s", sandbox.ShellQuote(skillDir)),
	}
	for _, cmd := range cmds {
		if _, err := s.execInstall(ctx, mgr, sessionID, cmd); err != nil {
			return fmt.Errorf("normalize skill permissions (%s): %w", cmd, err)
		}
	}
	return nil
}

// cleanImageScratch wipes the state that must not reach the snapshot: the
// per-session workspace and the package download caches.
func (s *TenantSkillService) cleanImageScratch(
	ctx context.Context, mgr sandbox.Manager, sessionID string,
) error {
	user := sandbox.DefaultSandboxExecUser
	inputRoot := sandbox.ShellQuote(sandbox.SessionInputRoot)
	outputRoot := sandbox.ShellQuote(sandbox.SessionOutputRoot)
	cmds := []string{
		"rm -rf /workspace/* /workspace/.[!.]* || true",
		// The wipe above takes the base image's own input/output directories
		// with it, so every session booting from this snapshot would start on
		// a bare /workspace. Restoring them here is the only place with root:
		// a provider whose filesystem API runs as root would otherwise
		// recreate them root-owned, and the session account can neither write
		// them nor take them over.
		fmt.Sprintf(
			"mkdir -p %s %s && chown %s:%s %s %s && chmod 775 %s %s",
			inputRoot, outputRoot,
			user, user, inputRoot, outputRoot,
			inputRoot, outputRoot,
		),
		// Spelled out for both accounts on purpose: this runs as root, so "~"
		// would only ever clear root's caches, while the agent's own installs
		// populate the exec user's caches and those are what reach the image.
		"rm -rf " + strings.Join(append(
			packageCachePaths("/root"),
			packageCachePaths(path.Join("/home", sandbox.DefaultSandboxExecUser))...,
		), " ") + " || true",
	}
	for _, cmd := range cmds {
		if _, err := s.execInstall(ctx, mgr, sessionID, cmd); err != nil {
			return fmt.Errorf("clean image scratch (%s): %w", cmd, err)
		}
	}
	return nil
}

// packageCachePaths lists the download caches the three package managers we
// support keep under a home directory. They are scratch by definition and
// would otherwise be snapshotted into every future session's image.
func packageCachePaths(home string) []string {
	return []string{
		path.Join(home, ".cache", "pip"),
		path.Join(home, ".cache", "uv"),
		path.Join(home, ".npm"),
		path.Join(home, ".local", "share", "pnpm", "store"),
	}
}

// describeExecFailure renders everything the executor knows about a failed
// command. Transport failures and timeouts arrive as ExitCode -1 with an empty
// Stderr and the cause in Error, so formatting Stderr alone leaves the admin
// with "command failed (exit -1): " and nothing else.
func describeExecFailure(res *sandbox.ExecuteResult) string {
	if res == nil {
		return "no result from the sandbox"
	}
	parts := []string{fmt.Sprintf("exit %d", res.ExitCode)}
	if res.Killed {
		parts = append(parts, "killed")
	}
	if cause := strings.TrimSpace(res.Error); cause != "" {
		parts = append(parts, "error: "+cause)
	}
	if stderr := strings.TrimSpace(res.Stderr); stderr != "" {
		parts = append(parts, "stderr: "+stderr)
	}
	return strings.Join(parts, "; ")
}

// installExecutor resolves the one executor every command of an install runs
// through. It goes through the capability accessor rather than a bare type
// assertion so a manager that cannot run install-mode shell reports no
// capability instead of attempting the install on the WeKnora host.
func installExecutor(mgr sandbox.Manager) (sandbox.SessionInstallShellExecutor, error) {
	executor := sessionSandboxInstallShellExecutor(mgr)
	if executor == nil {
		return nil, errors.New("sandbox backend does not support install-mode shell")
	}
	return executor, nil
}

// execInstall runs one command as root with the skills root allowed.
func (s *TenantSkillService) execInstall(
	ctx context.Context, mgr sandbox.Manager, sessionID, command string,
) (*sandbox.ExecuteResult, error) {
	executor, err := installExecutor(mgr)
	if err != nil {
		return nil, err
	}
	res, err := executor.ExecShellCommandWithOptions(ctx, sessionID, command,
		sandbox.ShellExecOptions{AsRoot: true, AllowSkillsRoot: true, Timeout: installCommandTimeout})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("command failed (%s)", describeExecFailure(res))
	}
	return res, nil
}

func (s *TenantSkillService) tenantForStorage(ctx context.Context, tenantID uint64) *types.Tenant {
	if info, ok := types.TenantInfoFromContext(ctx); ok && info.ID == tenantID {
		return info
	}
	return &types.Tenant{ID: tenantID}
}

func (s *TenantSkillService) fileServiceForTenant(
	ctx context.Context, tenantID uint64,
) (interfaces.FileService, error) {
	if s.resolver == nil {
		return nil, errors.New("storage resolver is not configured")
	}
	fs, _, err := s.resolver.ResolveFileService(ctx, s.tenantForStorage(ctx, tenantID), "", "", "")
	if err != nil {
		return nil, err
	}
	if fs == nil {
		return nil, errors.New("file service is not configured")
	}
	return fs, nil
}

func (s *TenantSkillService) saveBundle(
	ctx context.Context, tenantID uint64, skillID string, archive []byte,
) (string, error) {
	fs, err := s.fileServiceForTenant(ctx, tenantID)
	if err != nil {
		return "", err
	}
	return fs.SaveBytes(ctx, archive, tenantID, fmt.Sprintf("tenant-skills/%s.zip", skillID), false)
}

// updateSkillFields loads, mutates and writes back one skill row. It logs on
// failure for the progress-only call sites and returns the error for the one
// call site where the row is the record of a completed install.
func (s *TenantSkillService) updateSkillFields(
	ctx context.Context,
	tenantID uint64,
	configID, skillID string,
	mutate func(*types.TenantSkillEntity),
) error {
	skill, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		logger.Warnf(ctx, "[skill] load %s for update failed: %v", skillID, err)
		return fmt.Errorf("load skill %s: %w", skillID, err)
	}
	if skill == nil {
		return fmt.Errorf("skill %s not found", skillID)
	}
	mutate(skill)
	if err := s.skills.UpdateSkill(ctx, skill); err != nil {
		logger.Warnf(ctx, "[skill] update %s failed: %v", skillID, err)
		return fmt.Errorf("update skill %s: %w", skillID, err)
	}
	return nil
}

// writeReadySkillState records the finished install. It is retried because it
// runs after the pointer switch: the skill is installed, snapshotted and being
// served, so a transient write failure here is a bookkeeping gap, not a failed
// install, and re-running the whole install to fix a row would be absurd.
//
// The write is skipped when a newer upload or a queued remove already owns
// the row: the pointer still moved, but stamping ready (or this run's
// snapshot id) on that row would lie about which bundle is serving.
func (s *TenantSkillService) writeReadySkillState(
	ctx context.Context, tenantID uint64, configID, skillID, snapshotID string, bundle *SkillBundle,
) error {
	if err := s.retrySkillBookkeeping(ctx, func() error {
		owned, err := s.installStillOwnsTheRow(ctx, tenantID, configID, skillID, bundle)
		if err != nil {
			return err
		}
		if !owned {
			return nil
		}
		return s.updateSkillFields(ctx, tenantID, configID, skillID,
			func(e *types.TenantSkillEntity) {
				e.Status = types.SkillStatusReady
				e.Error = ""
				e.InstalledSnapshotID = snapshotID
				e.InstallingSince = nil
			})
	}); err != nil {
		return fmt.Errorf("skill %s is installed and serving but could not be marked ready: %w",
			skillID, err)
	}
	return nil
}

// retrySkillBookkeeping runs one terminal bookkeeping write past the point of
// no return, where the image already changed and only the row recording it is
// missing. Both callers are in that position, so both retry instead of
// reporting the whole run as failed.
func (s *TenantSkillService) retrySkillBookkeeping(
	ctx context.Context, write func() error,
) error {
	var lastErr error
	for attempt := 1; attempt <= readySkillWriteAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-time.After(readySkillWriteDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if lastErr = write(); lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (s *TenantSkillService) failSkill(
	ctx context.Context, tenantID uint64, configID, skillID string, bundle *SkillBundle, cause error,
) {
	owned, err := s.installStillOwnsTheRow(ctx, tenantID, configID, skillID, bundle)
	if err != nil || !owned {
		return
	}
	_ = s.updateSkillFields(ctx, tenantID, configID, skillID, func(e *types.TenantSkillEntity) {
		e.Status = types.SkillStatusFailed
		e.Error = cause.Error()
		e.InstallingSince = nil
	})
	s.publishProgress(ctx, tenantID, configID, skillID, SkillProgress{
		Percent: 100, Stage: "failed", Status: types.SkillStatusFailed, Log: cause.Error(),
	})
}

// installStillOwnsTheRow is the lock-side counterpart of InstallSkill's
// optimistic row write. A remove that ran first deleted the row; a newer
// upload of the same name replaced BundleSHA256; a queued remove flipped the
// status; a sibling retry of the same archive found the first run had already
// landed in the live image. Any of those means this run must not snapshot —
// failSkill would stamp the newer owner's row, and a snapshot with no matching
// row is an orphan the ledger cannot name.
func (s *TenantSkillService) installStillOwnsTheRow(
	ctx context.Context, tenantID uint64, configID, skillID string, bundle *SkillBundle,
) (bool, error) {
	current, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		return false, fmt.Errorf("load skill %s: %w", skillID, err)
	}
	if current == nil {
		return false, nil
	}
	if current.Status == types.SkillStatusRemoving {
		return false, nil
	}
	if bundle != nil && current.BundleSHA256 != "" && current.BundleSHA256 != bundle.SHA256 {
		return false, nil
	}
	if current.Status == types.SkillStatusReady {
		_, inImage, ok := s.skillFilesInLiveImage(ctx, current)
		if ok && inImage {
			return false, nil
		}
	}
	return true, nil
}

// canSkipInstall reports whether this upload is a no-op. Re-uploading the
// exact archive of a skill that is already ready (and still in the live image)
// must not boot a billed sandbox or grow a new snapshot. An install of the
// same bytes that is still beating is the same situation: the first run owns
// the work.
//
// Only a ready row is answered from the image. An installing row is answered
// from the heartbeat alone, deliberately: the ledger records which skill an
// install snapshotted, not which archive, so a row that is installing bundle
// B while the image still carries the earlier bundle A would look "already
// installed" and this upload would report a success that never happened.
//
// A failed skill with the same digest is a retry: the previous attempt never
// made it into the image. A removal in flight is not a skip either — taking
// the row back to installing is how an upload cancels it.
func (s *TenantSkillService) canSkipInstall(
	ctx context.Context, existing *types.TenantSkillEntity, bundle *SkillBundle,
) bool {
	if existing == nil || bundle == nil {
		return false
	}
	if existing.BundleSHA256 == "" || existing.BundleSHA256 != bundle.SHA256 {
		return false
	}
	switch existing.Status {
	case types.SkillStatusInstalling:
		return s.installIsInFlight(existing)
	case types.SkillStatusReady:
		_, inImage, ok := s.skillFilesInLiveImage(ctx, existing)
		return ok && inImage
	default:
		return false
	}
}

// installIsInFlight reports whether an installing row still belongs to a live
// process. The answer is the heartbeat: a running install restamps
// InstallingSince every skillInstallHeartbeatInterval, so silence past
// skillInstallInFlightSkip means the process is gone and the next upload must
// be allowed to start a new run rather than wait for the stuck-run reaper.
//
// Reading the submission time instead would force a choice between calling a
// slow install dead — a single agent command may take installCommandTimeout,
// and an install runs several — and leaving a dead one unrecoverable.
func (s *TenantSkillService) installIsInFlight(existing *types.TenantSkillEntity) bool {
	if existing == nil || existing.InstallingSince == nil {
		return false
	}
	return !existing.InstallingSince.Before(s.clock()().Add(-skillInstallInFlightSkip))
}

// startInstallHeartbeat keeps this run's liveness visible while it works, and
// returns the stop function that must be called before any terminal write.
//
// The heartbeat writes the whole row, so it would otherwise race the "ready"
// write past the pointer switch and revive an installing status. Both callers
// stop it before that point: runInstall stops it the moment the pointer moves,
// and the deferred stop runs before the deferred failSkill.
func (s *TenantSkillService) startInstallHeartbeat(
	ctx context.Context, tenantID uint64, configID, skillID string,
) func() {
	interval := s.installHeartbeat
	if interval <= 0 {
		interval = skillInstallHeartbeatInterval
	}
	beatCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-beatCtx.Done():
				return
			case <-ticker.C:
				s.beatInstallHeartbeat(beatCtx, tenantID, configID, skillID)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			stop()
			<-done
		})
	}
}

// beatInstallHeartbeat stamps InstallingSince for a row this run still owns.
// A row that has left the installing status belongs to a newer upload, a
// queued removal, or a finished run, and reviving its timestamp would hide
// one of those from the reaper.
func (s *TenantSkillService) beatInstallHeartbeat(
	ctx context.Context, tenantID uint64, configID, skillID string,
) {
	current, err := s.skills.GetSkill(ctx, tenantID, configID, skillID)
	if err != nil {
		logger.Warnf(ctx, "[skill] load %s for install heartbeat failed: %v", skillID, err)
		return
	}
	if current == nil || current.Status != types.SkillStatusInstalling {
		return
	}
	at := s.clock()()
	current.InstallingSince = &at
	if err := s.skills.UpdateSkill(ctx, current); err != nil {
		logger.Warnf(ctx, "[skill] install heartbeat for %s failed: %v", skillID, err)
	}
}

// refreshSkippedBundle stores the uploaded archive even when the image work
// is skipped. read_skill serves file contents from it, so a re-upload of a
// ready skill is how a missing object-store blob gets repaired without
// growing a new snapshot. A failure here is returned to the caller rather
// than turning the ready row into a failed install.
func (s *TenantSkillService) refreshSkippedBundle(
	ctx context.Context, existing *types.TenantSkillEntity, archive []byte,
) error {
	if existing == nil {
		return nil
	}
	ref, err := s.saveBundle(ctx, existing.TenantID, existing.ID, archive)
	if err != nil {
		return err
	}
	return s.updateSkillFields(ctx, existing.TenantID, existing.SandboxConfigID, existing.ID,
		func(e *types.TenantSkillEntity) {
			e.BundleRef = ref
		})
}

// startMaintenanceSession opens the session one image operation runs in. The
// operation name is carried into the session because the transcript is kept
// deliberately, for troubleshooting: filing a removal's under "Skill install"
// would send whoever reads it looking at the wrong operation.
//
// The description marker is what keeps this row out of the console's session
// list, and the owner is the admin who started the operation, so a transcript
// is readable by the person who caused it rather than by the whole workspace.
func (s *TenantSkillService) startMaintenanceSession(
	ctx context.Context, tenantID uint64, configID, operation string,
) (*types.Session, sandbox.Manager, error) {
	if s.sessions == nil {
		return nil, nil, errors.New("session service is not configured")
	}
	// Honour the workspace kill switch before creating a billed sandbox or a
	// session row. resolveTenantSandboxForConfig is the same choke point every
	// other sandbox caller uses; going through TenantSandboxResolver.Resolve
	// directly would let an install run while scripts are disabled.
	mgr, err := resolveTenantSandboxForConfig(ctx, s.sandboxes, nil, tenantID, configID, s.sandboxPolicy)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve sandbox config: %w", err)
	}
	if mgr == nil {
		return nil, nil, errors.New("sandbox resolver returned nil manager")
	}
	if mgr.GetType() == sandbox.SandboxTypeDisabled {
		return nil, nil, errors.New("sandbox execution is disabled for this workspace")
	}
	sess, err := s.sessions.CreateSession(ctx, &types.Session{
		TenantID:        tenantID,
		UserID:          sessionUserIDFromContext(ctx),
		Title:           "Skill " + operation,
		Description:     types.SkillMaintenanceSessionMarker + operation,
		SandboxConfigID: configID,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create %s session: %w", operation, err)
	}
	if sess == nil {
		return nil, nil, fmt.Errorf("create %s session returned nil", operation)
	}
	return sess, mgr, nil
}

func (s *TenantSkillService) releaseSandbox(ctx context.Context, mgr sandbox.Manager, sessionID string) {
	destroyer, ok := mgr.(sandbox.SessionDestroyer)
	if !ok {
		return
	}
	if err := destroyer.DestroySession(ctx, sessionID); err != nil {
		logger.Warnf(ctx, "[skill] destroy install sandbox for session %s failed: %v", sessionID, err)
	}
}

func (s *TenantSkillService) createSnapshot(
	ctx context.Context, mgr sandbox.Manager, sessionID, name string,
) (sandbox.RemoteSnapshotRef, error) {
	snapshots, ok := mgr.(sandbox.RemoteSnapshotManager)
	if !ok {
		return sandbox.RemoteSnapshotRef{}, errors.New("sandbox backend does not support snapshots")
	}
	ref, err := snapshots.CreateSnapshot(ctx, sessionID, name)
	if err != nil {
		return sandbox.RemoteSnapshotRef{}, fmt.Errorf("create snapshot: %w", err)
	}
	if strings.TrimSpace(ref.ID) == "" {
		return sandbox.RemoteSnapshotRef{}, errors.New("create snapshot returned empty id")
	}
	return ref, nil
}

func (s *TenantSkillService) deleteSnapshotBestEffort(
	ctx context.Context, mgr sandbox.Manager, snapshotID string,
) {
	snapshots, ok := mgr.(sandbox.RemoteSnapshotManager)
	if !ok {
		return
	}
	if err := snapshots.DeleteSnapshot(ctx, snapshotID); err != nil {
		logger.Warnf(ctx, "[skill] delete orphan snapshot %s failed: %v", snapshotID, err)
	}
}

// switchImagePointer is the single DB write that makes the new snapshot the
// image every future session boots from.
//
// The config is re-read here rather than reused from the read at the top of
// runInstall, which happened minutes and one whole agent conversation ago. The
// repository Update overwrites name, description, sandbox type and the entire
// config blob, and config edits are serialised by the config service's cordon
// rather than by this task's lock — so writing back the stale entity would
// silently revert an admin's rename or credential rotation.
//
// builtFingerprint is the account the maintenance sandbox (and therefore the
// snapshot) actually belongs to. A live fingerprint that no longer matches
// means the credentials rotated while this run was in flight: stamping the
// new fingerprint onto the old snapshot would make the session layer trust an
// ID the live key cannot resolve. Abort instead, and leave the previous image
// in place.
func (s *TenantSkillService) switchImagePointer(
	ctx context.Context,
	tenantID uint64,
	configID string,
	snapshotID string,
	generation int,
	builtFingerprint string,
) error {
	cfgEntity, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return fmt.Errorf("re-read sandbox config %s: %w", configID, err)
	}
	if cfgEntity == nil || cfgEntity.Config == nil {
		return fmt.Errorf("sandbox config %s disappeared during the install", configID)
	}

	if strings.TrimSpace(builtFingerprint) == "" {
		return fmt.Errorf(
			"sandbox config %s has no usable owner fingerprint for a skill image", configID,
		)
	}
	live := skillOwnerFingerprint(cfgEntity.Config)
	if live != builtFingerprint {
		return fmt.Errorf(
			"sandbox config %s credentials changed during the image build; "+
				"the snapshot belongs to the previous provider account",
			configID,
		)
	}

	// Only the SkillImage portion is touched; everything else in the entity is
	// whatever the latest read says it is.
	cfgEntity.Config.SkillImage = &types.SkillImageConfig{
		SnapshotID:       snapshotID,
		Generation:       generation,
		BuiltAt:          s.now(),
		BaseTemplateID:   effectiveBaseTemplate(cfgEntity),
		OwnerFingerprint: builtFingerprint,
	}
	cfgEntity.TenantID = tenantID
	return s.configs.Update(ctx, cfgEntity)
}

// effectiveBaseTemplate returns the template the image chain grew from. Once
// recorded it is never re-derived, because the config's own template field is
// overwritten with the snapshot ID whenever a session resolves the config.
func effectiveBaseTemplate(cfgEntity *types.TenantSandboxConfigEntity) string {
	if cfgEntity == nil || cfgEntity.Config == nil {
		return ""
	}
	if image := cfgEntity.Config.SkillImage; image != nil &&
		strings.TrimSpace(image.BaseTemplateID) != "" {
		return image.BaseTemplateID
	}
	return currentBaseTemplate(cfgEntity.Config)
}

func (s *TenantSkillService) markPreviousSnapshotsSuperseded(
	ctx context.Context, tenantID uint64, configID, currentRowID string,
) {
	rows, err := s.skills.ListSnapshotsByConfig(ctx, tenantID, configID)
	if err != nil {
		logger.Warnf(ctx, "[skill] list snapshots for supersede failed: %v", err)
		return
	}
	for _, row := range rows {
		if row == nil || row.ID == currentRowID || row.State != types.SkillSnapshotStateActive {
			continue
		}
		if err := s.skills.MarkSnapshotState(
			ctx, tenantID, row.ID, types.SkillSnapshotStateSuperseded, row.SnapshotID,
		); err != nil {
			logger.Warnf(ctx, "[skill] mark snapshot %s superseded failed: %v", row.ID, err)
		}
	}
}

func currentGeneration(cfgEntity *types.TenantSandboxConfigEntity) int {
	if cfgEntity == nil || cfgEntity.Config == nil || cfgEntity.Config.SkillImage == nil {
		return 0
	}
	return cfgEntity.Config.SkillImage.Generation
}

func currentSnapshotID(cfgEntity *types.TenantSandboxConfigEntity) string {
	if cfgEntity == nil || cfgEntity.Config == nil || cfgEntity.Config.SkillImage == nil {
		return ""
	}
	return cfgEntity.Config.SkillImage.SnapshotID
}

func skillSnapshotNamePrefix(tenantID uint64, configID string) string {
	return fmt.Sprintf("weknora-sk-t%d-%s", tenantID, compactConfigID(configID))
}

// nextSnapshotGeneration is one past both the live pointer and every ledger
// row. An abandoned building row still occupies its generation; reusing it
// would mint the same planned name and let the reaper delete a later install's
// snapshot.
func nextSnapshotGeneration(live int, rows []*types.TenantSkillSnapshotEntity) int {
	highest := live
	for _, row := range rows {
		if row != nil && row.Generation > highest {
			highest = row.Generation
		}
	}
	if highest < 0 {
		highest = 0
	}
	return highest + 1
}

func compactSnapshotToken(id string) string {
	s := compactConfigID(id)
	const n = 8
	if len(s) > n {
		return s[:n]
	}
	if s == "" {
		return "row"
	}
	return s
}

// skillSnapshotBuildName is the name every generation of a config's image
// chain is committed under. It is recorded on the ledger row before the
// provider call so an abandoned build stays identifiable. Tenant and the
// full config id are in the name because Cube, E2B and Docker all list
// snapshots across a shared account or daemon; the row token stops two
// builds of the same generation from sharing a tag.
func skillSnapshotBuildName(tenantID uint64, configID string, generation int, rowID string) string {
	prefix := skillSnapshotNamePrefix(tenantID, configID)
	return fmt.Sprintf("%s-g%d-%s", prefix, generation, compactSnapshotToken(rowID))
}

func compactConfigID(id string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), "-", ""))
}

// weknoraSkillSnapshotName pulls the weknora-sk-… token out of a provider
// listing. Cube and E2B echo it in Names; Docker embeds it in the image tag.
func weknoraSkillSnapshotName(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "docker.io/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if cut := strings.IndexByte(s, ':'); cut >= 0 {
		s = s[:cut]
	}
	if strings.HasPrefix(s, "weknora-sk-") {
		return s
	}
	return ""
}

// snapshotsNotFromOtherConfig drops provider listings that already name a
// different WeKnora config. Cube, E2B and Docker all ListSnapshots across the
// whole account/daemon, so without this a reconcile of one config would treat
// every other config's image as an extra, and an abandoned-build match could
// bind to the wrong snapshot.
func snapshotsNotFromOtherConfig(
	listed []sandbox.RemoteSnapshotRef, prefix string,
) []sandbox.RemoteSnapshotRef {
	if strings.TrimSpace(prefix) == "" {
		return listed
	}
	out := make([]sandbox.RemoteSnapshotRef, 0, len(listed))
	for _, snap := range listed {
		if snapshotBelongsToOtherConfig(snap, prefix) {
			continue
		}
		out = append(out, snap)
	}
	return out
}

func snapshotBelongsToOtherConfig(snap sandbox.RemoteSnapshotRef, prefix string) bool {
	if strings.TrimSpace(prefix) == "" {
		return false
	}
	needle := prefix + "-g"
	sawForeign := false
	for _, candidate := range append([]string{snap.ID}, snap.Names...) {
		name := weknoraSkillSnapshotName(candidate)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, needle) {
			return false
		}
		// New-format names are weknora-sk-t<tenant>-<config>-gN. Legacy
		// weknora-sk-<short>-gN names are left alone so a row written before
		// the prefix existed can still be matched.
		rest := strings.TrimPrefix(name, "weknora-sk-")
		if len(rest) > 1 && rest[0] == 't' && rest[1] >= '0' && rest[1] <= '9' {
			sawForeign = true
		}
	}
	return sawForeign
}

func buildInstallPrompt(skillDir string, bundle *SkillBundle, uvAvailable bool) string {
	skillMD := ""
	if bundle != nil {
		skillMD = string(bundle.Files["SKILL.md"])
	}
	return fmt.Sprintf(`Install this WeKnora skill into the sandbox image.

Skill directory: %s
uv available: %t

Hard requirements:
- Install dependencies for exactly this one skill.
- Python dependencies must go into %s/.venv. Do not install into system Python.
- Node dependencies must go under %s/node_modules. Do not install global packages unless no local alternative exists.
- Use shell_exec only. You may set work_dir to %s.
- Each command has a 10-minute budget; you do not need to set timeout_sec.
- When finished, report what you installed and any global/system packages you changed.

The server verifies the result itself before the image is kept, so report what
you did rather than whether it passed. Verification parses every script with
the interpreter that would run it, resolves the imports each one executes on
load, and checks every distribution named in requirements.txt is present in the
venv. It never runs the skill's code, so nothing is expected to answer --help.

SKILL.md:
%s
`, skillDir, uvAvailable, skillDir, skillDir, skillDir, skillMD)
}

func (s *TenantSkillService) probeUv(ctx context.Context, mgr sandbox.Manager, sessionID string) bool {
	_, err := s.execInstall(ctx, mgr, sessionID, "uv --version")
	return err == nil
}

// installerAgentDefaults returns the platform's own definition of the installer
// agent. It is deliberately not the record GetAgentByID serves, which a tenant
// can overwrite. When the registry has not been loaded, an ID-only agent yields
// the hardcoded platform defaults below rather than improvised settings.
func installerAgentDefaults(ctx context.Context, tenantID uint64) *types.CustomAgent {
	if defaults := types.GetBuiltinAgentWithContext(
		ctx, types.BuiltinSkillInstallerID, tenantID,
	); defaults != nil {
		return defaults
	}
	return &types.CustomAgent{ID: types.BuiltinSkillInstallerID}
}

// installerAgentConfig builds the config the install session runs under.
//
// `defaults` must be the built-in registry entry, never the tenant-editable
// record. Everything that decides what the root shell is asked to do — system
// prompt, tool whitelist, iteration budget — is fixed by the platform here.
// A tenant can edit any built-in agent's stored Config, and "can edit an
// agent" must not become "can script a root shell whose output is baked into
// the shared sandbox image": that is a different permission from "can upload a
// skill". The model is the one choice still taken from the stored record, in
// resolveInstallerModel.
func installerAgentConfig(defaults *types.CustomAgent, configID string) *types.AgentConfig {
	memoryOff := false
	cfg := &types.AgentConfig{
		MaxIterations:    30,
		AllowedTools:     []string{tools.ToolShellExec},
		Temperature:      0.2,
		WebSearchEnabled: false,
		MCPSelectionMode: "none",
		MemoryEnabled:    &memoryOff,
		SandboxConfigID:  configID,
	}
	if defaults == nil {
		return cfg
	}
	// The installer's shell_exec must run as root inside the skills image
	// root; the prompt below asks for exactly that. The grant is keyed on the
	// built-in agent ID and refused for anything else.
	cfg.EnableSkillInstallMode(defaults.ID)
	custom := defaults.Config
	cfg.MaxIterations = custom.MaxIterations
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = 30
	}
	cfg.AllowedTools = append([]string(nil), custom.AllowedTools...)
	if len(cfg.AllowedTools) == 0 {
		cfg.AllowedTools = []string{tools.ToolShellExec}
	}
	cfg.Temperature = custom.Temperature
	cfg.SystemPrompt = custom.SystemPrompt
	cfg.UseCustomSystemPrompt = custom.SystemPrompt != ""
	cfg.WebSearchEnabled = false
	cfg.WebSearchMaxResults = 0
	cfg.MCPSelectionMode = "none"
	cfg.MemoryEnabled = &memoryOff
	cfg.MultiTurnEnabled = custom.MultiTurnEnabled
	cfg.LLMCallTimeout = custom.LLMCallTimeout
	return cfg
}

// resolveInstallerModel prefers the model the installer agent is configured
// with. Whoever set that model chose it for this job; the workspace default is
// only the fallback for a record that names no model or names one this
// workspace can no longer resolve.
func (s *TenantSkillService) resolveInstallerModel(
	ctx context.Context, tenantID uint64, agent *types.CustomAgent,
) (chat.Chat, error) {
	if s.models == nil {
		return nil, errors.New("model service is not configured")
	}
	if agent != nil {
		if modelID := strings.TrimSpace(agent.Config.ModelID); modelID != "" {
			model, err := s.models.GetChatModel(ctx, modelID)
			if err == nil && model != nil {
				return model, nil
			}
			logger.Warnf(ctx,
				"[skill] installer agent model %s is unusable (%v); falling back to the workspace default",
				modelID, err)
		}
	}
	models, err := s.models.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models for installer: %w", err)
	}
	for _, model := range models {
		if model != nil && model.Type == types.ModelTypeKnowledgeQA &&
			model.Status == types.ModelStatusActive && model.IsDefault {
			return s.models.GetChatModel(ctx, model.ID)
		}
	}
	for _, model := range models {
		if model != nil && model.Type == types.ModelTypeKnowledgeQA &&
			model.Status == types.ModelStatusActive {
			return s.models.GetChatModel(ctx, model.ID)
		}
	}
	return nil, fmt.Errorf("workspace %d has no active chat model for skill installer", tenantID)
}

func (s *TenantSkillService) writeManifestEntry(
	ctx context.Context, mgr sandbox.Manager, sessionID, skillID string, bundle *SkillBundle,
) error {
	store, err := installFileStore(mgr)
	if err != nil {
		return err
	}
	manifest := skillImageManifest{}
	if reader, ok := mgr.(sandbox.SessionFileReader); ok {
		if raw, err := reader.ReadSessionFile(ctx, sessionID, sandbox.SkillsManifestPath); err == nil {
			_ = json.Unmarshal(raw, &manifest)
		}
	}
	entry := skillImageManifestEntry{
		ID:          skillID,
		Name:        bundle.Name,
		Version:     bundle.Version,
		SHA256:      bundle.SHA256,
		InstalledAt: s.now(),
	}
	replaced := false
	for i := range manifest.Skills {
		if manifest.Skills[i].ID == skillID {
			manifest.Skills[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		manifest.Skills = append(manifest.Skills, entry)
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return store.WriteSessionFile(ctx, sessionID, sandbox.SkillsManifestPath, payload)
}

type skillImageManifest struct {
	Skills []skillImageManifestEntry `json:"skills"`
}

type skillImageManifestEntry struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Version     string    `json:"version,omitempty"`
	SHA256      string    `json:"sha256"`
	InstalledAt time.Time `json:"installed_at"`
}

// sessionInstallFileStore is the narrow write surface the install flow needs:
// SessionBoundManager.WriteSessionFile only permits paths under the skills
// image root.
type sessionInstallFileStore interface {
	WriteSessionFile(ctx context.Context, sessionID, filePath string, content []byte) error
}

// installFileStore is the single place the image-write capability is checked.
// Local and disabled managers do not implement it, so an install that resolved
// to one fails here rather than half-writing an image.
func installFileStore(mgr sandbox.Manager) (sessionInstallFileStore, error) {
	store, ok := mgr.(sessionInstallFileStore)
	if !ok || store == nil {
		return nil, errors.New("sandbox backend cannot write files into the skills image")
	}
	return store, nil
}

// installerAgentSource is the one thing the install flow needs from the agent
// side: the stored installer record, read for its model choice.
// interfaces.AgentService does not carry this method — it belongs to
// interfaces.CustomAgentService — so the dependency is injected separately and
// narrowed to this method here rather than fished out of the agent service at
// runtime, which could only ever fail.
type installerAgentSource interface {
	GetAgentByID(ctx context.Context, id string) (*types.CustomAgent, error)
}

// The concrete type the container wires must keep satisfying the narrow
// contract, so a future move of GetAgentByID off the custom agent service
// breaks the build instead of the install flow.
var _ installerAgentSource = (*customAgentService)(nil)

func currentBaseTemplate(cfg *types.TenantSandboxConfig) string {
	if cfg == nil {
		return ""
	}
	switch sandbox.SandboxType(cfg.SandboxType) {
	case sandbox.SandboxTypeCube:
		if cfg.Cube != nil {
			return cfg.Cube.TemplateID
		}
	case sandbox.SandboxTypeE2B:
		if cfg.E2B != nil {
			return cfg.E2B.TemplateID
		}
	case sandbox.SandboxTypeDocker:
		if cfg.Docker != nil {
			return cfg.Docker.Image
		}
	}
	return ""
}

// isSkillNameConflict recognises the unique (config, name) violation two
// concurrent first-time uploads of the same skill produce.
func isSkillNameConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique constraint")
}

func skillOwnerFingerprint(cfg *types.TenantSandboxConfig) string {
	return sandbox.SkillOwnerFingerprint(cfg)
}

// configSandboxInvalidator is the narrow capability marking bound sandboxes
// needs. It is reached by type assertion rather than declared on Manager
// because only the session-bound remote manager owns bindings to mark: a
// disabled backend has none, and for that doing nothing is correct.
type configSandboxInvalidator interface {
	InvalidateConfigSandboxes(ctx context.Context, tenantID uint64, configID string) (int, error)
}

// The manager the resolver hands out must keep satisfying this, so a rename on
// the sandbox side breaks the build instead of silently skipping every mark.
var _ configSandboxInvalidator = (*sandbox.SessionBoundManager)(nil)

// markConfigSandboxesStale tells every session already holding a sandbox of
// this config that the image underneath it has been replaced.
//
// The pointer switch alone only reaches FUTURE sessions: one that is open right
// now is bound to a sandbox booted from the previous image, and without this it
// would keep serving that image until the session ends - so a skill installed
// minutes ago would be missing from exactly the sessions whose user just asked
// for it. Nothing is destroyed here; the binding is marked and the session's
// next resolve rebuilds it.
//
// It is best-effort on purpose. It runs after the pointer switch, so the
// install has already succeeded, and a binding that could not be marked is a
// session serving a stale image - an annoyance, not a corruption. Turning that
// into a failed install would be a lie about an image that is live and serving.
//
// The context is detached because the caller's is cancelled exactly when this
// matters most: withConfigLock cancels the run's context the moment lock
// renewal fails, and marking is ordinary Redis traffic that a dead context
// fails outright.
func (s *TenantSkillService) markConfigSandboxesStale(
	ctx context.Context, tenantID uint64, configID string,
) {
	if s.sandboxes == nil {
		return
	}
	markCtx, cancel := s.cleanupContext(context.WithoutCancel(ctx))
	defer cancel()

	if s.configs != nil {
		entity, err := s.configs.GetByID(markCtx, tenantID, configID)
		if err != nil {
			logger.Warnf(markCtx,
				"[skill] read config %s skill_rollout before marking sandboxes stale failed: %v",
				configID, err)
		} else if entity != nil && !entity.Config.RebuildsExistingOnSkillChange() {
			logger.Infof(markCtx,
				"[skill] config %s skill_rollout=%s; leaving live sandboxes on the previous image",
				configID, types.SkillRolloutNewSession)
			return
		}
	}

	mgr, err := s.sandboxes.Resolve(markCtx, tenantID, configID)
	if err != nil || mgr == nil {
		logger.Warnf(markCtx,
			"[skill] resolve sandbox config %s to mark its sandboxes stale failed: %v",
			configID, err)
		return
	}
	invalidator, ok := mgr.(configSandboxInvalidator)
	if !ok {
		return
	}
	marked, err := invalidator.InvalidateConfigSandboxes(markCtx, tenantID, configID)
	if err != nil {
		logger.Warnf(markCtx,
			"[skill] mark live sandboxes of config %s stale failed: %v", configID, err)
		return
	}
	if marked == 0 {
		return
	}
	logger.Infof(markCtx,
		"[skill] marked %d live sandbox binding(s) of config %s stale "+
			"(this run's own maintenance session included); each remaining session "+
			"rebuilds from the new image on its next use",
		marked, configID)
}

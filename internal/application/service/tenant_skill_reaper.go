package service

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/robfig/cron/v3"
)

const (
	skillReaperCronSpec            = "0 */5 * * * *"
	skillInstallInterruptedMessage = "安装进程中断: the process died before the install finished"
)

// skillReaperStore is the skill-row slice ReapStuckRuns needs.
type skillReaperStore interface {
	ListStaleInstalling(ctx context.Context, olderThan time.Time) ([]*types.TenantSkillEntity, error)
	GetSkill(ctx context.Context, tenantID uint64, configID, skillID string) (*types.TenantSkillEntity, error)
	UpdateSkill(ctx context.Context, e *types.TenantSkillEntity) error
	DeleteSkill(ctx context.Context, tenantID uint64, configID, skillID string) error
}

// skillReaperConfigReader is the config read ReapStuckRuns needs to tell a
// serving skill (pointer already switched) from a genuinely abandoned install.
type skillReaperConfigReader interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.TenantSandboxConfigEntity, error)
}

// skillSnapshotLedger is the per-config chain: what ReconcileSnapshots compares
// provider listings against, and what ReapStuckRuns walks to decide whether a
// stuck run's skill is still in the live image.
type skillSnapshotLedger interface {
	ListSnapshotsByConfig(
		ctx context.Context, tenantID uint64, configID string,
	) ([]*types.TenantSkillSnapshotEntity, error)
}

// skillSnapshotLister is the provider listing ReconcileSnapshots is allowed to
// call. It deliberately omits DeleteSnapshot: extras are warned, never removed,
// because the same provider account may be shared across environments.
type skillSnapshotLister interface {
	ListSnapshots(ctx context.Context, sandboxID string) ([]sandbox.RemoteSnapshotRef, error)
}

// skillSnapshotDeleter is the provider delete PruneSupersededSnapshots is
// allowed to call. It is a separate surface from the lister so reconcile
// cannot grow a delete by accident: extras not in the ledger stay untouched.
type skillSnapshotDeleter interface {
	DeleteSnapshot(ctx context.Context, snapshotID string) error
}

// sandboxConfigEnumerator walks every sandbox config for the orphan-snapshot
// sweep. ListAll is housekeeping-only.
type sandboxConfigEnumerator interface {
	ListAll(ctx context.Context) ([]*types.TenantSandboxConfigEntity, error)
}

var (
	_ skillReaperStore        = (repository.TenantSkillRepository)(nil)
	_ skillSnapshotLedger     = (repository.TenantSkillRepository)(nil)
	_ skillReaperConfigReader = (repository.TenantSandboxConfigRepository)(nil)
	_ sandboxConfigEnumerator = (repository.TenantSandboxConfigRepository)(nil)
	_ skillSnapshotLister     = (sandbox.RemoteSnapshotManager)(nil)
	_ skillSnapshotDeleter    = (*sandbox.SessionBoundManager)(nil)
)

// ReapStuckRuns recovers skill rows whose install or remove process died.
//
// Both branches turn on one question — does the image every new session boots
// still carry this skill's files — and skillFilesInLiveImage answers it from
// the snapshot ledger rather than from the row.
//
// An installing row older than skillInstallStuckTTL is healed to ready when
// the files are there: a re-install that died before the pointer moved, or a
// terminal ready write that never landed. Leaving it at installing would hide
// a skill the image still carries. Otherwise it becomes failed so the UI stops
// spinning.
//
// A removing row is restored to ready while the files are still there, so the
// operator can retry. Once they are gone the leftover row is deleted, so the
// agent cannot be told to invoke a skill no image carries.
func (s *TenantSkillService) ReapStuckRuns(ctx context.Context) (int, error) {
	if s == nil || s.skills == nil {
		return 0, nil
	}
	now := s.now
	if now == nil {
		now = time.Now
	}
	cutoff := now().Add(-skillInstallStuckTTL)
	stale, err := s.skills.ListStaleInstalling(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	reaped := 0
	for _, row := range stale {
		if row == nil || row.InstallingSince == nil || !row.InstallingSince.Before(cutoff) {
			continue
		}
		snapshotID, serving, known := s.skillFilesInLiveImage(ctx, row)
		switch row.Status {
		case types.SkillStatusInstalling:
			// An unreadable config or ledger must not be treated as a failed
			// install: the image may still be serving this skill.
			if serving || !known {
				if err := s.updateSkillFields(ctx, row.TenantID, row.SandboxConfigID, row.ID,
					func(e *types.TenantSkillEntity) {
						e.Status = types.SkillStatusReady
						e.Error = ""
						e.InstallingSince = nil
						// The install that died past the pointer switch never
						// got to record which snapshot carries it.
						if snapshotID != "" {
							e.InstalledSnapshotID = snapshotID
						}
					}); err != nil {
					logger.Warnf(ctx, "[skill] heal abandoned install %s back to ready failed: %v",
						row.ID, err)
					continue
				}
				reaped++
				continue
			}
			if err := s.updateSkillFields(ctx, row.TenantID, row.SandboxConfigID, row.ID,
				func(e *types.TenantSkillEntity) {
					e.Status = types.SkillStatusFailed
					e.Error = skillInstallInterruptedMessage
					e.InstallingSince = nil
				}); err != nil {
				logger.Warnf(ctx, "[skill] reap abandoned install %s failed: %v", row.ID, err)
				continue
			}
			reaped++
		case types.SkillStatusRemoving:
			// Deleting the row and its bundle is the one irreversible thing
			// the reaper does, so a run it cannot judge is left for the next
			// sweep rather than guessed at.
			if !known {
				continue
			}
			if serving {
				if err := s.updateSkillFields(ctx, row.TenantID, row.SandboxConfigID, row.ID,
					func(e *types.TenantSkillEntity) {
						e.Status = types.SkillStatusReady
						e.Error = ""
						e.InstallingSince = nil
						if snapshotID != "" {
							e.InstalledSnapshotID = snapshotID
						}
					}); err != nil {
					logger.Warnf(ctx, "[skill] restore abandoned removal %s failed: %v", row.ID, err)
					continue
				}
				reaped++
				continue
			}
			if err := s.skills.DeleteSkill(ctx, row.TenantID, row.SandboxConfigID, row.ID); err != nil {
				logger.Warnf(ctx, "[skill] drop abandoned removal %s failed: %v", row.ID, err)
				continue
			}
			if row.BundleRef != "" {
				s.deleteBundleBestEffort(ctx, row.TenantID, row.BundleRef)
			}
			reaped++
		}
	}
	if reaped > 0 {
		logger.Infof(ctx, "[skill] reaped %d stuck install/remove run(s)", reaped)
	}
	return reaped, nil
}

// skillFilesInLiveImage reports whether the image every new session boots
// still carries this skill, and which snapshot of the chain put it there.
// known is false when the config or the ledger cannot be read, so a caller
// about to do something irreversible can refuse to guess.
//
// The question is not whether this row's InstalledSnapshotID equals the live
// one. A config holds a single pointer that every install and removal
// advances, and each new snapshot is grown from the current one, so a skill
// installed two generations ago is still in the image under a snapshot ID its
// row never hears about — nothing rewrites one skill's row when another skill
// is installed. Comparing the two IDs calls such a skill gone: it fails
// healthy installs and, worse, deletes the row and bundle of a stuck removal
// whose files are still in the image.
//
// The ledger records each snapshot's parent, so the honest question is whether
// this skill's install is on the chain the pointer currently names.
func (s *TenantSkillService) skillFilesInLiveImage(
	ctx context.Context, row *types.TenantSkillEntity,
) (string, bool, bool) {
	if row == nil {
		return "", false, false
	}
	live, ok := s.liveSnapshotID(ctx, row)
	if !ok {
		return "", false, false
	}
	if live = strings.TrimSpace(live); live == "" {
		// The config boots its base template, which carries no skill by
		// construction — the last removal of the config cleared the pointer.
		return "", false, true
	}
	ledger, err := s.skills.ListSnapshotsByConfig(ctx, row.TenantID, row.SandboxConfigID)
	if err != nil {
		logger.Warnf(ctx, "[skill] reaper could not read the snapshot ledger of config %s: %v",
			row.SandboxConfigID, err)
		return "", false, false
	}

	bySnapshot := make(map[string]*types.TenantSkillSnapshotEntity, len(ledger))
	everInstalled := false
	for _, entry := range ledger {
		if entry == nil {
			continue
		}
		// A building row has no snapshot yet, and one abandoned between the
		// snapshot and the pointer switch is a child of live rather than an
		// ancestor, so anchoring the walk on live already excludes both.
		if id := strings.TrimSpace(entry.SnapshotID); id != "" {
			bySnapshot[id] = entry
		}
		if entry.SkillID == row.ID && entry.Trigger == types.SkillSnapshotTriggerInstall {
			everInstalled = true
		}
	}

	// The visited set only stops a corrupted parent pointer from looping; the
	// chain itself is finite.
	visited := make(map[string]struct{}, len(bySnapshot))
	for cursor := live; cursor != ""; {
		if _, seen := visited[cursor]; seen {
			break
		}
		visited[cursor] = struct{}{}
		entry, ok := bySnapshot[cursor]
		if !ok {
			// The chain runs out before it answers. A skill that never
			// produced a snapshot is absent either way; for one that did,
			// refuse to guess rather than delete files that may still exist.
			return "", false, !everInstalled
		}
		if entry.SkillID == row.ID {
			// The nearest generation naming this skill decides it: an install
			// put the files in, a removal took them out, and a rebuild says
			// nothing about one skill so the walk continues past it.
			switch entry.Trigger {
			case types.SkillSnapshotTriggerInstall:
				return entry.SnapshotID, true, true
			case types.SkillSnapshotTriggerRemove:
				return "", false, true
			}
		}
		cursor = strings.TrimSpace(entry.ParentSnapshotID)
	}
	return "", false, true
}

// liveSnapshotID returns the config's current SkillImage snapshot and whether
// that answer is trustworthy. ok is false when the config cannot be read, so
// the caller can refuse to guess.
func (s *TenantSkillService) liveSnapshotID(
	ctx context.Context, row *types.TenantSkillEntity,
) (string, bool) {
	if row == nil || s.configs == nil {
		return "", false
	}
	cfg, err := s.configs.GetByID(ctx, row.TenantID, row.SandboxConfigID)
	if err != nil {
		logger.Warnf(ctx, "[skill] reaper could not read sandbox config %s: %v",
			row.SandboxConfigID, err)
		return "", false
	}
	return currentSnapshotID(cfg), true
}

// ReconcileSnapshots compares provider ListSnapshots against the ledger for one
// config. Snapshots that exist on the provider but not in the ledger are
// logged as warnings and never deleted: the same provider account may be
// shared across environments, and an extra here is often another environment's
// live image.
func (s *TenantSkillService) ReconcileSnapshots(
	ctx context.Context, tenantID uint64, configID string,
) (int, error) {
	if s == nil || s.skills == nil {
		return 0, nil
	}
	rows, err := s.skills.ListSnapshotsByConfig(ctx, tenantID, configID)
	if err != nil {
		return 0, err
	}
	known := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		id := strings.TrimSpace(row.SnapshotID)
		if id == "" {
			continue
		}
		known[id] = struct{}{}
	}
	lister := snapshotListerFrom(ctx, s.sandboxes, tenantID, configID)
	if lister == nil {
		return 0, nil
	}
	listed, err := lister.ListSnapshots(ctx, "")
	if err != nil {
		return 0, err
	}
	extras := 0
	for _, snap := range listed {
		id := strings.TrimSpace(snap.ID)
		if id == "" {
			continue
		}
		if _, ok := known[id]; ok {
			continue
		}
		extras++
		logger.Warnf(ctx,
			"[skill] snapshot %s is not in the ledger of sandbox config %s "+
				"(not deleted; the provider account may be shared across environments)",
			id, configID)
	}
	return extras, nil
}

func snapshotListerFrom(
	ctx context.Context, resolver sandbox.TenantSandboxResolver, tenantID uint64, configID string,
) skillSnapshotLister {
	if resolver == nil {
		return nil
	}
	mgr, err := resolver.Resolve(ctx, tenantID, configID)
	if err != nil {
		logger.Warnf(ctx, "[skill] resolve sandbox for snapshot reconcile of %s failed: %v", configID, err)
		return nil
	}
	if mgr == nil {
		return nil
	}
	lister, ok := mgr.(skillSnapshotLister)
	if !ok {
		return nil
	}
	return lister
}

func snapshotDeleterFrom(
	ctx context.Context, resolver sandbox.TenantSandboxResolver, tenantID uint64, configID string,
) skillSnapshotDeleter {
	if resolver == nil {
		return nil
	}
	mgr, err := resolver.Resolve(ctx, tenantID, configID)
	if err != nil {
		logger.Warnf(ctx, "[skill] resolve sandbox for snapshot prune of %s failed: %v", configID, err)
		return nil
	}
	if mgr == nil {
		return nil
	}
	deleter, ok := mgr.(skillSnapshotDeleter)
	if !ok {
		return nil
	}
	return deleter
}

func (s *TenantSkillService) snapshotRetentionWindow() time.Duration {
	if s != nil && s.snapshotRetention > 0 {
		return s.snapshotRetention
	}
	return skillSnapshotRetention
}

// PruneSupersededSnapshots deletes provider snapshots the ledger has already
// retired, once they are older than snapshotRetention. The current image is
// never touched, nor is anything the ledger does not name: extras belong to
// other environments on a shared provider account.
//
// A live sandbox does not need the template it was created from in order to
// keep running, so the only reason to wait is in-flight creates that resolved
// the previous pointer. Twenty-four hours is far past every backend TTL.
func (s *TenantSkillService) PruneSupersededSnapshots(ctx context.Context) (int, error) {
	if s == nil || s.skills == nil {
		return 0, nil
	}
	enum, ok := s.configs.(sandboxConfigEnumerator)
	if !ok {
		return 0, nil
	}
	configs, err := enum.ListAll(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := s.now().Add(-s.snapshotRetentionWindow())
	pruned := 0
	for _, cfg := range configs {
		if cfg == nil || types.IsSandboxWorkspacePolicyRow(cfg) {
			continue
		}
		n, err := s.pruneConfigSnapshots(ctx, cfg, cutoff)
		if err != nil {
			logger.Warnf(ctx, "[skill] prune superseded snapshots of config %s failed: %v",
				cfg.ID, err)
			continue
		}
		pruned += n
	}
	return pruned, nil
}

func (s *TenantSkillService) pruneConfigSnapshots(
	ctx context.Context, cfg *types.TenantSandboxConfigEntity, cutoff time.Time,
) (int, error) {
	if cfg == nil {
		return 0, nil
	}
	rows, err := s.skills.ListSnapshotsByConfig(ctx, cfg.TenantID, cfg.ID)
	if err != nil {
		return 0, err
	}
	live := currentSnapshotID(cfg)
	deleter := snapshotDeleterFrom(ctx, s.sandboxes, cfg.TenantID, cfg.ID)
	pruned := 0
	for _, row := range rows {
		if !snapshotEligibleForPrune(row, live, cutoff) {
			continue
		}
		if deleter == nil {
			logger.Warnf(ctx,
				"[skill] cannot prune snapshot %s of config %s: provider does not support delete",
				row.SnapshotID, cfg.ID)
			continue
		}
		if err := deleter.DeleteSnapshot(ctx, row.SnapshotID); err != nil && !sandbox.IsRemoteNotFound(err) {
			logger.Warnf(ctx, "[skill] delete superseded snapshot %s failed: %v", row.SnapshotID, err)
			continue
		}
		if err := s.skills.MarkSnapshotState(
			ctx, cfg.TenantID, row.ID, types.SkillSnapshotStateDeleted, row.SnapshotID,
		); err != nil {
			logger.Warnf(ctx, "[skill] mark snapshot %s deleted after prune failed: %v", row.ID, err)
			continue
		}
		pruned++
	}
	return pruned, nil
}

// snapshotEligibleForPrune is the ledger-side gate. The provider delete is
// what costs money; this is what keeps it from touching the live image or a
// snapshot another environment created on the same account.
func snapshotEligibleForPrune(
	row *types.TenantSkillSnapshotEntity, liveSnapshotID string, cutoff time.Time,
) bool {
	if row == nil || row.State != types.SkillSnapshotStateSuperseded {
		return false
	}
	id := strings.TrimSpace(row.SnapshotID)
	if id == "" || id == strings.TrimSpace(liveSnapshotID) {
		return false
	}
	if row.SupersededAt == nil || row.SupersededAt.After(cutoff) || row.SupersededAt.Equal(cutoff) {
		return false
	}
	return true
}

func (s *TenantSkillService) reconcileAllSnapshots(ctx context.Context) {
	enum, ok := s.configs.(sandboxConfigEnumerator)
	if !ok {
		return
	}
	configs, err := enum.ListAll(ctx)
	if err != nil {
		logger.Warnf(ctx, "[skill] list sandbox configs for snapshot reconcile failed: %v", err)
		return
	}
	for _, cfg := range configs {
		if cfg == nil || types.IsSandboxWorkspacePolicyRow(cfg) {
			continue
		}
		if _, err := s.ReconcileSnapshots(ctx, cfg.TenantID, cfg.ID); err != nil {
			logger.Warnf(ctx, "[skill] reconcile snapshots for config %s failed: %v", cfg.ID, err)
		}
	}
}

func (s *TenantSkillService) runSkillReaper(ctx context.Context) {
	if _, err := s.ReapStuckRuns(ctx); err != nil {
		logger.Warnf(ctx, "[skill] reap stuck runs failed: %v", err)
	}
	if _, err := s.PruneSupersededSnapshots(ctx); err != nil {
		logger.Warnf(ctx, "[skill] prune superseded snapshots failed: %v", err)
	}
	s.reconcileAllSnapshots(ctx)
}

// Start registers the five-minute stuck-run sweep and begins the background
// runner. Idempotent — repeated calls are a no-op so wiring code can call
// Start without coordinating ordering.
func (s *TenantSkillService) Start(ctx context.Context) error {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if s.started {
		return nil
	}
	if s.cron == nil {
		s.cron = cron.New(cron.WithSeconds(), cron.WithChain(
			cron.Recover(cron.DefaultLogger),
		))
	}
	if _, err := s.cron.AddFunc(skillReaperCronSpec, func() {
		s.runSkillReaper(context.Background())
	}); err != nil {
		return err
	}
	s.cron.Start()
	s.started = true
	logger.Infof(ctx, "[skill] reaper started with 5-minute sweep")
	return nil
}

// Stop halts the cron and waits for in-flight sweeps to finish.
func (s *TenantSkillService) Stop() {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if !s.started {
		return
	}
	c := s.cron.Stop()
	<-c.Done()
	s.started = false
}

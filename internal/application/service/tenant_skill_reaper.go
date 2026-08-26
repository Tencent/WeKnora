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
// An installing row whose heartbeat has been silent for skillInstallStuckTTL
// is healed to ready when the files are there: a re-install that died before
// the pointer moved, or a terminal ready write that never landed. Leaving it
// at installing would hide a skill the image still carries. Otherwise it
// becomes failed so the UI stops spinning. A live install keeps stamping
// InstallingSince, so a run that is merely slow is never swept.
//
// A removing row is restored to ready while the files are still there, so the
// operator can retry. Once they are gone the leftover row is deleted, so the
// agent cannot be told to invoke a skill no image carries.
func (s *TenantSkillService) ReapStuckRuns(ctx context.Context) (int, error) {
	if s == nil || s.skills == nil {
		return 0, nil
	}
	cutoff := s.clock()().Add(-skillInstallStuckTTL)
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
	listed = snapshotsNotFromOtherConfig(listed, skillSnapshotNamePrefix(tenantID, configID))
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

// snapshotRetentionFor is how long this config's retired snapshots stay on
// the provider. The floor is snapshotRetentionWindow; a config that asked
// for a sandbox TTL longer than that keeps the previous template at least
// that long plus a margin, so a session created from it can still exist.
func (s *TenantSkillService) snapshotRetentionFor(cfg *types.TenantSandboxConfigEntity) time.Duration {
	window := s.snapshotRetentionWindow()
	ttl := time.Duration(0)
	if cfg != nil && cfg.Config != nil {
		ttl = configuredSandboxTTL(cfg.Config)
	}
	if needed := ttl + skillSnapshotTTLMargin; needed > window {
		return needed
	}
	return window
}

func configuredSandboxTTL(cfg *types.TenantSandboxConfig) time.Duration {
	if cfg == nil {
		return 0
	}
	seconds := 0
	if cfg.Cube != nil && cfg.Cube.CubeSandboxTTLSeconds > seconds {
		seconds = cfg.Cube.CubeSandboxTTLSeconds
	}
	if cfg.E2B != nil && cfg.E2B.E2BSandboxTTLSeconds > seconds {
		seconds = cfg.E2B.E2BSandboxTTLSeconds
	}
	// Docker's equivalent is the idle TTL: the daemon has none of its own, so
	// that is how long a container created from the previous image may still
	// be sitting there unused.
	if cfg.Docker != nil && cfg.Docker.IdleTTLSeconds > seconds {
		seconds = cfg.Docker.IdleTTLSeconds
	}
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// PruneSupersededSnapshots deletes provider snapshots the ledger has already
// retired, once they are older than this config's retention. The current
// image is never touched, nor is anything the ledger does not name: extras
// belong to other environments on a shared provider account.
//
// A live sandbox does not need the template it was created from in order to
// keep running, so the only reason to wait is in-flight creates that resolved
// the previous pointer. Twenty-four hours is far past every backend's default
// TTL; a config that sets a longer one extends the wait.
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
	now := s.clock()
	pruned := 0
	for _, cfg := range configs {
		if cfg == nil || types.IsSandboxWorkspacePolicyRow(cfg) {
			continue
		}
		cutoff := now().Add(-s.snapshotRetentionFor(cfg))
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
	// A rotated credential points at a different provider account, where the
	// ledger's snapshot IDs do not exist. The delete would come back
	// not-found, which this sweep reads as "already gone", so the account that
	// really holds those snapshots would keep being billed for them while the
	// ledger recorded them as deleted. ensureUsableImage stops installs for
	// the same reason; this stops the irreversible half.
	if err := ensureUsableImage(cfg); err != nil {
		logger.Warnf(ctx, "[skill] skip snapshot prune of config %s: %v", cfg.ID, err)
		return 0, nil
	}
	rows, err := s.skills.ListSnapshotsByConfig(ctx, cfg.TenantID, cfg.ID)
	if err != nil {
		return 0, err
	}
	live := currentSnapshotID(cfg)
	// Resolving builds a provider client, so it waits until a row is actually
	// eligible: most configs have nothing to prune on most sweeps.
	var deleter skillSnapshotDeleter
	resolved := false
	pruned := 0
	for _, row := range rows {
		if !snapshotEligibleForPrune(row, live, cutoff) {
			continue
		}
		if !resolved {
			deleter = snapshotDeleterFrom(ctx, s.sandboxes, cfg.TenantID, cfg.ID)
			resolved = true
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
	return pruned + s.reapAbandonedBuilds(ctx, cfg, rows), nil
}

// reapAbandonedBuilds deletes the provider snapshot of a build that never
// reached the ledger. It is the only path that can.
//
// A row is written as building, the provider commit runs, and only then is the
// snapshot's ID recorded. A process that dies in that window leaves a real,
// billed snapshot whose ID exists nowhere: PruneSupersededSnapshots skips the
// row because building is not a prunable state and its SnapshotID is empty,
// ReconcileSnapshots reports it as an extra but deliberately never deletes, and
// the config-delete path reads an empty SnapshotID as nothing to release.
// PlannedName is what closes that, because it is written before the commit.
//
// Only a positive match is acted on. A listing that names nothing we recognise
// cannot tell "the commit never happened" from "this provider does not echo
// names back", and marking the row deleted on that guess would throw away the
// last record of a snapshot that is still there.
func (s *TenantSkillService) reapAbandonedBuilds(
	ctx context.Context, cfg *types.TenantSandboxConfigEntity,
	rows []*types.TenantSkillSnapshotEntity,
) int {
	cutoff := s.clock()().Add(-skillInstallStuckTTL)
	pending := make([]*types.TenantSkillSnapshotEntity, 0, len(rows))
	for _, row := range rows {
		if abandonedBuild(row, cutoff) && !s.buildStillRunning(ctx, row) {
			pending = append(pending, row)
		}
	}
	if len(pending) == 0 {
		return 0
	}
	if s.configHasInFlightSkill(ctx, cfg.TenantID, cfg.ID) {
		return 0
	}

	lister := snapshotListerFrom(ctx, s.sandboxes, cfg.TenantID, cfg.ID)
	deleter := snapshotDeleterFrom(ctx, s.sandboxes, cfg.TenantID, cfg.ID)
	if lister == nil || deleter == nil {
		return 0
	}
	listed, err := lister.ListSnapshots(ctx, "")
	if err != nil {
		logger.Warnf(ctx, "[skill] list snapshots to reap abandoned builds of config %s failed: %v",
			cfg.ID, err)
		return 0
	}
	listed = snapshotsNotFromOtherConfig(listed, skillSnapshotNamePrefix(cfg.TenantID, cfg.ID))

	live := strings.TrimSpace(currentSnapshotID(cfg))
	reaped := 0
	for _, row := range pending {
		found := matchSnapshotByName(listed, row.PlannedName)
		// Unreachable by construction — the pointer moves after the ID is
		// recorded, so a building row cannot be live — but the delete is
		// irreversible, so the guard stays.
		if found == "" || found == live {
			continue
		}
		if err := deleter.DeleteSnapshot(ctx, found); err != nil && !sandbox.IsRemoteNotFound(err) {
			logger.Warnf(ctx, "[skill] delete abandoned build %s of config %s failed: %v",
				found, cfg.ID, err)
			continue
		}
		if err := s.skills.MarkSnapshotState(
			ctx, cfg.TenantID, row.ID, types.SkillSnapshotStateDeleted, found,
		); err != nil {
			logger.Warnf(ctx, "[skill] mark abandoned build %s deleted failed: %v", row.ID, err)
			continue
		}
		logger.Infof(ctx, "[skill] reclaimed abandoned build %s of config %s", found, cfg.ID)
		reaped++
	}
	return reaped
}

// abandonedBuild reports a building row old enough that the commit it was
// waiting on cannot still be running. The row is written immediately before the
// provider call and marked active immediately after, so the window is one
// commit wide; skillInstallStuckTTL is the same silence budget ReapStuckRuns
// gives a whole install.
func abandonedBuild(row *types.TenantSkillSnapshotEntity, cutoff time.Time) bool {
	if row == nil || row.State != types.SkillSnapshotStateBuilding {
		return false
	}
	if strings.TrimSpace(row.PlannedName) == "" {
		// Written before PlannedName existed. Nothing can name its snapshot.
		return false
	}
	return row.CreatedAt.Before(cutoff)
}

func (s *TenantSkillService) configHasInFlightSkill(
	ctx context.Context, tenantID uint64, configID string,
) bool {
	if s == nil || s.skills == nil || strings.TrimSpace(configID) == "" {
		return false
	}
	rows, err := s.skills.ListSkillsByConfig(ctx, tenantID, configID)
	if err != nil {
		logger.Warnf(ctx, "[skill] cannot read skills of config %s while reaping abandoned builds: %v",
			configID, err)
		return true
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		switch row.Status {
		case types.SkillStatusInstalling, types.SkillStatusRemoving:
			return true
		}
	}
	return false
}

// buildStillRunning reports whether the run that opened this row is alive.
//
// The age check alone would be enough for any commit that finishes in the
// minutes it normally takes, but a huge image on a slow remote daemon could
// exceed it — and deleting the snapshot of a commit that then succeeds would
// switch the config's pointer to an image that no longer exists. The install
// keeps stamping InstallingSince, so that is what says "still working".
func (s *TenantSkillService) buildStillRunning(
	ctx context.Context, row *types.TenantSkillSnapshotEntity,
) bool {
	skill, err := s.skills.GetSkill(ctx, row.TenantID, row.SandboxConfigID, row.SkillID)
	if err != nil {
		// Cannot tell; refuse to guess before an irreversible delete.
		return true
	}
	if skill == nil || skill.InstallingSince == nil {
		return false
	}
	switch skill.Status {
	case types.SkillStatusInstalling, types.SkillStatusRemoving:
		return skill.InstallingSince.After(s.clock()().Add(-skillInstallStuckTTL))
	}
	return false
}

// matchSnapshotByName finds the provider snapshot a planned name refers to.
//
// Cube and E2B mint their own ID and echo the requested name in Names. Docker's
// ID *is* the name, prefixed with the local repository it commits into, which
// is why a trailing path segment counts as a match.
func matchSnapshotByName(listed []sandbox.RemoteSnapshotRef, plannedName string) string {
	want := strings.TrimSpace(plannedName)
	if want == "" {
		return ""
	}
	for _, ref := range listed {
		candidates := append([]string{ref.ID}, ref.Names...)
		for _, candidate := range candidates {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if candidate == want || strings.HasSuffix(candidate, "/"+want) {
				if id := strings.TrimSpace(ref.ID); id != "" {
					return id
				}
				return candidate
			}
		}
	}
	return ""
}

// snapshotEligibleForPrune is the ledger-side gate. The provider delete is
// what costs money; this is what keeps it from touching the live image or a
// snapshot another environment created on the same account.
//
// Superseded rows are the normal case. Active rows that are not the live
// pointer are the crash window between switchImagePointer and
// markPreviousSnapshotsSuperseded: they are billed leftovers too, aged from
// UpdatedAt / CreatedAt because they never got a SupersededAt.
func snapshotEligibleForPrune(
	row *types.TenantSkillSnapshotEntity, liveSnapshotID string, cutoff time.Time,
) bool {
	if row == nil {
		return false
	}
	id := strings.TrimSpace(row.SnapshotID)
	if id == "" || id == strings.TrimSpace(liveSnapshotID) {
		return false
	}
	switch row.State {
	case types.SkillSnapshotStateSuperseded, types.SkillSnapshotStateActive:
	default:
		return false
	}
	aged := snapshotPruneAge(row)
	return aged != nil && aged.Before(cutoff)
}

func snapshotPruneAge(row *types.TenantSkillSnapshotEntity) *time.Time {
	if row == nil {
		return nil
	}
	if row.State == types.SkillSnapshotStateSuperseded && row.SupersededAt != nil {
		return row.SupersededAt
	}
	if !row.UpdatedAt.IsZero() {
		return &row.UpdatedAt
	}
	if !row.CreatedAt.IsZero() {
		return &row.CreatedAt
	}
	return nil
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

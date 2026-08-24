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

// skillSnapshotLedger is the per-config chain ReconcileSnapshots compares
// provider listings against.
type skillSnapshotLedger interface {
	ListSnapshotsByConfig(ctx context.Context, tenantID uint64, configID string) ([]*types.TenantSkillSnapshotEntity, error)
}

// skillSnapshotLister is the provider listing ReconcileSnapshots is allowed to
// call. It deliberately omits DeleteSnapshot: extras are warned, never removed,
// because the same provider account may be shared across environments.
type skillSnapshotLister interface {
	ListSnapshots(ctx context.Context, sandboxID string) ([]sandbox.RemoteSnapshotRef, error)
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
)

// ReapStuckRuns recovers skill rows whose install or remove process died.
//
// An installing row older than skillInstallStuckTTL is healed to ready when
// its InstalledSnapshotID is still the live image — a re-install that died
// before the pointer moved, or a terminal ready write that never landed.
// Leaving it at installing would hide a skill the image still carries.
// Otherwise it becomes failed so the UI stops spinning.
//
// A removing row is restored to ready only while that same snapshot is still
// live. If the pointer already moved (or the skill never reached an image),
// the files are gone and the leftover row is deleted so the agent cannot be
// told to invoke them.
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
		switch row.Status {
		case types.SkillStatusInstalling:
			if s.skillServesLiveSnapshot(ctx, row) {
				if err := s.updateSkillFields(ctx, row.TenantID, row.SandboxConfigID, row.ID,
					func(e *types.TenantSkillEntity) {
						e.Status = types.SkillStatusReady
						e.Error = ""
						e.InstallingSince = nil
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
			live, known := s.liveSnapshotID(ctx, row)
			if !known {
				continue
			}
			installed := strings.TrimSpace(row.InstalledSnapshotID)
			if installed != "" && installed == live {
				if err := s.updateSkillFields(ctx, row.TenantID, row.SandboxConfigID, row.ID,
					func(e *types.TenantSkillEntity) {
						e.Status = types.SkillStatusReady
						e.Error = ""
						e.InstallingSince = nil
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

// skillServesLiveSnapshot reports whether this row's last installed snapshot
// is still the image every new session boots. A true result means the files
// are still there: either the install never moved the pointer, or it did and
// only the terminal ready write is missing.
func (s *TenantSkillService) skillServesLiveSnapshot(
	ctx context.Context, row *types.TenantSkillEntity,
) bool {
	if row == nil || strings.TrimSpace(row.InstalledSnapshotID) == "" {
		return false
	}
	live, ok := s.liveSnapshotID(ctx, row)
	if !ok {
		// A config we cannot read must not be treated as a failed install:
		// the image may still be serving this skill.
		return true
	}
	return row.InstalledSnapshotID == live
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
			"[skill] snapshot %s is not in the ledger of sandbox config %s (not deleted; the provider account may be shared across environments)",
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

package sandboxalloc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// UserMachineGCLockKey makes one replica scan at a time.
	UserMachineGCLockKey = "weknora:sandbox:user-gc:lock"
	// UserMachineGCInterval is how often the container runs the scan.
	UserMachineGCInterval = time.Hour
	// UserMachineGCCursorKey remembers where a scan stopped, so the next one
	// continues instead of rereading the same oldest candidates.
	UserMachineGCCursorKey = "weknora:sandbox:user-gc:cursor"
	gcLockLease            = 5 * time.Minute
	gcBatch                = 200
	gcMaxPerRun            = 2000
)

// GCReport is one scan's outcome, logged as metrics.
type GCReport struct {
	Scanned, Reclaimed, Busy, Failed int
	CleanupsDone, CleanupsFailed     int
	Skipped                          bool
}

// UserMachineGC reclaims user machines that have had no live session for
// their config's retention window, and retries queued directory deletes.
type UserMachineGC struct {
	svc     *Service
	cleaner *WorkspaceCleaner
	configs ConfigReader
	redis   redis.UniversalClient
	now     func() time.Time
	batch   int // page size; tests shrink it
	maxRun  int
	cursor  interfaces.EmptyMachineCursor // used when there is no Redis
}

// NewUserMachineGC wires the GC. A nil redis client runs unlocked, which is
// only correct for a single process (user mode is never enabled without Redis).
func NewUserMachineGC(
	svc *Service, cleaner *WorkspaceCleaner, configs ConfigReader, client redis.UniversalClient,
) *UserMachineGC {
	return &UserMachineGC{
		svc: svc, cleaner: cleaner, configs: configs, redis: client,
		now: func() time.Time { return time.Now().UTC() }, batch: gcBatch, maxRun: gcMaxPerRun,
	}
}

// RunOnce performs one scan unless another replica holds the lock. The lock
// is renewed for the scan's duration; a lost lock ends the scan.
func (g *UserMachineGC) RunOnce(ctx context.Context) (GCReport, error) {
	if g.redis == nil {
		return g.scan(ctx)
	}
	token, err := redislock.NewToken()
	if err != nil {
		return GCReport{}, err
	}
	ok, err := redislock.TryAcquire(ctx, g.redis, UserMachineGCLockKey, token, gcLockLease)
	if err != nil {
		return GCReport{}, err
	}
	if !ok {
		return GCReport{Skipped: true}, nil
	}
	scanCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(gcLockLease / 3)
		defer t.Stop()
		for {
			select {
			case <-scanCtx.Done():
				return
			case <-t.C:
				renewed, err := redislock.Renew(scanCtx, g.redis, UserMachineGCLockKey, token, gcLockLease)
				if err != nil || !renewed {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		relCtx, relCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer relCancel()
		_, _ = redislock.Release(relCtx, g.redis, UserMachineGCLockKey, token)
	}()
	return g.scan(scanCtx)
}

func (g *UserMachineGC) scan(ctx context.Context) (GCReport, error) {
	var report GCReport
	report.CleanupsDone, report.CleanupsFailed, _ = g.cleaner.RetryDue(ctx, gcBatch)

	// Candidates only: the shortest window is one day, and every row is
	// re-evaluated inside its machine's lock. Pages advance past every
	// candidate, due or not, so none can hide the ones behind it.
	cursor := g.loadCursor(ctx)
	emptyBefore := g.now().Add(-24 * time.Hour)
	for report.Scanned < g.maxRun {
		rows, err := g.svc.repo.ListEmptyUserMachines(ctx, emptyBefore, cursor, g.batch)
		if err != nil {
			return report, err
		}
		if len(rows) == 0 {
			cursor = interfaces.EmptyMachineCursor{} // reached the end: next run starts over
			break
		}
		for _, row := range rows {
			if ctx.Err() != nil {
				g.saveCursor(ctx, cursor)
				return report, ctx.Err()
			}
			if report.Scanned >= g.maxRun {
				break
			}
			cursor = interfaces.EmptyMachineCursor{EmptySince: *row.EmptySince, Digest: row.MachineDigest}
			g.reclaimCandidate(ctx, row, &report)
		}
	}
	g.saveCursor(ctx, cursor)
	logger.Infof(ctx, "[sandbox-gc] scanned=%d reclaimed=%d busy=%d failed=%d cleanups_done=%d cleanups_failed=%d",
		report.Scanned, report.Reclaimed, report.Busy, report.Failed, report.CleanupsDone, report.CleanupsFailed)
	return report, nil
}

func (g *UserMachineGC) reclaimCandidate(ctx context.Context, row *types.SandboxUserMachine, report *GCReport) {
	report.Scanned++
	m := machineOf(row)
	digest := row.MachineDigest
	ops, err := g.cleaner.reclaimOps(ctx, m)
	if err != nil {
		report.Failed++
		logger.Warnf(ctx, "[sandbox-gc] resolve config %d/%s for machine %s: %v",
			row.ConfigTenantID, row.ConfigID, digestPrefix(digest), err)
		return
	}
	// Retention comes from the config table. Read it before the machine row
	// lock: SQLite serves the whole process on one connection, so a config
	// query while that transaction is open never returns. empty_since is
	// still the value read under the lock, below.
	days, err := g.retentionDays(ctx, row.ConfigTenantID, row.ConfigID)
	if err != nil {
		report.Failed++
		logger.Warnf(ctx, "[sandbox-gc] retention for machine %s: %v", digestPrefix(digest), err)
		return
	}
	var lock interfaces.MachineReclaimLock
	ok, err := ops.ReclaimUserMachine(ctx, m, func(_ context.Context) (bool, error) {
		var lockErr error
		// The manager cancels its provider context before returning. The row
		// lock must instead live until this scan commits or aborts the delete.
		lock, lockErr = g.svc.repo.LockEmptyMachine(ctx, digest)
		if lockErr != nil || lock == nil {
			return false, lockErr
		}
		due := !g.now().Before(lock.Claim().EmptySince.Add(time.Duration(days) * 24 * time.Hour))
		if !due {
			lock.Abort()
			lock = nil
			return false, nil
		}
		return true, nil
	})
	if lock != nil {
		defer lock.Abort()
	}
	switch {
	case err != nil:
		report.Failed++
		logger.Warnf(ctx, "[sandbox-gc] reclaim machine %s: %v", digestPrefix(digest), err)
	case !ok:
		report.Busy++
	case lock == nil:
		// The manager reported the machine reclaimable without ever running
		// the check that takes its row lock; nothing was provably deleted,
		// so the registry row stays for the next scan.
		report.Failed++
		logger.Warnf(ctx, "[sandbox-gc] reclaim machine %s: manager skipped the reclaim check",
			digestPrefix(digest))
	default:
		deleted, delErr := lock.CommitDelete()
		if delErr != nil {
			report.Failed++
			logger.Warnf(ctx, "[sandbox-gc] drop registry row %s: %v", digestPrefix(digest), delErr)
			return
		}
		if !deleted {
			report.Busy++
			return
		}
		report.Reclaimed++
	}
}

type gcCursorJSON struct {
	EmptySince time.Time `json:"empty_since"`
	Digest     string    `json:"digest"`
}

func (g *UserMachineGC) loadCursor(ctx context.Context) interfaces.EmptyMachineCursor {
	if g.redis == nil {
		return g.cursor
	}
	raw, err := g.redis.Get(ctx, UserMachineGCCursorKey).Bytes()
	if err != nil {
		return interfaces.EmptyMachineCursor{}
	}
	var c gcCursorJSON
	if json.Unmarshal(raw, &c) != nil {
		return interfaces.EmptyMachineCursor{}
	}
	return interfaces.EmptyMachineCursor{EmptySince: c.EmptySince, Digest: c.Digest}
}

// saveCursor stores where this run stopped. A lost write only means the
// next run starts over; the scan stays correct, just slower.
func (g *UserMachineGC) saveCursor(ctx context.Context, c interfaces.EmptyMachineCursor) {
	if g.redis == nil {
		g.cursor = c
		return
	}
	if c.EmptySince.IsZero() {
		_ = g.redis.Del(context.WithoutCancel(ctx), UserMachineGCCursorKey).Err()
		return
	}
	raw, _ := json.Marshal(gcCursorJSON{EmptySince: c.EmptySince, Digest: c.Digest})
	_ = g.redis.Set(context.WithoutCancel(ctx), UserMachineGCCursorKey, raw, 2*UserMachineGCInterval).Err()
}

// retentionDays is the config's current empty-machine window, read before
// the row lock is taken.
func (g *UserMachineGC) retentionDays(ctx context.Context, tenantID uint64, configID string) (int, error) {
	days := allocation.DefaultEmptyRetentionDays
	if g.configs == nil {
		return days, nil
	}
	entity, err := g.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return 0, err
	}
	// GetByID returns (nil, nil) for a soft-deleted config. That is not
	// the default window: config deletion reclaims the machine itself.
	if entity == nil || entity.Config == nil {
		return 0, fmt.Errorf("sandboxalloc: sandbox config %d/%s not found", tenantID, configID)
	}
	return allocation.EffectiveRetentionDays(entity.Config.UserSandboxEmptyRetentionDays), nil
}

func digestPrefix(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

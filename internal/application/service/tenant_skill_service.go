package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// skillImageLockLease bounds how long one install/remove may hold the config
// lock without renewing. Installs run for minutes, so the lease is renewed by
// redislock rather than being set long.
const (
	skillImageLockLease = 30 * time.Second
	skillImageLockRenew = 10 * time.Second

	// skillInstallStuckTTL is how long a run may go without a heartbeat
	// before the reaper treats it as abandoned. It is a silence budget, not
	// a duration budget: a legitimate install that spends two hours in the
	// agent keeps beating and is left alone.
	skillInstallStuckTTL = 60 * time.Minute

	// skillInstallHeartbeatInterval is how often a running install stamps
	// InstallingSince to say its process is still alive. Everything that has
	// to tell "still working" from "died" reads that timestamp.
	skillInstallHeartbeatInterval = 30 * time.Second

	// skillInstallInFlightSkip is how much heartbeat silence makes a second
	// upload of the same archive stop deferring to the run that owns the row.
	// It is a multiple of the heartbeat so a slow install is never mistaken
	// for a dead one, and short enough that a re-upload recovers a dead
	// process in minutes instead of waiting for skillInstallStuckTTL.
	skillInstallInFlightSkip = 3 * time.Minute

	// skillSnapshotRetention is how long a superseded snapshot stays on the
	// provider after the pointer has moved. Live sandboxes may still have
	// been created from it (especially SkillRolloutNewSession); once they
	// expire, the template is only a billed leftover. Twenty-four hours is
	// well past every backend's default sandbox TTL. A config that sets a
	// longer sandbox TTL extends this via snapshotRetentionFor.
	skillSnapshotRetention = 24 * time.Hour

	// skillSnapshotTTLMargin is added on top of a config's own sandbox TTL
	// so an in-flight create that resolved the previous pointer still has
	// a template to boot from.
	skillSnapshotTTLMargin = time.Hour
)

// TenantSkillService owns the skill image lifecycle for sandbox configs.
type TenantSkillService struct {
	skills        repository.TenantSkillRepository
	configs       repository.TenantSandboxConfigRepository
	resolver      interfaces.StorageBackendResolver
	sandboxes     sandbox.TenantSandboxResolver
	sandboxPolicy WorkspaceSandboxPolicy
	agents        interfaces.AgentService
	// installerAgents reads the stored installer record. It is a separate
	// dependency from agents because GetAgentByID lives on the custom agent
	// service, not on interfaces.AgentService.
	installerAgents installerAgentSource
	sessions        interfaces.SessionService
	models          interfaces.ModelService
	redis           *redis.Client

	// streams and messages are the two halves of an install transcript: the
	// replayable event log the console tails, and the durable rows it falls
	// back to once the log's TTL has passed.
	streams  interfaces.StreamManager
	messages interfaces.MessageRepository

	now func() time.Time

	// cleanupTimeout bounds one piece of compensating work. Injectable so a
	// test can let an install outlast it, which every real install does.
	cleanupTimeout time.Duration

	// snapshotRetention is how long a superseded snapshot is kept on the
	// provider. Injectable so a prune test can age a row without waiting a day.
	snapshotRetention time.Duration

	// installHeartbeat is how often a running install restamps its liveness.
	// Injectable so a test can observe a beat without waiting half a minute.
	installHeartbeat time.Duration

	// localLocks serialises installs when Redis is absent. It only guards this
	// process; multi-replica deployments require Redis for cross-process safety.
	localLocks *keyedMutex

	cron    *cron.Cron
	cronMu  sync.Mutex
	started bool
}

// NewTenantSkillService wires the repositories and runtimes the install and
// remove flows share. Redis may be nil; the local lock then serialises one
// process only.
func NewTenantSkillService(
	skillsRepo repository.TenantSkillRepository,
	configsRepo repository.TenantSandboxConfigRepository,
	resolver interfaces.StorageBackendResolver,
	sandboxes sandbox.TenantSandboxResolver,
	sandboxPolicy WorkspaceSandboxPolicy,
	agents interfaces.AgentService,
	customAgents interfaces.CustomAgentService,
	sessions interfaces.SessionService,
	models interfaces.ModelService,
	redisClient *redis.Client,
	streams interfaces.StreamManager,
	messages interfaces.MessageRepository,
) *TenantSkillService {
	return &TenantSkillService{
		skills:            skillsRepo,
		configs:           configsRepo,
		resolver:          resolver,
		sandboxes:         sandboxes,
		sandboxPolicy:     sandboxPolicy,
		agents:            agents,
		installerAgents:   customAgents,
		sessions:          sessions,
		models:            models,
		redis:             redisClient,
		streams:           streams,
		messages:          messages,
		now:               time.Now,
		cleanupTimeout:    installCleanupTimeout,
		snapshotRetention: skillSnapshotRetention,
		installHeartbeat:  skillInstallHeartbeatInterval,
		localLocks:        newKeyedMutex(),
		cron: cron.New(cron.WithSeconds(), cron.WithChain(
			cron.Recover(cron.DefaultLogger),
		)),
	}
}

// withConfigLock serialises every mutation of one config's skill image.
//
// This is not defensive locking: a new snapshot is the OLD snapshot plus this
// run's changes, and the config holds exactly one pointer. Two concurrent
// installs would each snapshot a base that lacks the other's work, and whoever
// wrote the pointer last would silently discard the other install.
func (s *TenantSkillService) withConfigLock(
	ctx context.Context, tenantID uint64, configID string, fn func(context.Context) error,
) error {
	key := skillImageLockKey(tenantID, configID)
	if s.redis == nil {
		release, err := s.localLocks.lock(ctx, key)
		if err != nil {
			return err
		}
		defer release()
		return fn(ctx)
	}
	return redislock.WithRenewableLock(
		ctx, s.redis, key, skillImageLockLease, skillImageLockRenew, fn,
	)
}

func skillImageLockKey(tenantID uint64, configID string) string {
	return fmt.Sprintf("weknora-skill-image-lock:%d:%s", tenantID, configID)
}

// clock is this service's time source. Tests inject one; a service built
// without NewTenantSkillService still gets a working default.
func (s *TenantSkillService) clock() func() time.Time {
	if s != nil && s.now != nil {
		return s.now
	}
	return time.Now
}

// keyedMutex is the no-Redis fallback for withConfigLock.
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]chan struct{}
}

func newKeyedMutex() *keyedMutex { return &keyedMutex{m: map[string]chan struct{}{}} }

func (k *keyedMutex) lock(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	k.mu.Lock()
	entry, ok := k.m[key]
	if !ok {
		entry = make(chan struct{}, 1)
		k.m[key] = entry
	}
	k.mu.Unlock()
	select {
	case entry <- struct{}{}:
		return func() { <-entry }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

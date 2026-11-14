package sandboxalloc

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
)

// integrationEnv returns an integration dependency's address. Missing, the
// test is skipped — unless WEKNORA_REQUIRE_INTEGRATION=1, which turns the
// skip into a failure so an enablement run cannot pass by skipping.
func integrationEnv(t *testing.T, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v != "" {
		return v
	}
	if os.Getenv("WEKNORA_REQUIRE_INTEGRATION") == "1" {
		t.Fatalf("%s is required when WEKNORA_REQUIRE_INTEGRATION=1", name)
	}
	t.Skipf("set %s to run against a real dependency", name)
	return ""
}

// gatingReclaim blocks inside ReclaimUserMachine until release is closed, so
// the replica that won the GC lock is still holding it when the other runs.
type gatingReclaim struct {
	reclaimRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatingReclaim) ReclaimUserMachine(
	ctx context.Context, m allocation.UserMachine, check func(context.Context) (bool, error),
) (bool, error) {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.release:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	return g.reclaimRecorder.ReclaimUserMachine(ctx, m, check)
}

func TestMultiReplicaRealRedisGCRunsOnOneReplica(t *testing.T) {
	addr := integrationEnv(t, "WEKNORA_TEST_REDIS_ADDR")
	raw := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = raw.Close() })
	ctx := context.Background()
	require.NoError(t, raw.Del(ctx, UserMachineGCLockKey, UserMachineGCCursorKey).Err())
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = raw.Del(c, UserMachineGCLockKey, UserMachineGCCursorKey).Err()
	})

	one := 1
	e, _ := releasedMachine(t)
	ops := &gatingReclaim{entered: make(chan struct{}), release: make(chan struct{})}
	cleaner := NewWorkspaceCleaner(e.svc, fakeResolver{mgr: ops})
	configs := retentionConfigs{days: &one}
	open := func() *redis.Client {
		rc := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = rc.Close() })
		return rc
	}
	gc1 := NewUserMachineGC(e.svc, cleaner, configs, open())
	gc2 := NewUserMachineGC(e.svc, cleaner, configs, open())
	later := time.Now().UTC().Add(8 * 24 * time.Hour)
	gc1.now = func() time.Time { return later }
	gc2.now = func() time.Time { return later }

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	type outcome struct {
		report GCReport
		err    error
	}
	ch := make(chan outcome, 2)
	go func() {
		report, err := gc1.RunOnce(runCtx)
		ch <- outcome{report, err}
	}()
	go func() {
		report, err := gc2.RunOnce(runCtx)
		ch <- outcome{report, err}
	}()

	select {
	case <-ops.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("neither replica entered reclaim")
	}
	var skipped, winner outcome
	select {
	case skipped = <-ch:
	case <-time.After(15 * time.Second):
		t.Fatal("the replica that lost the lock did not return")
	}
	require.NoError(t, skipped.err)
	require.True(t, skipped.report.Skipped, "one replica must observe the other's lock and skip")

	close(ops.release)
	select {
	case winner = <-ch:
	case <-time.After(15 * time.Second):
		t.Fatal("the replica that held the lock did not finish")
	}
	require.NoError(t, winner.err)
	require.False(t, winner.report.Skipped)
	require.Equal(t, 1, winner.report.Reclaimed)
}

// placementDatabase is Postgres when WEKNORA_REPOSITORY_TEST_POSTGRES_DSN is
// set, and the shared-cache sqlite pool otherwise. The sqlite path is still a
// regression test; only WEKNORA_REQUIRE_INTEGRATION=1 demands Postgres.
func placementDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN"))
	if dsn == "" {
		if os.Getenv("WEKNORA_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN is required when WEKNORA_REQUIRE_INTEGRATION=1")
		}
		dsn = fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", uuid.NewString())
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		require.NoError(t, db.AutoMigrate(
			&types.Session{}, &types.SessionSandboxAssignment{}, &types.SandboxUserMachine{}, &types.Message{},
		))
		return db
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(
		&types.Session{}, &types.SessionSandboxAssignment{}, &types.SandboxUserMachine{}, &types.Message{},
	))
	return db
}

func TestMultiReplicaPlacementAgreesAcrossServices(t *testing.T) {
	enableUserMode(t)
	db := placementDatabase(t)
	id := uuid.NewString()
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(webSession(id)).Error)

	configs := fakeConfigs{"cfg-a": "user"}
	repo := repository.NewSandboxAllocationRepository(db)
	sessions := repository.NewSessionRepository(db)
	s1 := New(repo, configs, sessions, "weknora")
	s2 := New(repo, configs, sessions, "weknora")
	services := []*Service{s1, s2}

	type placed struct {
		mode   allocation.Mode
		config string
		tenant uint64
		digest string
	}
	var wg sync.WaitGroup
	results := make(chan placed, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, err := services[i%2].Place(context.Background(), PlaceRequest{
				SessionTenantID: 7, SessionID: id, ConfigTenantID: 7, ConfigID: "cfg-a",
			})
			require.NoError(t, err)
			digest := ""
			if m, ok := a.UserMachine(); ok {
				digest = m.Digest(services[i%2].Namespace())
			}
			results <- placed{mode: a.Mode(), config: a.ConfigID, tenant: a.ConfigTenantID, digest: digest}
		}(i)
	}
	wg.Wait()
	close(results)

	var first placed
	n := 0
	for got := range results {
		if n == 0 {
			first = got
		}
		require.Equal(t, first, got, "both services tell every caller the same placement")
		n++
	}
	require.Equal(t, 16, n)
	require.Equal(t, allocation.ModeUser, first.mode)
	require.NotEmpty(t, first.digest)

	var sess types.Session
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Where("id = ?", id).Take(&sess).Error)
	require.Equal(t, first.config, sess.SandboxConfigID, "the pin is the agreed config")
	require.Equal(t, first.tenant, sess.SandboxConfigTenantID)
}

package gormstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/processingcache"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setup(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", t.Name())),
		&gorm.Config{SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := db.DB()
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Error(err)
		}
	})
	if err = db.AutoMigrate(&Model{}); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func record(key string, expires time.Time) *processingcache.Record {
	return &processingcache.Record{
		TenantID:  1,
		ScopeID:   "doc",
		Stage:     "embedding",
		Key:       key,
		Payload:   []byte("first"),
		Checksum:  "checksum",
		ExpiresAt: expires,
	}
}

func TestWinnerAndIsolation(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	r := record("key", time.Now().Add(time.Hour))
	if _, err := s.Get(ctx, 1, "doc", "embedding", "key"); !errors.Is(err, processingcache.ErrMiss) {
		t.Fatalf("miss: %v", err)
	}
	if _, err := s.PutIfAbsent(ctx, r); err != nil {
		t.Fatal(err)
	}
	loser := *r
	loser.Payload = []byte("second")
	got, err := s.PutIfAbsent(ctx, &loser)
	if err != nil || string(got.Payload) != "first" {
		t.Fatalf("winner: %+v %v", got, err)
	}
	for _, v := range []struct {
		tenant       uint64
		scope, stage string
	}{{2, "doc", "embedding"}, {1, "other", "embedding"}, {1, "doc", "summary"}} {
		if _, err = s.Get(ctx, v.tenant, v.scope, v.stage, "key"); !errors.Is(err, processingcache.ErrMiss) {
			t.Fatalf("isolation: %v", err)
		}
	}
}

func TestReplaceExpiredAndCompareDelete(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	old := record("key", time.Now().Add(-time.Hour))
	if _, err := s.PutIfAbsent(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, 1, "doc", "embedding", "key"); !errors.Is(err, processingcache.ErrMiss) {
		t.Fatal(err)
	}
	fresh := *old
	fresh.ExpiresAt = time.Now().Add(time.Hour)
	if _, err := s.PutIfAbsent(ctx, &fresh); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, 1, "doc", "embedding", "key"); err != nil {
		t.Fatalf("stale delete removed refreshed row: %v", err)
	}
	wrong := fresh
	wrong.Checksum = "different"
	if err := s.Delete(ctx, &wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, 1, "doc", "embedding", "key"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, &fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, 1, "doc", "embedding", "key"); !errors.Is(err, processingcache.ErrMiss) {
		t.Fatal(err)
	}
}

func TestBoundedCleanupAndScope(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		r := record(fmt.Sprint(i), time.Now().Add(-time.Hour))
		if _, err := s.PutIfAbsent(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.DeleteExpired(ctx, time.Now(), 2)
	if err != nil || n != 2 {
		t.Fatalf("cleanup %d %v", n, err)
	}
	if n, err = s.DeleteExpired(ctx, time.Now(), 0); err != nil || n != 0 {
		t.Fatalf("zero limit %d %v", n, err)
	}
	other := record("other", time.Now().Add(time.Hour))
	other.TenantID = 2
	if _, err := s.PutIfAbsent(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteScope(ctx, 1, "doc"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, 2, "doc", "embedding", "other"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentWritersKeepOneWinner(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan string, 16)
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := record("same", time.Now().Add(time.Hour))
			r.Payload = []byte(fmt.Sprint(i))
			got, err := s.PutIfAbsent(ctx, r)
			if err != nil {
				errs <- err
				return
			}
			results <- string(got.Payload)
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	winner := ""
	for got := range results {
		if winner == "" {
			winner = got
		}
		if got != winner {
			t.Fatalf("different winners %q %q", winner, got)
		}
	}
}

func TestCleanupPreservesRefreshedCandidate(t *testing.T) {
	s, db := setup(t)
	ctx := context.Background()
	old := record("key", time.Now().Add(-time.Hour))
	if _, err := s.PutIfAbsent(ctx, old); err != nil {
		t.Fatal(err)
	}
	// Refresh after cleanup SELECT and before conditional DELETE.
	fired := false
	if err := db.Callback().Delete().Before("gorm:delete").Register("test:refresh", func(_ *gorm.DB) {
		if fired {
			return
		}
		fired = true
		if err := db.Model(&Model{}).
			Where("tenant_id = ? AND scope_id = ? AND stage = ? AND cache_key = ?", 1, "doc", "embedding", "key").
			Update("expires_at", time.Now().Add(time.Hour)).
			Error; err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteExpired(ctx, time.Now(), 10)
	if err != nil || n != 0 {
		t.Fatalf("refreshed deleted: %d %v", n, err)
	}
	if _, err = s.Get(ctx, 1, "doc", "embedding", "key"); err != nil {
		t.Fatal(err)
	}
}

func TestPutTriggersBoundedOpportunisticCleanup(t *testing.T) {
	s, db := setup(t)
	ctx := context.Background()
	old := record("expired", time.Now().Add(-time.Hour))
	// Seed directly so the initial write is the cleanup trigger.
	row := Model{
		TenantID:  old.TenantID,
		ScopeID:   old.ScopeID,
		Stage:     old.Stage,
		CacheKey:  old.Key,
		Payload:   old.Payload,
		Checksum:  old.Checksum,
		ExpiresAt: old.ExpiresAt,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutIfAbsent(ctx, record("live", time.Now().Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&Model{}).Where("cache_key = ?", "expired").Count(&count)
	if count != 0 {
		t.Fatal("expired record was not cleaned")
	}
	row.CacheKey = "expired-again"
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutIfAbsent(ctx, record("live2", time.Now().Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	db.Model(&Model{}).Where("cache_key = ?", "expired-again").Count(&count)
	if count != 1 {
		t.Fatal("cleanup ran again within one minute")
	}
}

func TestZeroExpiryIsNonExpiring(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	r := record("forever", time.Time{})
	if _, err := s.PutIfAbsent(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, 1, "doc", "embedding", "forever"); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteExpired(ctx, time.Now(), 100)
	if err != nil || n != 0 {
		t.Fatalf("non-expiring deleted %d %v", n, err)
	}
}

func TestMigrationDocumentDeletionAndRollback(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:migration?mode=memory&cache=shared&_foreign_keys=on"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := db.DB()
	raw.SetMaxOpenConns(1)
	defer func() {
		if err := raw.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err = db.Exec("CREATE TABLE knowledges (id VARCHAR(36) PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("../../../migrations/sqlite/000053_processing_cache.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(string(up)).Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO knowledges(id) VALUES (?)", "doc")
	s := New(db)
	ctx := context.Background()
	r := record("key", time.Now().Add(time.Hour))
	if _, err = s.PutIfAbsent(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("DELETE FROM knowledges WHERE id = ?", "doc").Error; err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&Model{}).Count(&n)
	if n != 0 {
		t.Fatalf("cascade retained %d", n)
	}
	if _, err = s.PutIfAbsent(ctx, r); err == nil {
		t.Fatal("late write recreated deleted document cache")
	}
	down, err := os.ReadFile("../../../migrations/sqlite/000053_processing_cache.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(string(down)).Error; err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(&Model{}) {
		t.Fatal("rollback retained cache table")
	}
}

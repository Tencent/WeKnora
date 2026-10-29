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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This integration test uses a disposable schema; the DSN must allow CREATE
// SCHEMA. It never changes application tables or the caller's search_path.
func TestPostgresProcessingCacheMigrationLifecycle(t *testing.T) {
	dsn := os.Getenv("PROCESSING_CACHE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set PROCESSING_CACHE_TEST_POSTGRES_DSN to run PostgreSQL integration")
	}
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), config)
	if err != nil {
		t.Fatal(err)
	}
	adminSQL, _ := admin.DB()
	defer adminSQL.Close()
	schema := fmt.Sprintf("processing_cache_test_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	}()
	// pgx accepts either URL or keyword DSNs. Use its parsed config so adding
	// search_path never concatenates untrusted credentials into SQL or logs.
	connection, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test DSN")
	}
	connection.RuntimeParams["search_path"] = schema
	raw := stdlib.OpenDB(*connection)
	defer raw.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: raw}), config)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.Exec("CREATE TABLE knowledges(id VARCHAR(36) PRIMARY KEY)").Error)
	up, err := os.ReadFile("../../../migrations/versioned/000134_processing_cache.up.sql")
	must(err)
	must(db.Exec(string(up)).Error)
	must(db.Exec("INSERT INTO knowledges VALUES ('source'),('target')").Error)
	store := New(db)
	ctx := context.Background()
	opts := processingcache.Options{ReadEnabled: true, WriteEnabled: true, TTL: time.Hour}
	compute := func(context.Context, []int) ([][]byte, error) { return [][]byte{[]byte("vector")}, nil }
	_, _, err = processingcache.New(store, opts).BatchShared(ctx, 1, "source", "embedding", []any{"input"}, nil, compute)
	must(err)
	_, stats, err := processingcache.New(store, opts).BatchShared(ctx, 1, "target", "embedding", []any{"input"}, nil, func(context.Context, []int) ([][]byte, error) { t.Fatal("shared result recomputed"); return nil, nil })
	must(err)
	if stats.Hits != 1 {
		t.Fatal(stats)
	}
	must(db.Exec("DELETE FROM knowledges WHERE id='source'").Error)
	_, stats, err = processingcache.New(store, opts).BatchShared(ctx, 1, "target", "embedding", []any{"input"}, nil, compute)
	must(err)
	if stats.Hits != 1 {
		t.Fatal("target lost after source deletion")
	}
	for _, expired := range []bool{false, true} {
		key := fmt.Sprint("race-", expired)
		rec := &processingcache.Record{TenantID: 1, ScopeID: "target", Stage: "embedding", Key: key, Payload: []byte("old"), Checksum: "old", ExpiresAt: time.Now().Add(time.Hour)}
		if expired {
			rec.ExpiresAt = time.Now().Add(-time.Hour)
			_, err = store.PutIfAbsent(ctx, rec)
			must(err)
		}
		var wg sync.WaitGroup
		results := make(chan string, 16)
		errs := make(chan error, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				r := *rec
				r.Payload = []byte(fmt.Sprint(i))
				r.Checksum = fmt.Sprint(i)
				r.ExpiresAt = time.Now().Add(time.Hour)
				winner, err := New(db).PutIfAbsent(ctx, &r)
				if err != nil {
					errs <- err
					return
				}
				results <- string(winner.Payload)
			}(i)
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			must(err)
		}
		first := ""
		for v := range results {
			if first == "" {
				first = v
			}
			if v != first {
				t.Fatal("multiple winners")
			}
		}
	}
	must(db.Exec("DELETE FROM knowledges WHERE id='target'").Error)
	var count int64
	must(db.Model(&Model{}).Count(&count).Error)
	if count != 0 {
		t.Fatal("cascade retained rows")
	}
	if _, err := store.PutIfAbsent(ctx, &processingcache.Record{TenantID: 1, ScopeID: "target", Stage: "embedding", Key: "late", Payload: []byte("valid"), Checksum: "checksum", ExpiresAt: time.Now().Add(time.Hour)}); err == nil {
		t.Fatal("late write accepted")
	} else {
		var pgError *pgconn.PgError
		if !errors.As(err, &pgError) || pgError.Code != "23503" {
			t.Fatalf("late write failed for unexpected reason: %v", err)
		}
	}
	down, err := os.ReadFile("../../../migrations/versioned/000134_processing_cache.down.sql")
	must(err)
	must(db.Exec(string(down)).Error)
	if db.Migrator().HasTable(&Model{}) {
		t.Fatal("rollback retained table")
	}
}

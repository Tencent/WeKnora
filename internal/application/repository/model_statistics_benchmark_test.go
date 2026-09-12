package repository

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func BenchmarkModelStatisticsScale(b *testing.B) {
	for _, rows := range []int{10000, 100000, 1000000} {
		for _, concurrency := range []int{1, 4} {
			if rows < 1000000 && concurrency != 1 {
				continue
			}
			b.Run(fmt.Sprintf("rows_%d/concurrency_%d", rows, concurrency), func(b *testing.B) {
				db, err := gorm.Open(sqlite.Open(b.TempDir()+"/statistics.sqlite"), &gorm.Config{})
				if err != nil {
					b.Fatal(err)
				}
				sqlDB, err := db.DB()
				if err != nil {
					b.Fatal(err)
				}
				defer sqlDB.Close()
				sqlDB.SetMaxOpenConns(concurrency)
				if err = db.AutoMigrate(&types.ModelCallRecord{}, &types.ModelPriceVersion{}, &types.EmbeddingCacheLookupRecord{}); err != nil {
					b.Fatal(err)
				}
				now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
				query := `WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<?)
     INSERT INTO model_call_records(id,tenant_id,model_id,model_snapshot,purpose,operation,started_at,duration_ms,status,created_at,updated_at)
     SELECT CAST(n AS TEXT),7,'scale-fixture','{}','general','chat',?,n%1000,'success',?,? FROM seq`
				if err = db.Exec(query, rows, now.Add(-time.Hour), now, now).Error; err != nil {
					b.Fatal(err)
				}
				repo := NewModelStatisticsRepository(db)
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				b.ResetTimer()
				for run := 0; run < b.N; run++ {
					results := make(chan error, concurrency)
					var workers sync.WaitGroup
					workers.Add(concurrency)
					for i := 0; i < concurrency; i++ {
						go func() {
							defer workers.Done()
							ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
							defer cancel()
							statistics, err := repo.QueryModelUsage(
								ctx,
								types.ModelUsageQuery{TenantID: 7, From: now.Add(-24 * time.Hour), To: now},
							)
							if err == nil &&
								(len(statistics) != 1 || statistics[0].CallCount != int64(rows) || statistics[0].Latency.ReportedCalls != int64(rows) || statistics[0].Latency.P50Ms == nil || *statistics[0].Latency.P50Ms != 499.5) {
								err = fmt.Errorf("count or exact median mismatch")
							}
							results <- err
						}()
					}
					workers.Wait()
					close(results)
					for err := range results {
						if err != nil {
							b.Fatal(err)
						}
					}
				}
				b.StopTimer()
				runtime.ReadMemStats(&after)
				b.ReportMetric(
					float64(after.TotalAlloc-before.TotalAlloc)/float64(b.N*concurrency),
					"alloc-bytes/request",
				)
				b.ReportMetric(float64(rows), "rows/request")
				b.ReportMetric(float64(concurrency), "concurrent-requests")
			})
		}
	}
}

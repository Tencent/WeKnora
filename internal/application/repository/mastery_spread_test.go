package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// 日桶读取的日期契约：折叠桶（冷端）不带日期，逐日桶（近端）必须带。
// 调用方正是靠 EventDate 为空来决定「这条要用 cutoff 当代表日期」，若折叠桶带上
// 了某个具体日期，那段历史会被按错误的日期衰减——这是静默算错，必须钉住。
func assertBucketDateContract(t *testing.T, recent, cold []types.MasteryDailyBucket) {
	t.Helper()
	for _, c := range cold {
		if c.EventDate != "" {
			t.Errorf("折叠桶不应带 EventDate，got %q（slug=%s）", c.EventDate, c.Slug)
		}
	}
	for _, r := range recent {
		if r.EventDate == "" {
			t.Errorf("近端逐日桶必须带 EventDate（slug=%s）", r.Slug)
		}
	}
}

func loadSpread(t *testing.T, db *gorm.DB, slug string) (types.MemorySpreadView, bool) {
	t.Helper()
	var row types.MemorySpreadView
	err := db.Where("slug = ?", slug).Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return types.MemorySpreadView{}, false
	}
	if err != nil {
		t.Fatalf("load spread (%s): %v", slug, err)
	}
	return row, true
}

// 预热只写给邻居：自己不留行，重复的邻居（既是出链又是入链）只记一次，
// 空 slug 忽略；同一天内多次阅读按秒数累加。
func TestBumpSpreadViewsSkipsSelfDedupsAndAccumulates(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-spread-bump")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	neighbors := []string{"entity/acme", "entity/other", "entity/other", "", "concept/hub"}
	if err := repo.BumpSpreadViews(ctx, scope, "kb-1", "entity/acme", neighbors, 300); err != nil {
		t.Fatalf("first spread: %v", err)
	}
	if err := repo.BumpSpreadViews(ctx, scope, "kb-1", "entity/acme", neighbors, 60); err != nil {
		t.Fatalf("second spread: %v", err)
	}

	if _, ok := loadSpread(t, db, "entity/acme"); ok {
		t.Errorf("来源页自己不应收到预热")
	}
	other, ok := loadSpread(t, db, "entity/other")
	if !ok {
		t.Fatalf("重复的邻居应只有一行")
	}
	// 同一次调用里重复出现的邻居只记一次（360 = 300 + 60），而不是 720。
	if other.SpreadSeconds != 360 {
		t.Errorf("重复邻居应去重后累加 300+60=360，got %d", other.SpreadSeconds)
	}
	var count int64
	if err := db.Model(&types.MemorySpreadView{}).Where("slug = ?", "entity/other").Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("(租户,用户,库,slug,日期) 应唯一，got %d 行", count)
	}
	if _, ok := loadSpread(t, db, "concept/hub"); !ok {
		t.Errorf("普通邻居应收到预热")
	}
}

// 预热桶与浏览桶同构：cutoff 之内逐日保留，之外按 slug 求和折叠。
// PostgreSQL 的 DATE 列扫进 Go 字符串时是 RFC3339 渲染（"2026-09-10T00:00:00Z"），
// SQLite 给的是写入时的 "2026-09-10"。调用方只认日历日，因此仓储层必须把两种形状
// 归一。这条钉住的是一次真实线上故障：日期解析失败 → 整个桶被跳过 → 预热静默归零
// （它没有汇总行兜底）、浏览切片悄悄退回汇总行，而 SQLite 上的测试全绿。
func TestListDailyBucketsNormalizesDialectDateRendering(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-bucket-day-normalize")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}
	cutoff := time.Now().AddDate(0, 0, -30)

	// SQLite 允许往文本列里写任意字符串，正好用来复现 PG 的渲染形状。
	if err := db.Create(&types.MemorySpreadView{
		ID: "spread-pg-style", TenantID: 7, SubjectID: "web_user:alice",
		KnowledgeBaseID: "kb-1", Slug: "entity/a",
		EventDate: "2026-09-10T00:00:00Z", SpreadSeconds: 300,
	}).Error; err != nil {
		t.Fatalf("seed spread: %v", err)
	}
	if err := db.Create(&types.MemoryMasteryDaily{
		ID: "daily-pg-style", TenantID: 7, SubjectID: "web_user:alice",
		KnowledgeBaseID: "kb-1", Slug: "entity/a", EventType: types.MasteryEventView,
		EventDate: "2026-09-10T00:00:00Z", EventCount: 1, DurationSum: 300,
	}).Error; err != nil {
		t.Fatalf("seed view bucket: %v", err)
	}

	recentSpread, _, err := repo.ListDailySpread(ctx, scope, "kb-1", cutoff)
	if err != nil {
		t.Fatalf("list daily spread: %v", err)
	}
	if len(recentSpread) != 1 || recentSpread[0].EventDate != "2026-09-10" {
		t.Fatalf("预热桶日期应归一为 2026-09-10，got %+v", recentSpread)
	}
	recentViews, _, err := repo.ListDailyViews(ctx, scope, "kb-1", cutoff)
	if err != nil {
		t.Fatalf("list daily views: %v", err)
	}
	if len(recentViews) != 1 || recentViews[0].EventDate != "2026-09-10" {
		t.Fatalf("浏览桶日期应归一为 2026-09-10，got %+v", recentViews)
	}
	// 归一后的形状必须能被调用方的解析接受，否则整桶仍会被丢掉。
	if _, perr := time.ParseInLocation("2006-01-02", recentSpread[0].EventDate, time.Local); perr != nil {
		t.Fatalf("归一后的日期应可解析：%v", perr)
	}
}

func TestListDailySpreadSplitsRecentAndCold(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-spread-split")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	day := func(offset int) string {
		return time.Now().AddDate(0, 0, offset).Format("2006-01-02")
	}
	seed := func(slug, date string, seconds int64) {
		t.Helper()
		row := &types.MemorySpreadView{
			ID:              "spread-" + slug + "-" + date,
			TenantID:        scope.TenantID,
			SubjectID:       scope.SubjectID,
			KnowledgeBaseID: "kb-1",
			Slug:            slug,
			EventDate:       date,
			SpreadSeconds:   seconds,
		}
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("seed %s %s: %v", slug, date, err)
		}
	}

	seed("entity/acme", day(-1), 300)
	seed("entity/acme", day(-100), 120)
	seed("concept/hub", day(-200), 60)

	recent, cold, err := repo.ListDailySpread(ctx, scope, "kb-1", time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("list daily spread: %v", err)
	}
	if len(recent) != 1 {
		t.Errorf("30 天内的预热桶应逐日保留，got %d 条", len(recent))
	}
	if len(cold) != 2 {
		t.Errorf("30 天前的应按 slug 折叠成 2 条，got %d 条", len(cold))
	}
	sums := make(map[string]int64, len(cold))
	for _, c := range cold {
		sums[c.Slug] = c.Seconds
	}
	if sums["entity/acme"] != 120 || sums["concept/hub"] != 60 {
		t.Errorf("冷端求和错误：%v", sums)
	}
	assertBucketDateContract(t, recent, cold)
}

// 一次浏览的邻居写入是「一条语句写完」，所以要跨越 spreadWriteBatchSize 的批次
// 边界也不能漏行，且与去重语义不冲突。重复的邻居在 SQLite 上会表现为秒数被累加
// 两次（在 PostgreSQL 上则直接报 "cannot affect row a second time"），两条路径都
// 由「每个邻居恰好一次、秒数恰好一份」钉住。
func TestBumpSpreadViewsWritesEveryNeighborAcrossBatchBoundary(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-spread-batch")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	const neighbors = spreadWriteBatchSize + 40
	slugs := make([]string, 0, neighbors*2+2)
	for i := 0; i < neighbors; i++ {
		slug := fmt.Sprintf("entity/n%03d", i)
		slugs = append(slugs, slug, slug) // 每个邻居出现两次
	}
	slugs = append(slugs, "", "entity/source")
	if err := repo.BumpSpreadViews(ctx, scope, "kb-1", "entity/source", slugs, 120); err != nil {
		t.Fatalf("batch spread: %v", err)
	}

	var rows []types.MemorySpreadView
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load spread rows: %v", err)
	}
	if len(rows) != neighbors {
		t.Errorf("应写入 %d 行（重复折叠、自己与空忽略），got %d", neighbors, len(rows))
	}
	for _, r := range rows {
		if r.SpreadSeconds != 120 {
			t.Fatalf("%s 的秒数应为 120（重复邻居只计一次），got %d", r.Slug, r.SpreadSeconds)
		}
	}
}

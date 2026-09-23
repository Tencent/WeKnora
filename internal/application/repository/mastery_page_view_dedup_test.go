package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 同页去重窗口的语义是「只折次数，不折时长」：
//
//	要挡的是反复开关页面刷次数（6 次 × 5 秒靠 ViewWeight 就能推到 100%）；
//	不能丢的是窗口内第二次认真阅读的时长（丢了会逼用户「过会儿再来看」，体验很差）。
//
// 窗口是服务器本地日历日，不是短促的爆发窗口——30 分钟窗只要「每半小时点一次」
// 就能绕过，跨天回访才是真正的重复。
// 这组测试把三条都钉住。
func newMasteryDedupDB(t *testing.T, name string) (*gorm.DB, interfaces.MasteryRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&types.MemoryPageView{},
		&types.MemoryMasteryDaily{},
		&types.MemoryGuideExposure{},
		&types.MemorySpreadView{},
		&types.MemoryCitation{},
		&types.MemoryAnswerLike{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, NewMasteryRepository(db)
}

func loadPageView(t *testing.T, db *gorm.DB, subjectID, slug string) types.MemoryPageView {
	t.Helper()
	var row types.MemoryPageView
	if err := db.Where("subject_id = ? AND slug = ?", subjectID, slug).Take(&row).Error; err != nil {
		t.Fatalf("load page view (%s/%s): %v", subjectID, slug, err)
	}
	return row
}

// 反复开关同一页面：次数折叠，但时长照常累计——分数只有 2 + 30/60 = 2.5，
// 仍停在「接触」档，靠手速刷不到满格。
func TestBumpPageViewCollapsesCountButKeepsDuration(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-dedup-collapse")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	for i := 0; i < 6; i++ {
		if err := repo.BumpPageView(ctx, scope, "kb-1", "entity/acme", 5); err != nil {
			t.Fatalf("bump page view #%d: %v", i, err)
		}
	}

	row := loadPageView(t, db, "web_user:alice", "entity/acme")
	if row.ViewCount != 1 {
		t.Errorf("窗口内重复浏览必须折叠为一次，got view_count=%d", row.ViewCount)
	}
	if row.TotalDuration != 30 {
		t.Errorf("真实浏览时长不得被丢弃，got total_duration=%d（want 30 = 6×5）", row.TotalDuration)
	}
}

// 用户最在意的场景：先扫一眼 5 秒，同一天内回来认真读 180 秒。
// 这一次是真实学习，时长必须计入——次数虽然当天不再增加，水位仍会因时长上涨，
// 用户不必等到第二天才看到变化。
func TestBumpPageViewKeepsLongReadInsideWindow(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-dedup-longread")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	if err := repo.BumpPageView(ctx, scope, "kb-1", "concept/small-topic", 5); err != nil {
		t.Fatalf("first skim: %v", err)
	}
	if err := repo.BumpPageView(ctx, scope, "kb-1", "concept/small-topic", 180); err != nil {
		t.Fatalf("second long read: %v", err)
	}

	row := loadPageView(t, db, "web_user:alice", "concept/small-topic")
	if row.ViewCount != 1 {
		t.Errorf("次数仍应折叠，got view_count=%d", row.ViewCount)
	}
	if row.TotalDuration != 185 {
		t.Errorf("窗口内的长阅读必须计入，got total_duration=%d（want 185 = 5+180）", row.TotalDuration)
	}
	// 5+180=185 秒 → 时长贡献 185/60≈3.08，加 ViewWeight(2) = 5.08 分，
	// 落在「熟悉」档（40~60）：一个小知识点认真读三分钟就该到这个位置。
}

// 去重只针对「同租户 + 同用户 + 同库 + 同页」。
func TestBumpPageViewDedupIsScoped(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-dedup-scope")
	ctx := context.Background()
	alice := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}
	bob := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:bob"}

	if err := repo.BumpPageView(ctx, alice, "kb-1", "entity/acme", 5); err != nil {
		t.Fatalf("bump alice: %v", err)
	}
	if err := repo.BumpPageView(ctx, bob, "kb-1", "entity/acme", 5); err != nil {
		t.Fatalf("bump bob: %v", err)
	}
	if err := repo.BumpPageView(ctx, alice, "kb-1", "entity/other", 5); err != nil {
		t.Fatalf("bump other slug: %v", err)
	}

	if got := loadPageView(t, db, "web_user:bob", "entity/acme").ViewCount; got != 1 {
		t.Errorf("去重不得跨用户生效，got view_count=%d", got)
	}
	if got := loadPageView(t, db, "web_user:alice", "entity/other").ViewCount; got != 1 {
		t.Errorf("不同页面各自独立计数，got view_count=%d", got)
	}
}

// 去重窗口是本地日历日：跨天回访算一次新的重复。这条把「每半小时点一次」的绕过
// 路径钉死——30 分钟窗下三小时的零散点击就能攒满 6 次重复（6 × ViewWeight 2 = 12
// ≥ SatScore）把节点推到永久饱和；按天后最省力的路径变成「连续 6 天各来一次」。
func TestBumpPageViewCountsAgainNextDay(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-dedup-crossday")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	if err := repo.BumpPageView(ctx, scope, "kb-1", "entity/acme", 5); err != nil {
		t.Fatalf("first view: %v", err)
	}
	// 把上一次浏览推到昨天，模拟「昨天来过」。
	stale := types.MasteryPageViewDayStart(time.Now()).Add(-time.Minute)
	if err := db.Model(&types.MemoryPageView{}).
		Where("subject_id = ? AND slug = ?", "web_user:alice", "entity/acme").
		Update("last_view_at", stale).Error; err != nil {
		t.Fatalf("age the previous view: %v", err)
	}
	if err := repo.BumpPageView(ctx, scope, "kb-1", "entity/acme", 5); err != nil {
		t.Fatalf("second view: %v", err)
	}

	if got := loadPageView(t, db, "web_user:alice", "entity/acme").ViewCount; got != 2 {
		t.Errorf("跨天回访应计为新的重复，got view_count=%d（want 2）", got)
	}
}

// 日聚合桶是时间切分回放的数据源，必须与实时状态同口径：次数折叠、时长累计。
func TestBumpPageViewDedupKeepsDailyBucketConsistent(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-dedup-daily")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	for i := 0; i < 4; i++ {
		if err := repo.BumpPageView(ctx, scope, "kb-1", "entity/acme", 5); err != nil {
			t.Fatalf("bump page view #%d: %v", i, err)
		}
	}

	var daily types.MemoryMasteryDaily
	if err := db.Where("subject_id = ? AND event_type = ?", "web_user:alice", types.MasteryEventView).
		Take(&daily).Error; err != nil {
		t.Fatalf("load daily bucket: %v", err)
	}
	if daily.EventCount != 1 {
		t.Errorf("日桶次数应与 view_count 一致，got event_count=%d", daily.EventCount)
	}
	if daily.DurationSum != 20 {
		t.Errorf("日桶时长不得被折叠，got duration_sum=%d（want 20 = 4×5）", daily.DurationSum)
	}
}

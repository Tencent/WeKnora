package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 窗口起点固定：命中窗口的重复浏览不得前移 last_view_at，否则「连续刷」会把
// 同一个窗口无限延长，去重窗口就形同虚设。
func TestBumpPageViewKeepsWindowStartFixed(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-window-start")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	if err := repo.BumpPageView(ctx, scope, "kb-1", "entity/acme", 5); err != nil {
		t.Fatalf("first view: %v", err)
	}
	first := loadPageView(t, db, "web_user:alice", "entity/acme")

	if err := repo.BumpPageView(ctx, scope, "kb-1", "entity/acme", 5); err != nil {
		t.Fatalf("second view: %v", err)
	}
	second := loadPageView(t, db, "web_user:alice", "entity/acme")

	if second.ViewCount != 1 {
		t.Errorf("窗口内重复浏览不应增加次数，got view_count=%d", second.ViewCount)
	}
	if !second.LastViewAt.Equal(first.LastViewAt) {
		t.Errorf("命中窗口不得前移起点：first=%v second=%v", first.LastViewAt, second.LastViewAt)
	}
	if second.TotalDuration != 10 {
		t.Errorf("时长仍应累计，got total_duration=%d（want 10）", second.TotalDuration)
	}
}

// 汇总行的次数与日桶的次数必须一致。两者若采用不同口径（滚动 24 小时 vs 日历日），
// 就会出现「画像显示 5 次却已满格」——评分读日桶，展示读汇总行，用户会看到自相矛盾
// 的画像。这条把两个口径钉成同一个。
func TestBumpPageViewCountMatchesDailyBucketCount(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-count-consistency")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	const days = 5
	for i := 0; i < days; i++ {
		if err := repo.BumpPageView(ctx, scope, "kb-1", "entity/acme", 5); err != nil {
			t.Fatalf("view #%d: %v", i, err)
		}
		// 把这次浏览连同它的日桶一起挪到 (i+1) 天前，形成「每天来一次」的历史。
		when := time.Now().AddDate(0, 0, -(i + 1))
		if err := db.Model(&types.MemoryPageView{}).
			Where("subject_id = ? AND slug = ?", "web_user:alice", "entity/acme").
			Update("last_view_at", when).Error; err != nil {
			t.Fatalf("age view #%d: %v", i, err)
		}
		if err := db.Model(&types.MemoryMasteryDaily{}).
			Where("slug = ? AND event_type = ? AND event_date = ?", "entity/acme",
				types.MasteryEventView, time.Now().Format("2006-01-02")).
			Update("event_date", when.Format("2006-01-02")).Error; err != nil {
			t.Fatalf("age bucket #%d: %v", i, err)
		}
	}

	row := loadPageView(t, db, "web_user:alice", "entity/acme")
	var buckets []types.MemoryMasteryDaily
	if err := db.Where("slug = ? AND event_type = ?", "entity/acme", types.MasteryEventView).
		Find(&buckets).Error; err != nil {
		t.Fatalf("load buckets: %v", err)
	}
	bucketCount := 0
	for _, b := range buckets {
		bucketCount += b.EventCount
	}
	if row.ViewCount != days {
		t.Errorf("每天一次、共 %d 天应记 %d 次，got view_count=%d", days, days, row.ViewCount)
	}
	if len(buckets) != days || bucketCount != row.ViewCount {
		t.Errorf("汇总行与日桶必须同口径：view_count=%d 桶数=%d 桶次数和=%d",
			row.ViewCount, len(buckets), bucketCount)
	}
}

// 日桶按 cutoff 分成「近期逐日」与「冷端按 slug 折叠」两段；冷端的求和必须
// 覆盖该 slug 的全部旧桶，否则折叠会丢证据。
func TestListDailyViewsSplitsRecentAndCold(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-daily-split")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	day := func(offset int) string {
		return time.Now().AddDate(0, 0, offset).Format("2006-01-02")
	}
	seed := func(slug, date string, count int, duration int64) {
		t.Helper()
		row := &types.MemoryMasteryDaily{
			ID:              "daily-" + slug + "-" + date,
			TenantID:        scope.TenantID,
			SubjectID:       scope.SubjectID,
			KnowledgeBaseID: "kb-1",
			Slug:            slug,
			EventType:       types.MasteryEventView,
			EventDate:       date,
			EventCount:      count,
			DurationSum:     duration,
		}
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("seed %s %s: %v", slug, date, err)
		}
	}

	seed("entity/acme", day(-1), 1, 30)
	seed("entity/acme", day(-2), 1, 60)
	seed("entity/acme", day(-100), 1, 300)
	seed("concept/other", day(-200), 1, 60)
	seed("concept/other", day(-201), 1, 120)

	recent, cold, err := repo.ListDailyViews(ctx, scope, "kb-1", time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("list daily views: %v", err)
	}
	if len(recent) != 2 {
		t.Errorf("30 天内的桶应逐日保留，got %d 条", len(recent))
	}
	if len(cold) != 2 {
		t.Errorf("30 天前的桶应按 slug 折叠成 2 条，got %d 条", len(cold))
	}
	sums := make(map[string][2]int64, len(cold))
	for _, c := range cold {
		sums[c.Slug] = [2]int64{int64(c.Count), c.Seconds}
	}
	if got := sums["entity/acme"]; got != [2]int64{1, 300} {
		t.Errorf("entity/acme 的冷端折叠应为 {1,300}，got %v", got)
	}
	if got := sums["concept/other"]; got != [2]int64{2, 180} {
		t.Errorf("concept/other 的冷端折叠应为 {2,180}，got %v", got)
	}
	assertBucketDateContract(t, recent, cold)
}

// 曝光去重按服务器本地日历日判定，与日桶的 event_date 同口径：
// 昨天的展示不算「今天」，今天的展示才算。
func TestListExposedTodayUsesLocalDay(t *testing.T) {
	db, repo := newMasteryDedupDB(t, "mastery-exposure-day")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}

	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	seed := func(candidate string, shownAt time.Time) {
		t.Helper()
		row := &types.MemoryGuideExposure{
			ID:              "exp-" + candidate,
			TenantID:        scope.TenantID,
			SubjectID:       scope.SubjectID,
			KnowledgeBaseID: "kb-1",
			TriggerSlug:     "entity/hub",
			CandidateSlug:   candidate,
			Strategy:        types.GuideStrategyPPRBoundary,
			Rank:            1,
			ShownAt:         shownAt,
		}
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("seed exposure %s: %v", candidate, err)
		}
	}

	// 用「本地零点 ± 1 分钟」构造边界，避免测试在凌晨运行时把 -2h 算到昨天。
	seed("entity/today", dayStart.Add(time.Minute))
	seed("entity/yesterday", dayStart.Add(-time.Minute))

	shown, err := repo.ListExposedToday(ctx, scope, "kb-1", "entity/hub")
	if err != nil {
		t.Fatalf("list today's exposures: %v", err)
	}
	if _, ok := shown["entity/today"]; !ok {
		t.Errorf("当天展示过的候选应判定为已展示")
	}
	if _, ok := shown["entity/yesterday"]; ok {
		t.Errorf("昨天的展示不应算作「今天」")
	}
}

// bucketIndex 把一次日桶读归一成以日期为键的表，让两条读取路径的结果可以比较而
// 不受行顺序影响；折叠行（EventDate 为空）保留为独立键，不去猜它代表哪一天。
func bucketIndex(buckets []types.MasteryDailyBucket) map[string]types.MasteryDailyBucket {
	out := make(map[string]types.MasteryDailyBucket, len(buckets))
	for _, b := range buckets {
		out[b.EventDate] = b
	}
	return out
}

// 单节点窄查询与全量读必须给出同一份证据。前者存在的唯一理由是省掉「为一个节点读
// 整个知识库」，而不是换一套口径 —— 页面浏览回传的水位必须与图谱上显示的完全一致。
// 这条把两条路径钉成同一个结果，否则将来收窄查询时会悄悄让两者分叉。
func TestLoadNodeLedgerMatchesKnowledgeBaseRead(t *testing.T) {
	_, repo := newMasteryDedupDB(t, "mastery-node-ledger")
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "web_user:alice"}
	cutoff := time.Now().AddDate(0, 0, -30)

	// 本节点：浏览一次、被来源文档引用、收到一个点赞，并被另一页的阅读预热。
	if err := repo.BumpPageView(ctx, scope, "kb-1", "entity/acme", 5); err != nil {
		t.Fatalf("view acme: %v", err)
	}
	if err := repo.BumpPageView(ctx, scope, "kb-1", "entity/other", 9); err != nil {
		t.Fatalf("view other: %v", err)
	}
	if err := repo.BumpSpreadViews(ctx, scope, "kb-1", "entity/other",
		[]string{"entity/acme", "entity/neighbour"}, 9); err != nil {
		t.Fatalf("spread: %v", err)
	}
	if err := repo.BumpCitation(ctx, scope, []types.MemoryDocAffinity{
		{KnowledgeID: "doc-a", KnowledgeBaseID: "kb-1"},
		{KnowledgeID: "doc-z", KnowledgeBaseID: "kb-2"},
	}); err != nil {
		t.Fatalf("citation: %v", err)
	}
	if err := repo.RecordAnswerLike(ctx, scope, &types.MemoryAnswerLike{
		MessageID: "msg-1",
		Allocations: types.LikeAllocations{
			{KnowledgeID: "doc-a", KnowledgeBaseID: "kb-1", CreditedWeight: 0.75},
		},
	}); err != nil {
		t.Fatalf("like: %v", err)
	}

	narrow, err := repo.LoadNodeLedger(ctx, scope, "kb-1", "entity/acme", []string{"doc-a"}, cutoff)
	if err != nil {
		t.Fatalf("load node ledger: %v", err)
	}

	// 浏览行：窄查询只能拿到这一个 slug 的。
	views, err := repo.ListPageViews(ctx, scope, "kb-1")
	if err != nil {
		t.Fatalf("list page views: %v", err)
	}
	if narrow.View == nil {
		t.Fatalf("窄查询应返回本节点的浏览行")
	}
	if narrow.View.Slug != "entity/acme" {
		t.Errorf("浏览行不应越界到其它 slug，got %q", narrow.View.Slug)
	}
	var wantView *types.MemoryPageView
	for _, v := range views {
		if v.Slug == "entity/acme" {
			wantView = v
		}
	}
	if wantView == nil ||
		narrow.View.ViewCount != wantView.ViewCount ||
		narrow.View.TotalDuration != wantView.TotalDuration ||
		!narrow.View.LastViewAt.Equal(wantView.LastViewAt) {
		t.Errorf("浏览行与全量读不一致：narrow=%+v want=%+v", narrow.View, wantView)
	}

	// 日桶：窄查询的结果必须等于全量读里属于本 slug 的那部分（含折叠行）。
	recent, cold, err := repo.ListDailyViews(ctx, scope, "kb-1", cutoff)
	if err != nil {
		t.Fatalf("list daily views: %v", err)
	}
	wantViewBuckets := map[string]types.MasteryDailyBucket{}
	for _, b := range append(append([]types.MasteryDailyBucket{}, recent...), cold...) {
		if b.Slug == "entity/acme" {
			wantViewBuckets[b.EventDate] = b
		}
	}
	if !equalBucketIndex(bucketIndex(narrow.ViewBuckets), wantViewBuckets) {
		t.Errorf("浏览日桶与全量读不一致：narrow=%v want=%v",
			bucketIndex(narrow.ViewBuckets), wantViewBuckets)
	}

	// 预热日桶：同上，且必须排除另一个节点自己收到的那一份。
	recentSpread, coldSpread, err := repo.ListDailySpread(ctx, scope, "kb-1", cutoff)
	if err != nil {
		t.Fatalf("list daily spread: %v", err)
	}
	wantSpreadBuckets := map[string]types.MasteryDailyBucket{}
	for _, b := range append(append([]types.MasteryDailyBucket{}, recentSpread...), coldSpread...) {
		if b.Slug == "entity/acme" {
			wantSpreadBuckets[b.EventDate] = b
		}
	}
	if len(wantSpreadBuckets) == 0 {
		t.Fatalf("用例应至少为本节点产生一条预热桶")
	}
	if !equalBucketIndex(bucketIndex(narrow.SpreadBuckets), wantSpreadBuckets) {
		t.Errorf("预热日桶与全量读不一致：narrow=%v want=%v",
			bucketIndex(narrow.SpreadBuckets), wantSpreadBuckets)
	}

	// 引用行按来源文档收窄：只带 doc-a，不能把另一个知识库的 doc-z 也读进来。
	citations, err := repo.ListCitations(ctx, scope, "kb-1")
	if err != nil {
		t.Fatalf("list citations: %v", err)
	}
	wantCitations := 0
	for _, c := range citations {
		if c.KnowledgeID == "doc-a" {
			wantCitations++
		}
	}
	if wantCitations == 0 {
		t.Fatalf("用例应至少为本节点产生一条引用行")
	}
	if len(narrow.Citations) != wantCitations {
		t.Errorf("引用行数不一致：narrow=%d want=%d", len(narrow.Citations), wantCitations)
	}
	for _, c := range narrow.Citations {
		if c.KnowledgeID != "doc-a" {
			t.Errorf("引用行不应包含来源之外的文档，got %q", c.KnowledgeID)
		}
	}

	// 从未浏览过的页面：没有浏览行，日桶为空，而不是报错。
	fresh, err := repo.LoadNodeLedger(ctx, scope, "kb-1", "entity/never", nil, cutoff)
	if err != nil {
		t.Fatalf("load untouched node: %v", err)
	}
	if fresh.View != nil || len(fresh.ViewBuckets) != 0 || len(fresh.SpreadBuckets) != 0 {
		t.Errorf("未浏览过的节点不应带回任何证据：%+v", fresh)
	}
}

func equalBucketIndex(a, b map[string]types.MasteryDailyBucket) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w.Count != v.Count || w.Seconds != v.Seconds || w.Slug != v.Slug {
			return false
		}
	}
	return true
}

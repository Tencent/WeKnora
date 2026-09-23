package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// masteryRepository stores the knowledge-guidance ledgers. It mirrors
// memoryRepository's scoping and upsert discipline so every row is
// (tenant_id, subject_id)-isolated and writes are idempotent under retry.
type masteryRepository struct {
	db *gorm.DB
}

// NewMasteryRepository creates the knowledge-guidance ledger repository.
func NewMasteryRepository(db *gorm.DB) interfaces.MasteryRepository {
	return &masteryRepository{db: db}
}

func (r *masteryRepository) scoped(ctx context.Context, scope interfaces.MemoryScope) *gorm.DB {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID)
}

// BumpCitation records cited docs in the guidance ledger, in two statements no
// matter how many documents the answer cited.
//
// It used to be an insert-then-increment pair per document plus a two-statement
// daily bump — four round trips per citation, on the path every RAG answer takes.
// The shape is now the one BumpSpreadViews established: deduplicate in Go, then a
// single multi-row upsert, because PostgreSQL rejects a statement that touches one
// conflict key twice.
//
// The deduplication also has to keep the old per-row rule, which is why the last
// mention of a document wins for knowledge_base_id but an unknown knowledge base
// never erases a known one: the single-row form updated that column only when it
// was non-empty.
func (r *masteryRepository) BumpCitation(
	ctx context.Context, scope interfaces.MemoryScope, docs []types.MemoryDocAffinity,
) error {
	if len(docs) == 0 {
		return nil
	}
	now := time.Now()

	type citedDoc struct {
		knowledgeID string
		kbID        string
	}
	order := make([]string, 0, len(docs))
	byID := make(map[string]*citedDoc, len(docs))
	for _, doc := range docs {
		if doc.KnowledgeID == "" {
			continue
		}
		existing, ok := byID[doc.KnowledgeID]
		if !ok {
			byID[doc.KnowledgeID] = &citedDoc{knowledgeID: doc.KnowledgeID, kbID: doc.KnowledgeBaseID}
			order = append(order, doc.KnowledgeID)
			continue
		}
		if doc.KnowledgeBaseID != "" {
			existing.kbID = doc.KnowledgeBaseID
		}
	}
	if len(order) == 0 {
		return nil
	}

	rows := make([]*types.MemoryCitation, 0, len(order))
	daily := make([]dailyEntry, 0, len(order))
	for _, id := range order {
		cited := byID[id]
		rows = append(rows, &types.MemoryCitation{
			ID:              uuid.New().String(),
			TenantID:        scope.TenantID,
			SubjectID:       scope.SubjectID,
			KnowledgeID:     cited.knowledgeID,
			KnowledgeBaseID: cited.kbID,
			// The increment rides on the proposed row so a fresh insert and an
			// upserted increment land on the same value.
			CiteCount:   1,
			LastCitedAt: now,
		})
		// 日聚合：citation 事件按文档 knowledge_id 记账。
		daily = append(daily, dailyEntry{
			kbID:      cited.kbID,
			subject:   cited.knowledgeID,
			eventType: types.MasteryEventCitation,
			count:     1,
		})
	}

	if err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "knowledge_id"},
			},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"cite_count":    gorm.Expr("memory_citations.cite_count + excluded.cite_count"),
				"last_cited_at": gorm.Expr("excluded.last_cited_at"),
				"knowledge_base_id": gorm.Expr(
					"CASE WHEN excluded.knowledge_base_id <> '' THEN excluded.knowledge_base_id " +
						"ELSE memory_citations.knowledge_base_id END"),
				"updated_at": now,
			}),
		}).
		CreateInBatches(rows, masteryWriteBatchSize).Error; err != nil {
		return err
	}
	return r.bumpDailyBatch(ctx, scope, daily)
}

// BumpPageView records one effective page view with its duration.
//
// The window verdict is evaluated by the database inside a single upsert, not by
// a preceding SELECT. The old shape (count → decide → increment) had a TOCTOU
// window: two concurrent views of a page whose window had just expired could both
// conclude "not repeated" and each add a count, so the folded row could gain two
// repetitions from one moment. Folding it into one statement also drops two
// round trips per view.
func (r *masteryRepository) BumpPageView(
	ctx context.Context, scope interfaces.MemoryScope, kbID, slug string, duration int64,
) error {
	if slug == "" {
		return nil
	}
	now := time.Now()
	// 同一天内只计一次：以服务器本地日历日为零点，与日聚合桶、曝光去重同口径。
	dayStart := types.MasteryPageViewDayStart(now)

	// 同页去重：同一节点在同一天内重复打开，只折叠「浏览次数」，「时长」照常累计。
	//
	// 只折次数、不折时长是刻意的：需要挡的是「反复开关页面刷次数」——6 次 × 5 秒
	// 的点击就能靠 ViewWeight 把节点推到 100%；但当天第二次认真阅读（几分钟）
	// 是真实的学习行为，把它丢掉会逼用户「过一会儿再来看一遍」才能涨水位，体验很差。
	// 时长权重本身偏低（每 60 秒 1 分），累计它不会破坏「跨天」的档位语义。
	//
	// 命中窗口时不前移 last_view_at：当天起点因此保持固定，连续刷不会把同一个
	// 计数窗口无限延长。判定改用 CASE 表达式后，起点与自增在同一条语句内完成。
	row := &types.MemoryPageView{
		ID:              uuid.New().String(),
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		KnowledgeBaseID: kbID,
		Slug:            slug,
		ViewCount:       1,        // 首次插入即在窗口外，直接计一次
		TotalDuration:   duration, // 首次插入即带上本次时长
		LastViewAt:      now,
	}
	if err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"}, {Name: "subject_id"},
				{Name: "knowledge_base_id"}, {Name: "slug"},
			},
			// 表达式里的列必须带表限定：PostgreSQL 的 DO UPDATE 作用域里同时有
			// 目标表与 excluded，未限定名会报 "column reference is ambiguous"。
			DoUpdates: clause.Assignments(map[string]interface{}{
				"view_count": gorm.Expr(
					"memory_page_views.view_count + CASE WHEN memory_page_views.last_view_at >= ? THEN 0 ELSE 1 END",
					dayStart),
				"total_duration": gorm.Expr("memory_page_views.total_duration + ?", duration),
				"last_view_at": gorm.Expr(
					"CASE WHEN memory_page_views.last_view_at >= ? THEN memory_page_views.last_view_at ELSE ? END",
					dayStart, now),
				"updated_at": now,
			}),
		}).
		Create(row).Error; err != nil {
		return err
	}
	// 日聚合：view 事件按页面 slug 记账，支持时间切分回放与逐日衰减。次数与
	// view_count 同口径（按窗口折叠，且每天最多 1 次），时长照常累计。
	return r.bumpDailyOncePerDay(ctx, scope, kbID, slug, types.MasteryEventView, duration)
}

// masteryWriteBatchSize bounds how many ledger rows go into one statement. It is a
// safety valve for pathological input, not the normal path: one answer cites a
// handful of documents and one ripple holds at most three candidates.
const masteryWriteBatchSize = 128

// dailyEntry is one increment of the daily aggregation bucket: how much of one
// signal landed on one subject on one day.
type dailyEntry struct {
	kbID      string
	subject   string // page slug for views, knowledge id for citations and likes
	eventType string
	count     int
	duration  int64
}

// bumpDailyBatch upserts daily buckets in one statement.
//
// It replaced an insert-then-update pair per row: two round trips for what a single
// ON CONFLICT DO UPDATE expresses, and the sibling writers (bumpDailyOncePerDay,
// BumpSpreadViews) had already moved to the single-statement shape — keeping both
// meant the same invariant documented in two places and enforced in one.
//
// Entries sharing a conflict key are summed here before the statement is built.
// That is not only tidiness: PostgreSQL rejects a statement that touches one
// conflict key twice, so a caller must not be able to build one by accident.
func (r *masteryRepository) bumpDailyBatch(
	ctx context.Context, scope interfaces.MemoryScope, entries []dailyEntry,
) error {
	if len(entries) == 0 {
		return nil
	}
	now := time.Now()
	date := now.Format("2006-01-02")

	type dailyKey struct{ kbID, subject, eventType string }
	order := make([]dailyKey, 0, len(entries))
	merged := make(map[dailyKey]*dailyEntry, len(entries))
	for _, e := range entries {
		if e.subject == "" || e.eventType == "" {
			continue
		}
		if e.count == 0 && e.duration == 0 {
			continue
		}
		k := dailyKey{e.kbID, e.subject, e.eventType}
		if existing, ok := merged[k]; ok {
			existing.count += e.count
			existing.duration += e.duration
			continue
		}
		copied := e
		merged[k] = &copied
		order = append(order, k)
	}
	if len(order) == 0 {
		return nil
	}

	rows := make([]*types.MemoryMasteryDaily, 0, len(order))
	for _, k := range order {
		e := merged[k]
		rows = append(rows, &types.MemoryMasteryDaily{
			ID:              uuid.New().String(),
			TenantID:        scope.TenantID,
			SubjectID:       scope.SubjectID,
			KnowledgeBaseID: e.kbID,
			Slug:            e.subject,
			EventType:       e.eventType,
			EventDate:       date,
			// The insert path carries its own increment, so a fresh row and an
			// upserted one land on the same value.
			EventCount:  e.count,
			DurationSum: e.duration,
		})
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "knowledge_base_id"},
				{Name: "slug"}, {Name: "event_type"}, {Name: "event_date"},
			},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"event_count":  gorm.Expr("memory_mastery_daily.event_count + excluded.event_count"),
				"duration_sum": gorm.Expr("memory_mastery_daily.duration_sum + excluded.duration_sum"),
				"updated_at":   now,
			}),
		}).
		CreateInBatches(rows, masteryWriteBatchSize).Error
}

// bumpDaily upserts a single daily aggregation bucket. The slug column stores the
// event subject: a page slug for views, a knowledge id for citations. It is the
// cheap event ledger that backs time-split replay and time-decay without a full log.
func (r *masteryRepository) bumpDaily(
	ctx context.Context, scope interfaces.MemoryScope, kbID, subject, eventType string, count int, duration int64,
) error {
	return r.bumpDailyBatch(ctx, scope, []dailyEntry{{
		kbID:      kbID,
		subject:   subject,
		eventType: eventType,
		count:     count,
		duration:  duration,
	}})
}

// bumpDailyOncePerDay upserts a daily bucket whose count is capped at one per
// (subject, day), with duration still accumulating. It exists so the view bucket
// can mirror BumpPageView's window rule without reading the page-view row back:
// BumpPageView dedups on the local calendar day, so its row counts at most once
// per local day — which is exactly what capping the bucket's own count at one per
// day expresses, evaluated atomically against the bucket's own row.
func (r *masteryRepository) bumpDailyOncePerDay(
	ctx context.Context, scope interfaces.MemoryScope, kbID, subject, eventType string, duration int64,
) error {
	if subject == "" || eventType == "" {
		return nil
	}
	now := time.Now()
	row := &types.MemoryMasteryDaily{
		ID:              uuid.New().String(),
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		KnowledgeBaseID: kbID,
		Slug:            subject,
		EventType:       eventType,
		EventDate:       now.Format("2006-01-02"),
		EventCount:      1,
		DurationSum:     duration,
	}
	updates := map[string]interface{}{
		"event_count": gorm.Expr(
			"memory_mastery_daily.event_count + CASE WHEN memory_mastery_daily.event_count >= 1 THEN 0 ELSE 1 END"),
		"updated_at": now,
	}
	if duration > 0 {
		updates["duration_sum"] = gorm.Expr("memory_mastery_daily.duration_sum + ?", duration)
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "knowledge_base_id"},
				{Name: "slug"}, {Name: "event_type"}, {Name: "event_date"},
			},
			DoUpdates: clause.Assignments(updates),
		}).
		Create(row).Error
}

// RecordAnswerLike upserts a like bound to a message, storing its allocation
// snapshot. Re-liking the same message overwrites the snapshot and clears the
// cancellation.
func (r *masteryRepository) RecordAnswerLike(
	ctx context.Context, scope interfaces.MemoryScope, like *types.MemoryAnswerLike,
) error {
	like.ID = uuid.New().String()
	like.TenantID = scope.TenantID
	like.SubjectID = scope.SubjectID
	like.LikedAt = time.Now()
	like.CancelledAt = nil
	if err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "message_id"},
			},
			DoUpdates: clause.AssignmentColumns([]string{"allocations", "liked_at", "cancelled_at"}),
		}).
		Create(like).Error; err != nil {
		return err
	}
	// 日聚合：like 事件按来源文档 knowledge_id 记账，补齐时间切分回放的
	// 点赞维度。knowledge_id 全局唯一，KB 空也不与其它 KB 冲突；若分配携带
	// KB 则一并记录。
	for _, alloc := range like.Allocations {
		if err := r.bumpDaily(ctx, scope, alloc.KnowledgeBaseID, alloc.KnowledgeID, types.MasteryEventLike, 1, 0); err != nil {
			return err
		}
	}
	return nil
}

// CancelAnswerLike marks a like cancelled, so its allocations stop counting.
func (r *masteryRepository) CancelAnswerLike(
	ctx context.Context, scope interfaces.MemoryScope, messageID string,
) error {
	now := time.Now()
	return r.scoped(ctx, scope).
		Model(&types.MemoryAnswerLike{}).
		Where("message_id = ? AND cancelled_at IS NULL", messageID).
		Updates(map[string]interface{}{"cancelled_at": now}).Error
}

// RecordExposure writes one guidance exposure row.
func (r *masteryRepository) RecordExposure(
	ctx context.Context, scope interfaces.MemoryScope, exp *types.MemoryGuideExposure,
) error {
	return r.RecordExposureBatch(ctx, scope, []*types.MemoryGuideExposure{exp})
}

// RecordExposureBatch writes a ripple's exposures in one statement. Scope and id
// are stamped here; ShownAt is left as the caller set it, so every candidate of one
// ripple carries the same instant.
func (r *masteryRepository) RecordExposureBatch(
	ctx context.Context, scope interfaces.MemoryScope, exps []*types.MemoryGuideExposure,
) error {
	if len(exps) == 0 {
		return nil
	}
	for _, exp := range exps {
		exp.ID = uuid.New().String()
		exp.TenantID = scope.TenantID
		exp.SubjectID = scope.SubjectID
	}
	return r.db.WithContext(ctx).CreateInBatches(exps, masteryWriteBatchSize).Error
}

// ListExposedToday returns the candidate slugs already exposed today for one
// (kb, trigger) pair, in a single read.
//
// The caller filters a whole ripple against it. It replaced a per-candidate "has
// this one been shown today?" count, which turned one node click into a round trip
// per candidate — and a ripple is a dozen candidates.
//
// The window is the local calendar day, matching the daily bucket's event_date and
// the page-view fold: time.Truncate(24h) cuts at UTC boundaries instead, i.e. 08:00
// in UTC+8.
func (r *masteryRepository) ListExposedToday(
	ctx context.Context, scope interfaces.MemoryScope, kbID, triggerSlug string,
) (map[string]struct{}, error) {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var slugs []string
	if err := r.scoped(ctx, scope).
		Model(&types.MemoryGuideExposure{}).
		Where("knowledge_base_id = ? AND trigger_slug = ? AND shown_at >= ?",
			kbID, triggerSlug, dayStart).
		Distinct().
		Pluck("candidate_slug", &slugs).Error; err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(slugs))
	for _, s := range slugs {
		out[s] = struct{}{}
	}
	return out, nil
}

// MarkExposureClicked marks the most recent unclicked exposure for a candidate
// in a KB as clicked (the candidate the user actually followed up on).
func (r *masteryRepository) MarkExposureClicked(
	ctx context.Context, scope interfaces.MemoryScope, kbID, candidateSlug string,
) error {
	now := time.Now()
	return r.scoped(ctx, scope).
		Model(&types.MemoryGuideExposure{}).
		Where("knowledge_base_id = ? AND candidate_slug = ? AND clicked_at IS NULL", kbID, candidateSlug).
		Order("shown_at DESC").
		Limit(1).
		Updates(map[string]interface{}{"clicked_at": now}).Error
}

// MarkExposureQualified marks the most recent unqualified exposure for a
// candidate in a KB as qualified: the candidate page reached the effective-view
// threshold, which is the reward signal for offline evaluation and strategy
// learning. Idempotent — only exposures without a qualified_view_at are touched.
func (r *masteryRepository) MarkExposureQualified(
	ctx context.Context, scope interfaces.MemoryScope, kbID, slug string,
) error {
	now := time.Now()
	return r.scoped(ctx, scope).
		Model(&types.MemoryGuideExposure{}).
		Where("knowledge_base_id = ? AND candidate_slug = ? AND qualified_view_at IS NULL", kbID, slug).
		Order("shown_at DESC").
		Limit(1).
		Updates(map[string]interface{}{"qualified_view_at": now}).Error
}

func (r *masteryRepository) ListCitations(
	ctx context.Context, scope interfaces.MemoryScope, kbID string,
) ([]*types.MemoryCitation, error) {
	var rows []*types.MemoryCitation
	err := r.scoped(ctx, scope).
		Where("knowledge_base_id = ?", kbID).
		Find(&rows).Error
	return rows, err
}

func (r *masteryRepository) ListPageViews(
	ctx context.Context, scope interfaces.MemoryScope, kbID string,
) ([]*types.MemoryPageView, error) {
	var rows []*types.MemoryPageView
	err := r.scoped(ctx, scope).
		Where("knowledge_base_id = ?", kbID).
		Find(&rows).Error
	return rows, err
}

// dailyLedger describes one per-day ledger read: which table, which extra filter
// (the daily table is keyed by event type, the spread table is single-purpose),
// and which columns carry the folded count and the seconds.
//
// It exists so the two daily reads share one query and one folding rule instead of
// two copies kept in sync by a comment promising they stay "shaped exactly like"
// each other.
type dailyLedger struct {
	model         interface{}
	filter        string
	filterArgs    []interface{}
	countColumn   string
	secondsColumn string
}

var (
	viewDailyLedger = dailyLedger{
		model:         &types.MemoryMasteryDaily{},
		filter:        "event_type = ?",
		filterArgs:    []interface{}{types.MasteryEventView},
		countColumn:   "event_count",
		secondsColumn: "duration_sum",
	}
	// Spread carries time only, so it contributes no count: SUM(0) keeps the read
	// shaped like the view read without inventing a column for it.
	spreadDailyLedger = dailyLedger{
		model:         &types.MemorySpreadView{},
		countColumn:   "0",
		secondsColumn: "spread_seconds",
	}
)

// listDailyBuckets returns the buckets of one KB, split at cutoff:
//
//	recent — one row per (slug, day) for days on or after cutoff, oldest first,
//	         so every recent day can pay its own decay;
//	cold   — one row per slug summing every day before cutoff.
//
// The split keeps the row count bounded (visited pages, instead of visited pages
// × days). Splitting at the decay floor's horizon makes it exact rather than an
// approximation: every cold bucket already decays at exactly DecayFloor, so one
// summed row per slug scores identically to the individual days. Cold rows come
// back with an empty EventDate — that is how the caller knows to stamp them with
// the cutoff day.
//
// An empty slug reads the whole KB (the graph overlay needs every page); a set slug
// narrows to one page, which is all the page-view echo needs. Same fold, same shape
// either way, so the two callers cannot drift apart.
func (r *masteryRepository) listDailyBuckets(
	ctx context.Context, scope interfaces.MemoryScope, kbID string, ledger dailyLedger, cutoff time.Time, slug string,
) (recent, cold []types.MasteryDailyBucket, err error) {
	cutoffDate := cutoff.Format("2006-01-02")
	// A fresh chain per query: reusing one *gorm.DB would let the first statement's
	// WHERE clauses leak into the second.
	scoped := func() *gorm.DB {
		q := r.scoped(ctx, scope).Where("knowledge_base_id = ?", kbID)
		if ledger.filter != "" {
			q = q.Where(ledger.filter, ledger.filterArgs...)
		}
		if slug != "" {
			q = q.Where("slug = ?", slug)
		}
		return q
	}
	if err = scoped().Model(ledger.model).
		Select("slug, event_date, "+ledger.countColumn+" AS count, "+
			ledger.secondsColumn+" AS seconds").
		Where("event_date >= ?", cutoffDate).
		Order("event_date ASC").
		Find(&recent).Error; err != nil {
		return nil, nil, err
	}
	if err = scoped().Model(ledger.model).
		Select("slug, SUM("+ledger.countColumn+") AS count, SUM("+
			ledger.secondsColumn+") AS seconds").
		Where("event_date < ?", cutoffDate).
		Group("slug").
		Order("slug ASC").
		Find(&cold).Error; err != nil {
		return nil, nil, err
	}
	// 归一日期渲染，理由见 normalizeBucketDay。
	for i := range recent {
		recent[i].EventDate = normalizeBucketDay(recent[i].EventDate)
	}
	for i := range cold {
		cold[i].EventDate = normalizeBucketDay(cold[i].EventDate)
	}
	return recent, cold, nil
}

// normalizeBucketDay trims a bucket's date to its calendar-day part.
//
// event_date is a real DATE in PostgreSQL but a text column in SQLite, and the two
// drivers hand the value back differently: a PG date scanned into a Go string
// arrives as the RFC3339 rendering ("2026-09-10T00:00:00Z"), while SQLite returns
// exactly what was written ("2026-09-10"). Callers parse the day and want the day
// *as stored* — never converted into another zone — so the trimming happens here
// instead of in every caller.
//
// This is not cosmetic. Before it existed, the aggregation's date parse failed on
// every PostgreSQL bucket, and a failed parse skips the bucket: neighbour warmth
// silently went to zero (it has no lumped fallback at all) while view slicing
// quietly fell back to the lumped row — on SQLite everything looked green.
func normalizeBucketDay(raw string) string {
	if len(raw) > 10 && (raw[10] == 'T' || raw[10] == ' ') {
		return raw[:10]
	}
	return raw
}

// ListDailyViews returns the view buckets of one KB, split at cutoff. See
// listDailyBuckets for the split's shape and why the fold is exact.
func (r *masteryRepository) ListDailyViews(
	ctx context.Context, scope interfaces.MemoryScope, kbID string, cutoff time.Time,
) (recent, cold []types.MasteryDailyBucket, err error) {
	return r.listDailyBuckets(ctx, scope, kbID, viewDailyLedger, cutoff, "")
}

// spreadWriteBatchSize bounds how many neighbour rows go into one statement. It is
// a safety valve for pathological pages, not the normal path: pages in the dev KB
// average ~8 links and peak just under 60, i.e. one statement.
const spreadWriteBatchSize = 128

// BumpSpreadViews credits the pages adjacent to a viewed page with the reader's
// time on it.
//
// Only time spreads: the caller passes the clamped view duration, so a
// few-second glance credits a few seconds (worth nothing at scoring time) while a
// real read warms its neighbours a little. Self is skipped, and duplicates are
// collapsed so a page that is both an in-link and an out-link is credited once.
//
// All neighbours go in one multi-row upsert. This runs on the page-view request
// path, and a page links dozens of neighbours: written per row, one effective view
// cost as many round trips as the page has links (and a failure mid-loop left half
// the neighbours credited, with no transaction to roll it back). The deduplication
// above is load-bearing for that batching: PostgreSQL rejects a statement that
// touches one conflict key twice ("ON CONFLICT DO UPDATE command cannot affect row
// a second time"), so the collapse has to happen in Go before the insert.
func (r *masteryRepository) BumpSpreadViews(
	ctx context.Context, scope interfaces.MemoryScope, kbID, sourceSlug string, neighbors []string, seconds int64,
) error {
	if kbID == "" || sourceSlug == "" || seconds <= 0 || len(neighbors) == 0 {
		return nil
	}
	now := time.Now()
	date := now.Format("2006-01-02")
	rows := make([]*types.MemorySpreadView, 0, len(neighbors))
	seen := make(map[string]struct{}, len(neighbors))
	for _, slug := range neighbors {
		if slug == "" || slug == sourceSlug {
			continue
		}
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		rows = append(rows, &types.MemorySpreadView{
			ID:              uuid.New().String(),
			TenantID:        scope.TenantID,
			SubjectID:       scope.SubjectID,
			KnowledgeBaseID: kbID,
			Slug:            slug,
			EventDate:       date,
			SpreadSeconds:   seconds,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	// spread_seconds is qualified by table and by EXCLUDED for a reason: inside
	// ON CONFLICT DO UPDATE, a bare column name is ambiguous between the target
	// row and the proposed one, and PostgreSQL refuses it outright.
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "tenant_id"}, {Name: "subject_id"}, {Name: "knowledge_base_id"},
				{Name: "slug"}, {Name: "event_date"},
			},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"spread_seconds": gorm.Expr("memory_spread_views.spread_seconds + excluded.spread_seconds"),
				"updated_at":     now,
			}),
		}).
		CreateInBatches(rows, spreadWriteBatchSize).Error
}

// ListDailySpread returns the neighbour-spread buckets of one KB, split at cutoff
// under the same rule as ListDailyViews.
func (r *masteryRepository) ListDailySpread(
	ctx context.Context, scope interfaces.MemoryScope, kbID string, cutoff time.Time,
) (recent, cold []types.MasteryDailyBucket, err error) {
	return r.listDailyBuckets(ctx, scope, kbID, spreadDailyLedger, cutoff, "")
}

// LoadNodeLedger returns the ledger rows that can possibly affect one page's water
// level, and nothing else.
//
// Every read below is a point lookup inside the same (tenant, subject) scope the
// KB-wide reads use, with row counts bounded by one page instead of by the
// knowledge base: this slug's daily buckets, this slug's view row, and the
// citations of the documents this page is built from.
//
// The subject's like rows are the one exception, and they are still read whole: the
// like snapshot is keyed by message and its document scope lives inside the
// allocations JSON, so there is no column to narrow on. Filtering those is a schema
// question rather than a query one — and narrowing the other four is what removes
// the read that actually scales, since daily buckets grow with pages × days, not
// with the number of answers the subject liked.
func (r *masteryRepository) LoadNodeLedger(
	ctx context.Context, scope interfaces.MemoryScope, kbID, slug string,
	sourceKnowledgeIDs []string, cutoff time.Time,
) (*types.NodeLedger, error) {
	out := &types.NodeLedger{}
	if kbID == "" || slug == "" {
		return out, nil
	}

	// Source ids come from the wiki page; duplicates would only widen the IN list.
	ids := make([]string, 0, len(sourceKnowledgeIDs))
	seen := make(map[string]struct{}, len(sourceKnowledgeIDs))
	for _, id := range sourceKnowledgeIDs {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		if err := r.scoped(ctx, scope).
			Where("knowledge_base_id = ?", kbID).
			Where("knowledge_id IN ?", ids).
			Find(&out.Citations).Error; err != nil {
			return nil, err
		}
	}
	if err := r.scoped(ctx, scope).
		Where("cancelled_at IS NULL").
		Find(&out.Likes).Error; err != nil {
		return nil, err
	}

	var view types.MemoryPageView
	switch err := r.scoped(ctx, scope).
		Where("knowledge_base_id = ? AND slug = ?", kbID, slug).
		Take(&view).Error; {
	case err == nil:
		out.View = &view
	case errors.Is(err, gorm.ErrRecordNotFound):
		// Never viewed: Evidence leaves the view fields at zero, exactly as the
		// KB-wide read does when the page has no row.
	default:
		return nil, err
	}

	recentViews, coldViews, err := r.listDailyBuckets(ctx, scope, kbID, viewDailyLedger, cutoff, slug)
	if err != nil {
		return nil, err
	}
	recentSpread, coldSpread, err := r.listDailyBuckets(ctx, scope, kbID, spreadDailyLedger, cutoff, slug)
	if err != nil {
		return nil, err
	}
	// Cold rows keep their empty EventDate and are stamped with the cutoff day by
	// the caller, so merging the two reads loses nothing.
	out.ViewBuckets = append(recentViews, coldViews...)
	out.SpreadBuckets = append(recentSpread, coldSpread...)
	return out, nil
}

func (r *masteryRepository) ListActiveLikes(
	ctx context.Context, scope interfaces.MemoryScope,
) ([]*types.MemoryAnswerLike, error) {
	var rows []*types.MemoryAnswerLike
	err := r.scoped(ctx, scope).
		Where("cancelled_at IS NULL").
		Find(&rows).Error
	return rows, err
}

// DeleteAll drops every guidance ledger row in the scope. It deliberately does
// not touch memory_doc_affinity or any long-term memory table.
//
// All six deletes run in one transaction. This is a privacy guarantee, not a
// convenience: as separate statements a failure part-way through left some ledgers
// cleared and others untouched, and the user — who sees only an error — cannot tell
// which half survived. The transaction makes the outcome all-or-nothing, which is
// what "your profile is deleted" has to mean.
func (r *masteryRepository) DeleteAll(ctx context.Context, scope interfaces.MemoryScope) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range []interface{}{
			&types.MemorySpreadView{},
			&types.MemoryMasteryDaily{},
			&types.MemoryGuideExposure{},
			&types.MemoryAnswerLike{},
			&types.MemoryPageView{},
			&types.MemoryCitation{},
		} {
			if err := tx.WithContext(ctx).
				Where("tenant_id = ? AND subject_id = ?", scope.TenantID, scope.SubjectID).
				Delete(model).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

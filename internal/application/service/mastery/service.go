package mastery

import (
	"context"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/memory"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Service implements interfaces.MasteryService. It resolves the memory scope
// from the request context (never from a client id) and delegates storage to the
// ledger repository, keeping the water-level computation a pure function.
type Service struct {
	repo   interfaces.MasteryRepository
	config Config
}

// NewMasteryService creates the knowledge-guidance service.
func NewMasteryService(repo interfaces.MasteryRepository) interfaces.MasteryService {
	return &Service{repo: repo, config: DefaultConfig()}
}

func (s *Service) RecordCitations(ctx context.Context, refs []types.MemoryDocAffinity) {
	if len(refs) == 0 {
		return
	}
	scope, err := memory.ResolveScope(ctx)
	if err != nil {
		return
	}
	if err := s.repo.BumpCitation(ctx, scope, refs); err != nil {
		logger.Warnf(ctx, "mastery: record citations failed: %v", err)
	}
}

func (s *Service) RecordPageView(ctx context.Context, kbID, slug string, duration int64, neighbors []string) {
	if slug == "" || duration <= 0 {
		return
	}
	// 服务端也钳制单次时长，防止绕过 handler 的调用路径刷高水位。
	if duration > types.MasteryMaxPageViewSeconds {
		duration = types.MasteryMaxPageViewSeconds
	}
	scope, err := memory.ResolveScope(ctx)
	if err != nil {
		return
	}
	if err := s.repo.BumpPageView(ctx, scope, kbID, slug, duration); err != nil {
		logger.Warnf(ctx, "mastery: record page view failed: %v", err)
		return
	}
	// 一跳邻居的「上下文预热」：按本次阅读时间折算，扫一眼几乎不产生分数。
	// 放在浏览写入成功之后，避免主证据写失败却留下扩散痕迹。
	if err := s.repo.BumpSpreadViews(ctx, scope, kbID, slug, neighbors, duration); err != nil {
		logger.Warnf(ctx, "mastery: record neighbour spread failed: %v", err)
	}
	// 有效浏览既是状态信号，也是曝光回报信号：若该 slug 曾作为边界候选被
	// 展示/点击，则回填 qualified_view_at，闭环「展示→点击→有效浏览」。
	if err := s.repo.MarkExposureQualified(ctx, scope, kbID, slug); err != nil {
		logger.Warnf(ctx, "mastery: mark exposure qualified failed: %v", err)
	}
}

func (s *Service) RecordAnswerLike(ctx context.Context, messageID string, refs []types.MemoryDocAffinity) {
	if messageID == "" {
		return
	}
	allocations := AllocateLikes(refs)
	if len(allocations) == 0 {
		return
	}
	scope, err := memory.ResolveScope(ctx)
	if err != nil {
		return
	}
	like := &types.MemoryAnswerLike{MessageID: messageID, Allocations: allocations}
	if err := s.repo.RecordAnswerLike(ctx, scope, like); err != nil {
		logger.Warnf(ctx, "mastery: record answer like failed: %v", err)
	}
}

func (s *Service) CancelAnswerLike(ctx context.Context, messageID string) {
	scope, err := memory.ResolveScope(ctx)
	if err != nil {
		return
	}
	if err := s.repo.CancelAnswerLike(ctx, scope, messageID); err != nil {
		logger.Warnf(ctx, "mastery: cancel answer like failed: %v", err)
	}
}

func (s *Service) RecordExposure(ctx context.Context, exp *types.MemoryGuideExposure) {
	if exp == nil {
		return
	}
	scope, err := memory.ResolveScope(ctx)
	if err != nil {
		return
	}
	if err := s.repo.RecordExposure(ctx, scope, exp); err != nil {
		logger.Warnf(ctx, "mastery: record exposure failed: %v", err)
	}
}

// coldCutoff returns the bucket cutoff: days before it are folded per slug, and a
// degenerate configuration (no decay floor) yields the epoch, i.e. "fold nothing".
func (s *Service) coldCutoff() time.Time {
	if horizon := s.config.ColdHorizon(); horizon > 0 {
		return time.Now().Add(-horizon)
	}
	return time.Unix(0, 0)
}

// mergeBuckets concatenates a split daily read back into one list. Cold rows keep
// their empty EventDate, which is how the parser recognises them.
func mergeBuckets(recent, cold []types.MasteryDailyBucket) []types.MasteryDailyBucket {
	if len(cold) == 0 {
		return recent
	}
	out := make([]types.MasteryDailyBucket, 0, len(recent)+len(cold))
	out = append(out, recent...)
	out = append(out, cold...)
	return out
}

// dailySlices parses folded daily reads into per-day slices, returning the number of
// buckets it had to skip together with the first offending date. View and spread
// evidence share this one parse point so the two ledgers cannot drift apart in shape
// or folding rule.
//
// A bucket without a date of its own is a folded (cold) row: it is stamped with the
// cutoff day, which is exact rather than approximate because every day it covers
// already decays at DecayFloor.
//
// A bucket whose date cannot be parsed is skipped rather than failing the read, but
// it is counted so the caller can warn once instead of per bucket. This is not
// harmless dirty data: skipping a bucket silently zeroes neighbour warmth (which has
// no lumped fallback at all) and quietly drops view slicing back to the lumped row.
// The repository is responsible for normalizing the dialect rendering to YYYY-MM-DD.
func dailySlices(
	viewBuckets, spreadBuckets []types.MasteryDailyBucket, cutoff time.Time,
) (
	viewSlices map[string][]ViewSlice,
	spreadSlices map[string][]SpreadSlice,
	badDates int,
	firstBadDate string,
) {
	// view 证据按天切片，供 Level 逐日衰减（理由见 ViewSlice 的类型注释）。
	// 日桶由 BumpPageView 在每次浏览时写入，次数与时长与 memory_page_views 同口径。
	viewSlices = make(map[string][]ViewSlice)
	spreadSlices = make(map[string][]SpreadSlice)
	appendView := func(b types.MasteryDailyBucket, day time.Time) {
		viewSlices[b.Slug] = append(viewSlices[b.Slug], ViewSlice{
			Day:      day,
			Views:    b.Count,
			Duration: b.Seconds,
		})
	}
	appendSpread := func(b types.MasteryDailyBucket, day time.Time) {
		spreadSlices[b.Slug] = append(spreadSlices[b.Slug], SpreadSlice{Day: day, Seconds: b.Seconds})
	}
	forEachBucket := func(
		buckets []types.MasteryDailyBucket, onEach func(b types.MasteryDailyBucket, day time.Time),
	) {
		for _, b := range buckets {
			day := cutoff
			if b.EventDate != "" {
				parsed, perr := time.ParseInLocation("2006-01-02", b.EventDate, time.Local)
				if perr != nil {
					badDates++
					if firstBadDate == "" {
						firstBadDate = b.EventDate
					}
					continue
				}
				day = parsed
			}
			onEach(b, day)
		}
	}
	forEachBucket(viewBuckets, appendView)
	forEachBucket(spreadBuckets, appendSpread)
	return viewSlices, spreadSlices, badDates, firstBadDate
}

// warnBadBucketDates reports skipped buckets once per aggregation. Per-bucket
// warnings would drown the log, and this is precisely the signal that the ledger's
// date format changed.
func warnBadBucketDates(ctx context.Context, badDates int, firstBadDate string) {
	if badDates == 0 {
		return
	}
	logger.Warnf(ctx, "mastery: skipped %d daily bucket(s) whose date could not be parsed "+
		"(first %q); bucket dates must arrive normalized to YYYY-MM-DD", badDates, firstBadDate)
}

// projectEvidence builds the per-slug evidence from already-loaded ledger rows.
//
// It is shared by the KB-wide aggregation (the graph overlay) and the single-node
// one (the page-view echo), and that sharing is the point: the echo exists to return
// the same number the overlay shows, so the two must not be able to disagree about
// how a signal becomes evidence.
func projectEvidence(
	slugSources map[string][]string,
	citations []*types.MemoryCitation,
	likes []*types.MemoryAnswerLike,
	views []*types.MemoryPageView,
	viewSlices map[string][]ViewSlice,
	spreadSlices map[string][]SpreadSlice,
) map[string]Evidence {
	citeByDoc := make(map[string]*types.MemoryCitation, len(citations))
	for _, c := range citations {
		citeByDoc[c.KnowledgeID] = c
	}

	type likeAgg struct {
		weight float64
		last   time.Time
	}
	likeByDoc := make(map[string]*likeAgg)
	for _, l := range likes {
		for _, alloc := range l.Allocations {
			agg := likeByDoc[alloc.KnowledgeID]
			if agg == nil {
				agg = &likeAgg{}
				likeByDoc[alloc.KnowledgeID] = agg
			}
			agg.weight += alloc.CreditedWeight
			if l.LikedAt.After(agg.last) {
				agg.last = l.LikedAt
			}
		}
	}

	viewBySlug := make(map[string]*types.MemoryPageView, len(views))
	for _, v := range views {
		viewBySlug[v.Slug] = v
	}

	result := make(map[string]Evidence, len(slugSources))
	for slug, docs := range slugSources {
		e := Evidence{}
		// Citation: take the strongest source doc. Summary pages map 1:1 to a
		// document; a multi-source page (entity/concept) inherits the strongest
		// source's citation strength, which keeps the projection conservative —
		// one heavily cited source never inflates a page the user barely touched.
		for _, d := range docs {
			if c, ok := citeByDoc[d]; ok {
				if c.CiteCount > e.Citations {
					e.Citations = c.CiteCount
				}
				if c.LastCitedAt.After(e.LastCitedAt) {
					e.LastCitedAt = c.LastCitedAt
				}
			}
			if agg, ok := likeByDoc[d]; ok {
				e.Likes += agg.weight
				if agg.last.After(e.LastLikedAt) {
					e.LastLikedAt = agg.last
				}
			}
		}
		if v, ok := viewBySlug[slug]; ok {
			e.Views = v.ViewCount
			e.Duration = v.TotalDuration
			e.LastViewedAt = v.LastViewAt
		}
		// nil 时 Level 回落到汇总口径（历史数据 / 无日桶的库）。
		e.ViewSlices = viewSlices[slug]
		e.SpreadSlices = spreadSlices[slug]
		result[slug] = e
	}
	return result
}

// aggregate loads the ledgers of a whole knowledge base and projects them onto
// pages, returning the evidence breakdown per slug. Citation and like evidence are
// recorded by knowledge id, so they are projected onto pages via slugSources (from
// WikiPage.SourceRefs); view evidence is recorded directly by slug.
//
// This is the graph overlay's read: it needs every page of the KB. The page-view
// echo must not use it — see nodeEvidence.
func (s *Service) aggregate(
	ctx context.Context, kbID string, slugSources map[string][]string,
) (map[string]Evidence, error) {
	scope, err := memory.ResolveScope(ctx)
	if err != nil {
		return map[string]Evidence{}, nil
	}
	// 没有节点可投影时，证据一定为空：先返回，省掉四次账本查询。
	if len(slugSources) == 0 {
		return map[string]Evidence{}, nil
	}

	citations, err := s.repo.ListCitations(ctx, scope, kbID)
	if err != nil {
		return nil, err
	}
	views, err := s.repo.ListPageViews(ctx, scope, kbID)
	if err != nil {
		return nil, err
	}
	likes, err := s.repo.ListActiveLikes(ctx, scope)
	if err != nil {
		return nil, err
	}
	cutoff := s.coldCutoff()
	recentViews, coldViews, err := s.repo.ListDailyViews(ctx, scope, kbID, cutoff)
	if err != nil {
		return nil, err
	}
	recentSpread, coldSpread, err := s.repo.ListDailySpread(ctx, scope, kbID, cutoff)
	if err != nil {
		return nil, err
	}

	viewSlices, spreadSlices, badDates, firstBadDate := dailySlices(
		mergeBuckets(recentViews, coldViews), mergeBuckets(recentSpread, coldSpread), cutoff)
	warnBadBucketDates(ctx, badDates, firstBadDate)

	return projectEvidence(slugSources, citations, likes, views, viewSlices, spreadSlices), nil
}

// nodeEvidence loads just one page's ledgers and projects them into Evidence.
//
// The page-view endpoint echoes this page's fresh level after every effective view.
// Reusing aggregate for that meant reading the whole knowledge base — every daily
// bucket in it, a set that grows with pages × days — to answer about one node, so
// the cost of reading a single page grew with the KB and with the subject's history.
// Every read behind LoadNodeLedger is a point lookup bounded by this page.
func (s *Service) nodeEvidence(
	ctx context.Context, kbID, slug string, sourceKnowledgeIDs []string,
) (Evidence, error) {
	scope, err := memory.ResolveScope(ctx)
	if err != nil || slug == "" {
		return Evidence{}, nil
	}
	cutoff := s.coldCutoff()
	ledger, err := s.repo.LoadNodeLedger(ctx, scope, kbID, slug, sourceKnowledgeIDs, cutoff)
	if err != nil {
		return Evidence{}, err
	}
	var views []*types.MemoryPageView
	if ledger.View != nil {
		views = []*types.MemoryPageView{ledger.View}
	}
	viewSlices, spreadSlices, badDates, firstBadDate := dailySlices(
		ledger.ViewBuckets, ledger.SpreadBuckets, cutoff)
	warnBadBucketDates(ctx, badDates, firstBadDate)

	return projectEvidence(
		map[string][]string{slug: sourceKnowledgeIDs},
		ledger.Citations, ledger.Likes, views, viewSlices, spreadSlices,
	)[slug], nil
}

// NodeMastery computes the water level per slug.
func (s *Service) NodeMastery(
	ctx context.Context, kbID string, slugSources map[string][]string,
) (map[string]int, error) {
	states, err := s.NodeStates(ctx, kbID, slugSources)
	if err != nil {
		return nil, err
	}
	result := make(map[string]int, len(states))
	for slug, st := range states {
		result[slug] = st.Level
	}
	return result, nil
}

// NodeStates computes the full per-node state: water level plus last-active
// time and a server-computed recency flag. RecentlyActive uses the freshness
// window (visual-only), independent of the half-life decay that drives the
// water level itself.
func (s *Service) NodeStates(
	ctx context.Context, kbID string, slugSources map[string][]string,
) (map[string]types.MasteryNodeState, error) {
	evidence, err := s.aggregate(ctx, kbID, slugSources)
	if err != nil {
		return nil, err
	}
	return s.statesFromEvidence(evidence, time.Now()), nil
}

// NodeStateForSlug returns the state of a single page. It shares the projection and
// the level mapping with NodeStates, so the value echoed back after a page view
// matches the graph overlay exactly — without reading the whole knowledge base to
// compute it.
func (s *Service) NodeStateForSlug(
	ctx context.Context, kbID, slug string, sourceKnowledgeIDs []string,
) (types.MasteryNodeState, error) {
	e, err := s.nodeEvidence(ctx, kbID, slug, sourceKnowledgeIDs)
	if err != nil {
		return types.MasteryNodeState{}, err
	}
	return stateFromEvidence(s.config, e, time.Now()), nil
}

// stateFromEvidence derives one node's state. The multi-node and single-node reads
// share it so neither can map evidence onto a level differently from the other.
func stateFromEvidence(cfg Config, e Evidence, now time.Time) types.MasteryNodeState {
	lastActive := e.LastActive()
	return types.MasteryNodeState{
		Level:          Level(cfg, e, now),
		LastActive:     lastActive,
		RecentlyActive: !lastActive.IsZero() && now.Sub(lastActive) <= time.Duration(cfg.FreshnessDays*24)*time.Hour,
	}
}

func (s *Service) statesFromEvidence(
	evidence map[string]Evidence, now time.Time,
) map[string]types.MasteryNodeState {
	result := make(map[string]types.MasteryNodeState, len(evidence))
	for slug, e := range evidence {
		result[slug] = stateFromEvidence(s.config, e, now)
	}
	return result
}

// Profile returns the evidence breakdown per node, sorted by level desc, so a
// user can inspect exactly which signals formed each node's water level.
func (s *Service) Profile(
	ctx context.Context, kbID string, slugSources map[string][]string,
) ([]types.MasteryNodeDetail, error) {
	evidence, err := s.aggregate(ctx, kbID, slugSources)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]types.MasteryNodeDetail, 0, len(evidence))
	for slug, e := range evidence {
		level := Level(s.config, e, now)
		out = append(out, types.MasteryNodeDetail{
			Slug:       slug,
			Level:      level,
			Tier:       types.MasteryTierLabel(level),
			Citations:  e.Citations,
			Views:      e.Views,
			Duration:   e.Duration,
			Likes:      e.Likes,
			Spread:     e.decayedSpreadScore(s.config, now),
			LastActive: e.LastActive(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Level != out[j].Level {
			return out[i].Level > out[j].Level
		}
		return out[i].Slug < out[j].Slug
	})
	return out, nil
}

// RecordExposures records the local ripple candidates shown when the user
// clicks a center node. Each candidate carries rank = index + 1 so the
// click-through evaluation can answer "which position did the user follow".
// Deduplicated per (kb, trigger, candidate, day).
//
// One read and one write, for the whole ripple. It used to ask "has this candidate
// been shown today?" once per candidate and insert each one separately, so a node
// click cost two round trips per candidate. A ripple is the drawer's shortlist —
// at most three candidates, see the frontend's computeRecommendations — so that was
// up to six round trips for one click.
//
// The read now decides whether the whole ripple is recorded: if it fails, the ripple
// is dropped rather than written in part. A partial write is not *detectably*
// partial — the ranks it did record stay self-consistent, so a consumer cannot tell
// it from a legitimately shorter shortlist, and the distortion is silent. Recording
// nothing lands in the "no exposure today" state the consumer already handles. Both
// outcomes bias the click-through rate; only one of them is indistinguishable from
// real data. (Nothing consumes this log yet — see §6.4 — so the choice currently
// affects the quality of the future replay, not any live metric.)
func (s *Service) RecordExposures(ctx context.Context, kbID, triggerSlug string, candidates []string) {
	if kbID == "" || len(candidates) == 0 {
		return
	}
	scope, err := memory.ResolveScope(ctx)
	if err != nil {
		return
	}
	shown, err := s.repo.ListExposedToday(ctx, scope, kbID, triggerSlug)
	if err != nil {
		logger.Warnf(ctx, "mastery: list today's exposures failed: %v", err)
		return
	}
	// 一次涟漪共用一个时间戳：同批展示的候选在评估里应当属于同一时刻。
	shownAt := time.Now()
	batch := make([]*types.MemoryGuideExposure, 0, len(candidates))
	// 候选自身也要去重：首次出现的位置保留了更好的位次，而批量插入不应把同一个
	// 候选写两遍。这与按天去重叠加后，与逐条写入的旧行为一致。
	seen := make(map[string]struct{}, len(candidates))
	for i, slug := range candidates {
		if slug == "" {
			continue
		}
		if _, dup := shown[slug]; dup {
			continue
		}
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		batch = append(batch, &types.MemoryGuideExposure{
			KnowledgeBaseID: kbID,
			TriggerSlug:     triggerSlug,
			CandidateSlug:   slug,
			Strategy:        types.GuideStrategyPPRBoundary,
			Rank:            i + 1,
			ShownAt:         shownAt,
		})
	}
	if len(batch) == 0 {
		return
	}
	if err := s.repo.RecordExposureBatch(ctx, scope, batch); err != nil {
		logger.Warnf(ctx, "mastery: record exposures failed: %v", err)
	}
}

// MarkExposureClicked marks an exposure as clicked (the candidate the user
// actually followed up on).
func (s *Service) MarkExposureClicked(ctx context.Context, kbID, candidateSlug string) {
	if kbID == "" || candidateSlug == "" {
		return
	}
	scope, err := memory.ResolveScope(ctx)
	if err != nil {
		return
	}
	if err := s.repo.MarkExposureClicked(ctx, scope, kbID, candidateSlug); err != nil {
		logger.Warnf(ctx, "mastery: mark exposure clicked failed: %v", err)
	}
}

// MarkExposureQualified marks an exposure as qualified (the candidate page was
// effectively viewed). It is also invoked from RecordPageView, so a qualifying
// view of a previously shown boundary candidate closes the evaluation loop.
func (s *Service) MarkExposureQualified(ctx context.Context, kbID, slug string) {
	if kbID == "" || slug == "" {
		return
	}
	scope, err := memory.ResolveScope(ctx)
	if err != nil {
		return
	}
	if err := s.repo.MarkExposureQualified(ctx, scope, kbID, slug); err != nil {
		logger.Warnf(ctx, "mastery: mark exposure qualified failed: %v", err)
	}
}

// Boundary exposes the PPR frontier as a slug → score map for the graph
// endpoint. A non-zero score means the slug is on the knowledge boundary; the
// score itself lets the frontend rank one-hop neighbors when producing the
// "next step" recommendations.
func (s *Service) Boundary(levels map[string]int, adjacency map[string][]string) map[string]float64 {
	nodes := ComputeBoundary(levels, adjacency, FamiliarLevel, DefaultBoundaryTopK)
	result := make(map[string]float64, len(nodes))
	for _, n := range nodes {
		result[n.Slug] = n.Score
	}
	return result
}

func (s *Service) DeleteAll(ctx context.Context) error {
	scope, err := memory.ResolveScope(ctx)
	if err != nil {
		return err
	}
	return s.repo.DeleteAll(ctx, scope)
}

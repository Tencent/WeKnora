package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDedupMergeRejectReason(t *testing.T) {
	cases := []struct {
		name        string
		src, dst    string
		candidates  map[string]bool
		wantAllowed bool
	}{
		{
			name:        "allowed: dst is a candidate for this item, same type prefix",
			src:         "entity/acme-corp",
			dst:         "entity/acme-corporation",
			candidates:  map[string]bool{"entity/acme-corporation": true},
			wantAllowed: true,
		},
		{
			name:        "rejected: dst similar to a DIFFERENT item (union hallucination)",
			src:         "entity/tencent-open",
			dst:         "entity/hiring-agent",
			candidates:  map[string]bool{"entity/tencent-ur": true}, // hiring-agent not here
			wantAllowed: false,
		},
		{
			name:        "rejected: dst is not a candidate at all (pure hallucination)",
			src:         "entity/hy3-preview",
			dst:         "entity/llm-cli-tool",
			candidates:  nil,
			wantAllowed: false,
		},
		{
			name:        "rejected: type mismatch even when dst is a candidate",
			src:         "entity/foo",
			dst:         "concept/foo",
			candidates:  map[string]bool{"concept/foo": true},
			wantAllowed: false,
		},
		{
			name:        "rejected: missing type prefix",
			src:         "foo",
			dst:         "entity/foo",
			candidates:  map[string]bool{"entity/foo": true},
			wantAllowed: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := dedupMergeRejectReason(tc.src, tc.dst, tc.candidates)
			gotAllowed := reason == ""
			if gotAllowed != tc.wantAllowed {
				t.Fatalf("dedupMergeRejectReason(%q, %q) allowed=%v (reason=%q), want allowed=%v",
					tc.src, tc.dst, gotAllowed, reason, tc.wantAllowed)
			}
		})
	}
}

// helper: build a minimal entity/concept WikiPage.
func makePage(slug, title, typ string, aliases ...string) *types.WikiPage {
	return &types.WikiPage{
		Slug:     slug,
		Title:    title,
		PageType: typ,
		Aliases:  types.StringArray(aliases),
	}
}

func pageSlugs(pages []*types.WikiPage) []string {
	out := make([]string, 0, len(pages))
	for _, p := range pages {
		out = append(out, p.Slug)
	}
	return out
}

func containsSlug(pages []*types.WikiPage, slug string) bool {
	for _, p := range pages {
		if p.Slug == slug {
			return true
		}
	}
	return false
}

// Regression for the observed hallucination in llm_debug/20260422_171316.675.log:
// deepseek-v3.2 merged concept/chengzhen-dengji-shiye-renyuan into
// concept/zhong-hua-you-xiu-chuan-tong-wen-hua despite zero character overlap.
// With the prefilter in place, the traditional-culture page should not even
// be offered to the LLM as a candidate for that new item.
func TestSelectDedupCandidatePages_FiltersUnrelatedHallucinationTarget(t *testing.T) {
	newItems := []extractedItem{
		{Slug: "concept/chengzhen-dengji-shiye-renyuan", Name: "城镇登记失业人员", Aliases: []string{"登记失业人员"}},
		{Slug: "entity/beijing-nongshang-yinxing", Name: "北京农商银行"},
	}

	// Build a corpus that mirrors the real log's shape: ~90 unrelated pages
	// plus a few that share tokens with the new items (so the prefilter has
	// plausible near-matches to keep).
	pages := []*types.WikiPage{
		makePage("concept/zhong-hua-you-xiu-chuan-tong-wen-hua", "中华优秀传统文化", "concept"),
		makePage("concept/jiuye-jineng-peixun", "就业技能培训", "concept"),
		makePage("concept/peixun-kaohe-pingjia", "培训考核评价", "concept"),
		makePage("concept/ren-gong-zhi-neng-an-quan", "人工智能安全", "concept", "AI安全"),
		makePage("entity/bei-jing-shi-jiao-yu-wei-yuan-hui", "北京市教育委员会", "entity", "北京市教委"),
		makePage("entity/beijingshi-changping-zhiye-xuexiao", "北京市昌平职业学校", "entity"),
	}
	// Pad with filler so we exceed dedupSmallCorpusBypass.
	for i := 0; i < 40; i++ {
		pages = append(pages, makePage(
			"concept/filler-"+strings.Repeat("x", i+1),
			"填充概念"+strings.Repeat("占位", i+1),
			"concept",
		))
	}

	got := selectDedupCandidatePages(newItems, pages)

	if containsSlug(got, "concept/zhong-hua-you-xiu-chuan-tong-wen-hua") {
		t.Fatalf("expected unrelated page to be filtered out, but got it in candidates: %v",
			pageSlugs(got))
	}
	if len(got) >= len(pages) {
		t.Fatalf("expected prefilter to shrink the corpus (%d pages), but kept %d",
			len(pages), len(got))
	}
}

// A related page (shares tokens / characters with a new item) must survive
// the prefilter so the LLM can still evaluate the merge.
func TestSelectDedupCandidatePages_KeepsRelatedPages(t *testing.T) {
	newItems := []extractedItem{
		{Slug: "concept/chengzhen-dengji-shiye-renyuan", Name: "城镇登记失业人员", Aliases: []string{"登记失业人员"}},
	}
	// Build > dedupSmallCorpusBypass pages so the filter actually runs.
	pages := []*types.WikiPage{
		// Directly related: existing page whose title overlaps with the new
		// item on "登记失业人员". Prefilter MUST keep this.
		makePage("concept/deng-ji-shi-ye-ren-yuan", "登记失业人员", "concept", "城镇登记失业人员"),
		// Unrelated.
		makePage("concept/zhong-hua-you-xiu-chuan-tong-wen-hua", "中华优秀传统文化", "concept"),
	}
	for i := 0; i < 30; i++ {
		pages = append(pages, makePage(
			"entity/filler-"+strings.Repeat("x", i+1),
			"填充实体"+strings.Repeat("占位", i+1),
			"entity",
		))
	}

	got := selectDedupCandidatePages(newItems, pages)

	if !containsSlug(got, "concept/deng-ji-shi-ye-ren-yuan") {
		t.Fatalf("expected strongly-related page to be kept, got: %v", pageSlugs(got))
	}
}

// On small corpora the filter should be a no-op (minus page-type filtering):
// passing every page through is cheap and avoids cutting legitimate matches
// when the prompt is already small.
func TestSelectDedupCandidatePages_SmallCorpusBypass(t *testing.T) {
	newItems := []extractedItem{
		{Slug: "concept/a", Name: "A"},
	}
	pages := []*types.WikiPage{
		makePage("concept/wholly-unrelated-1", "毫不相关一", "concept"),
		makePage("concept/wholly-unrelated-2", "毫不相关二", "concept"),
		makePage("concept/wholly-unrelated-3", "毫不相关三", "concept"),
	}
	got := selectDedupCandidatePages(newItems, pages)
	if len(got) != len(pages) {
		t.Fatalf("expected bypass on small corpus: got %d, want %d", len(got), len(pages))
	}
}

// Non-entity/concept pages (summaries, comparisons, …) must be stripped regardless
// of corpus size — they are never valid merge targets.
func TestSelectDedupCandidatePages_DropsNonEntityConcept(t *testing.T) {
	newItems := []extractedItem{
		{Slug: "concept/foo", Name: "Foo"},
	}
	pages := []*types.WikiPage{
		makePage("summary/some-doc", "Some Doc Summary", types.WikiPageTypeSummary),
		makePage("comparison/foo-vs-bar", "Foo vs Bar", types.WikiPageTypeComparison),
		makePage("concept/foo-related", "Foo Related", types.WikiPageTypeConcept),
	}
	got := selectDedupCandidatePages(newItems, pages)
	for _, p := range got {
		if p.PageType != types.WikiPageTypeEntity && p.PageType != types.WikiPageTypeConcept {
			t.Fatalf("non-entity/concept page should have been filtered: %s (%s)",
				p.Slug, p.PageType)
		}
	}
}

// surfaceGrams must yield empty intersection for the real hallucinated pair,
// confirming the underlying similarity signal is doing its job.
func TestSurfaceGrams_UnrelatedCJKPair(t *testing.T) {
	a := surfaceGrams("城镇登记失业人员")
	b := surfaceGrams("中华优秀传统文化")
	for k := range a {
		if _, ok := b[k]; ok {
			t.Fatalf("expected zero bigram overlap, but shared %q", k)
		}
	}
}

// Latin abbreviation ↔ full-name pair must score highly (> floor) so the
// filter keeps legitimate merge candidates like "Acme Corp" ↔ "Acme Corporation".
func TestDedupPairScore_AcmeCorpVariant(t *testing.T) {
	a := dedupSurface{
		slugTokens:   slugBaseTokens("entity/acme-corp"),
		nameGramSets: gramsPerSurface([]string{"Acme Corp"}),
	}
	b := dedupSurface{
		slugTokens:   slugBaseTokens("entity/acme-corporation"),
		nameGramSets: gramsPerSurface([]string{"Acme Corporation"}),
	}
	score := dedupPairScore(a, b)
	if score < dedupCandidateScoreFloor {
		t.Fatalf("expected Acme Corp ↔ Corporation score above floor %v, got %v",
			dedupCandidateScoreFloor, score)
	}
}

// Unrelated CJK pair (the exact case observed in production) must score 0.
func TestDedupPairScore_UnrelatedCJKPair(t *testing.T) {
	a := dedupSurface{
		slugTokens:   slugBaseTokens("concept/chengzhen-dengji-shiye-renyuan"),
		nameGramSets: gramsPerSurface([]string{"城镇登记失业人员", "登记失业人员"}),
	}
	b := dedupSurface{
		slugTokens:   slugBaseTokens("concept/zhong-hua-you-xiu-chuan-tong-wen-hua"),
		nameGramSets: gramsPerSurface([]string{"中华优秀传统文化"}),
	}
	if s := dedupPairScore(a, b); s >= dedupCandidateScoreFloor {
		t.Fatalf("expected unrelated CJK pair to score below floor %v, got %v",
			dedupCandidateScoreFloor, s)
	}
}

func TestNormalizeWikiIdentityTitlePreservesSemanticPunctuation(t *testing.T) {
	if got := normalizeWikiIdentityTitle("  Acme  Corp "); got != "acmecorp" {
		t.Fatalf("normalizeWikiIdentityTitle whitespace/case = %q, want acmecorp", got)
	}
	if normalizeWikiIdentityTitle("寓言") == normalizeWikiIdentityTitle("《寓言》") {
		t.Fatal("concept title and work/chapter title must keep distinct identities")
	}
}

func TestExactIdentityTargetSameTypeOnly(t *testing.T) {
	item := extractedItem{Name: "孔子", Slug: "entity/kong-zi"}
	pages := map[string]*types.WikiPageLite{
		"entity/confucius": {
			Slug:     "entity/confucius",
			Title:    "孔 子",
			PageType: types.WikiPageTypeEntity,
		},
		"concept/confucius": {
			Slug:     "concept/confucius",
			Title:    "孔子",
			PageType: types.WikiPageTypeConcept,
		},
	}
	candidates := map[string]bool{
		"entity/confucius":  true,
		"concept/confucius": true,
	}
	if got := exactIdentityTarget(item, types.WikiPageTypeEntity, candidates, pages); got != "entity/confucius" {
		t.Fatalf("exactIdentityTarget = %q, want entity/confucius", got)
	}
}

func TestWikiIdentityClaimConvergesDifferentSlugs(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	svc := &wikiIngestService{redisClient: rdb}
	ctx := context.Background()

	proposals := []string{
		"entity/kong-zi",
		"entity/confucius",
		"entity/kongzi",
		"entity/kong-qiu",
	}
	results := make([]string, len(proposals))
	var wg sync.WaitGroup
	for i, proposal := range proposals {
		i, proposal := i, proposal
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = svc.claimWikiIdentitySlug(
				ctx, "kb-1", types.WikiPageTypeEntity, "孔 子", proposal, false, &WikiBatchContext{},
			)
		}()
	}
	wg.Wait()
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatalf("concurrent identity claims did not converge: %#v", results)
		}
	}

	// A verified existing page is authoritative and replaces a provisional
	// reservation left by an earlier map worker.
	authoritative := svc.claimWikiIdentitySlug(
		ctx, "kb-1", types.WikiPageTypeEntity, "孔子", "entity/confucius", true, &WikiBatchContext{},
	)
	third := svc.claimWikiIdentitySlug(
		ctx, "kb-1", types.WikiPageTypeEntity, "孔子", "entity/kongzi", false, &WikiBatchContext{},
	)
	if authoritative != "entity/confucius" || third != authoritative {
		t.Fatalf("authoritative identity did not replace claim: authoritative=%q third=%q", authoritative, third)
	}
}

func TestWikiIdentityClaimKeepsDistinctTypesAndPunctuation(t *testing.T) {
	batch := &WikiBatchContext{}
	svc := &wikiIngestService{}
	ctx := context.Background()

	concept := svc.claimWikiIdentitySlug(
		ctx, "kb-1", types.WikiPageTypeConcept, "寓言", "concept/fable-zhuangzi", false, batch,
	)
	chapter := svc.claimWikiIdentitySlug(
		ctx, "kb-1", types.WikiPageTypeConcept, "《寓言》", "concept/yuyan-chapter", false, batch,
	)
	entity := svc.claimWikiIdentitySlug(
		ctx, "kb-1", types.WikiPageTypeEntity, "寓言", "entity/yuyan-zhuangzi", false, batch,
	)
	if concept != "concept/fable-zhuangzi" || chapter != "concept/yuyan-chapter" || entity != "entity/yuyan-zhuangzi" {
		t.Fatalf("distinct identities were collapsed: concept=%q chapter=%q entity=%q", concept, chapter, entity)
	}
}

func TestStabilizeExtractedIdentitiesCoalescesEvidence(t *testing.T) {
	svc := &wikiIngestService{}
	batch := &WikiBatchContext{}
	items := []extractedItem{
		{
			Name: "孔子", Slug: "entity/kong-zi", Aliases: []string{"孔丘"},
			Description: "思想家", Details: "短", SourceChunks: []string{"chunk-1"},
		},
		{
			Name: "孔 子", Slug: "entity/confucius", Aliases: []string{"Confucius"},
			Description: "中国古代思想家、教育家", Details: "更完整的说明", SourceChunks: []string{"chunk-2"},
		},
	}
	got := svc.stabilizeExtractedIdentities(
		context.Background(), "kb-1", types.WikiPageTypeEntity, items, nil, nil, batch,
	)
	if len(got) != 1 {
		t.Fatalf("stabilized item count = %d, want 1", len(got))
	}
	if got[0].Slug != "entity/kong-zi" {
		t.Fatalf("stabilized slug = %q, want first claimed slug", got[0].Slug)
	}
	if got[0].Description != "中国古代思想家、教育家" || got[0].Details != "更完整的说明" {
		t.Fatalf("richer fallback text not preserved: %#v", got[0])
	}
	if len(got[0].SourceChunks) != 2 {
		t.Fatalf("source chunks were not unioned: %#v", got[0].SourceChunks)
	}
}

func TestWikiIdentityClaimRedisOverridesStaleLocal(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	svc := &wikiIngestService{redisClient: rdb}
	ctx := context.Background()
	batch := &WikiBatchContext{}
	identity := normalizeWikiIdentityTitle("孔子")
	batch.identityClaims.Store(types.WikiPageTypeEntity+"\x00"+identity, "entity/kong-zi")
	if err := rdb.Set(ctx, wikiIdentityClaimPrefix+"kb-1:"+types.WikiPageTypeEntity+":"+identity, "entity/confucius", wikiIdentityClaimTTL).Err(); err != nil {
		t.Fatalf("seed redis claim: %v", err)
	}

	got := svc.claimWikiIdentitySlug(ctx, "kb-1", types.WikiPageTypeEntity, "孔子", "entity/kongqiu", false, batch)
	if got != "entity/confucius" {
		t.Fatalf("redis claim should beat stale local map: got %q", got)
	}
	if stored, _ := batch.identityClaims.Load(types.WikiPageTypeEntity + "\x00" + identity); stored != "entity/confucius" {
		t.Fatalf("local cache not refreshed from redis: %#v", stored)
	}
}

func TestStabilizeLLMMergeDoesNotOverrideRedisClaim(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	svc := &wikiIngestService{redisClient: rdb}
	ctx := context.Background()
	batch := &WikiBatchContext{}

	first := svc.claimWikiIdentitySlug(ctx, "kb-1", types.WikiPageTypeEntity, "孔子", "entity/kong-zi", false, batch)
	if first != "entity/kong-zi" {
		t.Fatalf("provisional claim = %q, want entity/kong-zi", first)
	}

	got := svc.stabilizeExtractedIdentities(ctx, "kb-1", types.WikiPageTypeEntity, []extractedItem{
		{Name: "孔子", Slug: "entity/confucius"},
	}, map[string]string{"entity/confucius": "entity/kong-zi-institute"}, nil, batch)
	if len(got) != 1 || got[0].Slug != "entity/kong-zi" {
		t.Fatalf("LLM merge overwrote identity claim: %#v", got)
	}
}

func TestStabilizeExactTargetIsAuthoritative(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	svc := &wikiIngestService{redisClient: rdb}
	ctx := context.Background()
	batch := &WikiBatchContext{}

	svc.claimWikiIdentitySlug(ctx, "kb-1", types.WikiPageTypeEntity, "孔子", "entity/kong-zi", false, batch)
	got := svc.stabilizeExtractedIdentities(ctx, "kb-1", types.WikiPageTypeEntity, []extractedItem{
		{Name: "孔子", Slug: "entity/kong-zi"},
	}, nil, map[string]string{"entity/kong-zi": "entity/confucius"}, batch)
	if len(got) != 1 || got[0].Slug != "entity/confucius" {
		t.Fatalf("exact existing page should replace provisional claim: %#v", got)
	}

	follow := svc.claimWikiIdentitySlug(ctx, "kb-1", types.WikiPageTypeEntity, "孔子", "entity/kongqiu", false, &WikiBatchContext{})
	if follow != "entity/confucius" {
		t.Fatalf("authoritative exact hit did not stick in redis: %q", follow)
	}
}

func TestWikiIdentityClaimLiteConcurrentSameBatch(t *testing.T) {
	svc := &wikiIngestService{}
	batch := &WikiBatchContext{}
	ctx := context.Background()
	proposals := []string{"entity/kong-zi", "entity/confucius", "entity/kongzi"}
	results := make([]string, len(proposals))
	var wg sync.WaitGroup
	for i, proposal := range proposals {
		i, proposal := i, proposal
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = svc.claimWikiIdentitySlug(ctx, "kb-1", types.WikiPageTypeEntity, "孔子", proposal, false, batch)
		}()
	}
	wg.Wait()
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatalf("lite same-batch claims did not converge: %#v", results)
		}
	}
}

func TestRemapSlugUpdatesByIdentityConverges(t *testing.T) {
	svc := &wikiIngestService{}
	batch := &WikiBatchContext{}
	ctx := context.Background()
	svc.claimWikiIdentitySlug(ctx, "kb-1", types.WikiPageTypeEntity, "孔子", "entity/kong-zi", false, batch)

	got := svc.remapSlugUpdatesByIdentity(ctx, "kb-1", map[string][]SlugUpdate{
		"entity/kong-zi": {{
			Slug: "entity/kong-zi", Type: types.WikiPageTypeEntity,
			Item: extractedItem{Name: "孔子", Slug: "entity/kong-zi"},
		}},
		"entity/confucius": {{
			Slug: "entity/confucius", Type: types.WikiPageTypeEntity,
			Item: extractedItem{Name: "孔 子", Slug: "entity/confucius"},
		}},
		"summary/doc": {{Slug: "summary/doc", Type: types.WikiPageTypeSummary}},
	}, batch)
	if len(got["entity/kong-zi"]) != 2 {
		t.Fatalf("entity updates should coalesce onto claimed slug: %#v", got)
	}
	if len(got["summary/doc"]) != 1 {
		t.Fatalf("summary slug should stay untouched: %#v", got)
	}
	if _, ok := got["entity/confucius"]; ok {
		t.Fatalf("unclaimed romanization should have been remapped away: %#v", got)
	}
}

func TestReclaimExtractedIdentitiesCoalescesCitationSlugs(t *testing.T) {
	svc := &wikiIngestService{}
	batch := &WikiBatchContext{}
	svc.claimWikiIdentitySlug(context.Background(), "kb-1", types.WikiPageTypeEntity, "孔子", "entity/kong-zi", false, batch)

	entities, concepts := svc.reclaimExtractedIdentities(context.Background(), "kb-1", []extractedItem{
		{Name: "孔子", Slug: "entity/kong-zi", SourceChunks: []string{"c1"}},
		{Name: "孔 子", Slug: "entity/confucius", SourceChunks: []string{"c2"}},
	}, nil, batch)
	if len(entities) != 1 || entities[0].Slug != "entity/kong-zi" {
		t.Fatalf("citation slugs did not reclaim onto identity: entities=%#v", entities)
	}
	if len(entities[0].SourceChunks) != 2 {
		t.Fatalf("reclaim dropped citation evidence: %#v", entities[0])
	}
	if len(concepts) != 0 {
		t.Fatalf("unexpected concepts: %#v", concepts)
	}
}

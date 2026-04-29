package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

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

// Non-entity/concept pages (summaries, logs, …) must be stripped regardless
// of corpus size — they are never valid merge targets.
func TestSelectDedupCandidatePages_DropsNonEntityConcept(t *testing.T) {
	newItems := []extractedItem{
		{Slug: "concept/foo", Name: "Foo"},
	}
	pages := []*types.WikiPage{
		makePage("summary/some-doc", "Some Doc Summary", types.WikiPageTypeSummary),
		makePage("log/batch-123", "Batch 123", types.WikiPageTypeLog),
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

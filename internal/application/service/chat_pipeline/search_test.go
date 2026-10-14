package chatpipeline

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Anonymized FAQ entries from the partial-overlap report. Product and file
// names are placeholders; the requirements and support conclusions are not.
const (
	faqSigningRequirement = `标准问题：产品 X 是否满足以下要求：256位SM2签名速度≥200000TPS
答案：
产品：产品 X
产品型号：（原文未填写）
要求：256位SM2签名速度≥200000TPS
支持结论：条件支持
备注：需定制实现
来源：product-X.csv，CSV 第 18 行。`

	faqKeygenRequirement = `标准问题：产品 X 是否满足以下要求：256位SM2密钥对生成速度≥300000对/秒
答案：
产品：产品 X
产品型号：（原文未填写）
要求：256位SM2密钥对生成速度≥300000对/秒
支持结论：不支持
备注：（原文未填写）
来源：product-X.csv，CSV 第 15 行。`

	// Shared template line. The sanitized entries above measure a token-overlap
	// coefficient of about 0.846, just under the 0.85 gate, so dedup already
	// keeps them. The reported failure was contained=false at or above that
	// gate. This line is on both sides and only restores that condition.
	faqSharedTemplateLine = "适用场景：生产环境部署"
)

func TestRemovePartialOverlapsKeepsDistinctFAQFacts(t *testing.T) {
	signing := faqSigningRequirement + "\n" + faqSharedTemplateLine
	keygen := faqKeygenRequirement + "\n" + faqSharedTemplateLine

	t.Run("sanitized entries", func(t *testing.T) {
		got := removePartialOverlaps(context.Background(), []*types.SearchResult{
			{ID: "faq-A-correct", Content: faqSigningRequirement, Score: 1, KnowledgeID: "product-x"},
			{ID: "faq-B-other-requirement", Content: faqKeygenRequirement, Score: 1, KnowledgeID: "product-x"},
		})
		assertResultIDs(t, got, "faq-A-correct", "faq-B-other-requirement")
	})

	t.Run("same knowledge", func(t *testing.T) {
		got := removePartialOverlaps(context.Background(), []*types.SearchResult{
			{ID: "faq-A-correct", Content: signing, Score: 1, KnowledgeID: "product-x"},
			{ID: "faq-B-other-requirement", Content: keygen, Score: 1, KnowledgeID: "product-x"},
		})
		assertResultIDs(t, got, "faq-A-correct", "faq-B-other-requirement")
	})

	t.Run("different knowledge", func(t *testing.T) {
		got := removePartialOverlaps(context.Background(), []*types.SearchResult{
			{ID: "faq-A-correct", Content: signing, Score: 1, KnowledgeID: "kb-signing"},
			{ID: "faq-B-other-requirement", Content: keygen, Score: 1, KnowledgeID: "kb-keygen"},
		})
		assertResultIDs(t, got, "faq-A-correct", "faq-B-other-requirement")
	})
}

func TestRemovePartialOverlapsCollapsesCoveredNearDuplicate(t *testing.T) {
	t.Run("renewal extends the borrowing period", func(t *testing.T) {
		got := removePartialOverlaps(context.Background(), []*types.SearchResult{
			{ID: "two-months", Content: "borrowing lasts two months", Score: 0.8},
			{ID: "with-renewal", Content: "borrowing lasts two months and permits one one-month renewal", Score: 0.8},
		})
		assertResultIDs(t, got, "with-renewal")
	})

	// Same tokens on the shorter side, but not a literal substring, so only
	// the overlap path can collapse the pair.
	t.Run("token subset", func(t *testing.T) {
		got := removePartialOverlaps(context.Background(), []*types.SearchResult{
			{ID: "short", Content: "renewal window two months borrowing", Score: 0.4},
			{ID: "long", Content: "the borrowing window is two months and permits one renewal", Score: 0.9},
		})
		assertResultIDs(t, got, "long")
	})

	// One leftover token in a long shared stem is still the same fact.
	t.Run("single leftover token", func(t *testing.T) {
		var stem strings.Builder
		for i := 0; i < 55; i++ {
			if i > 0 {
				stem.WriteByte(' ')
			}
			stem.WriteString("word")
			stem.WriteString(strings.Repeat("x", i%3))
			stem.WriteByte(byte('a' + i%26))
		}
		shared := stem.String()
		got := removePartialOverlaps(context.Background(), []*types.SearchResult{
			{ID: "short", Content: shared + " stray", Score: 0.4},
			{ID: "long", Content: shared + " other alpha beta", Score: 0.9},
		})
		assertResultIDs(t, got, "long")
	})
}

func TestRemovePartialOverlapsKeepsDistinctNumbers(t *testing.T) {
	base := "产品 X 标准问题 是否满足以下要求 答案 产品型号 原文未填写 备注 来源 product-X csv 行 支持结论 条件 签名速度"
	got := removePartialOverlaps(context.Background(), []*types.SearchResult{
		{ID: "limit-200000", Content: base + " 200000", Score: 1, KnowledgeID: "kb-a"},
		{ID: "limit-300000", Content: base + " 300000", Score: 1, KnowledgeID: "kb-b"},
	})
	assertResultIDs(t, got, "limit-200000", "limit-300000")
}

func assertResultIDs(t *testing.T, results []*types.SearchResult, want ...string) {
	t.Helper()
	got := make([]string, len(results))
	for i, r := range results {
		got[i] = r.ID
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

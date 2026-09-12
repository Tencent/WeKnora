package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func u14KB(i, total int) *KnowledgeBaseInfo {
	return &KnowledgeBaseInfo{
		ID:           fmt.Sprintf("kb-%05d-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", i),
		Name:         fmt.Sprintf("部门知识库-%05d（产品线文档）", i),
		Type:         "document",
		DocCount:     120,
		Capabilities: []string{"vector", "keyword"},
		Description:  strings.Repeat("本知识库收录该产品线的需求文档、设计评审与运维手册，覆盖接入规范与发布流程。", 6),
		RecentDocs: []RecentDocInfo{
			{KnowledgeID: "k-11111111-2222-3333-4444-555555555555", Title: "2026-Q3 产品需求文档合集", Type: "file", ChunkID: "c-1"},
			{KnowledgeID: "k-66666666-7777-8888-9999-000000000000", Title: "服务接入规范 v3", Type: "file", ChunkID: "c-2"},
		},
	}
}

func u14Corpus(n int) []*KnowledgeBaseInfo {
	out := make([]*KnowledgeBaseInfo, n)
	for i := range out {
		out[i] = u14KB(i, n)
	}
	return out
}

// TestFormatKnowledgeBaseListSmallCatalogUnchanged pins that ordinary catalogs
// (a handful of KBs) keep the full-detail rendering this list has always had.
func TestFormatKnowledgeBaseListSmallCatalogUnchanged(t *testing.T) {
	out := formatKnowledgeBaseList(u14Corpus(5))
	require.Contains(t, out, "<description>")
	require.Contains(t, out, "<recent_documents>")
	require.NotContains(t, out, "omitted")
	for i := 0; i < 5; i++ {
		require.Contains(t, out, fmt.Sprintf("kb-%05d", i))
	}
}

// TestFormatKnowledgeBaseListDegradesUnderBudget pins the two-layer budget:
// full-detail entries stop at kbCatalogFullDetailChars, the rest degrade to
// single-line entries that keep every routing id, and the total stays bounded
// no matter how many knowledge bases are bound.
func TestFormatKnowledgeBaseListDegradesUnderBudget(t *testing.T) {
	const n = 541
	out := formatKnowledgeBaseList(u14Corpus(n))

	// The pre-budget rendering of this corpus was ~666KB / 392k chars; the
	// budgeted one must stay an order of magnitude below that.
	require.Less(t, len(out), 150_000, "catalog should stay bounded for 541 KBs")

	// Every routing id must survive degradation: knowledge_search targets
	// bases by these ids.
	for i := 0; i < n; i++ {
		require.Contains(t, out, fmt.Sprintf("kb-%05d", i), "routing id lost at %d", i)
	}

	// The head keeps full detail, the tail is compact: the degraded region
	// carries ids but no descriptions.
	head := strings.Index(out, "<description>")
	require.GreaterOrEqual(t, head, 0)
	tail := out[strings.LastIndex(out, "<knowledge_base "):]
	require.NotContains(t, tail, "<description>")

	// A 541-KB tenant fits inside the hard ceiling, so nothing is omitted.
	require.NotContains(t, out, "omitted")
}

// TestFormatKnowledgeBaseListHardCeilingOmits pins the hard ceiling: past
// kbCatalogHardCeilingChars the tail is dropped entirely and the note tells
// the model those bases are still covered by unscoped searches.
func TestFormatKnowledgeBaseListHardCeilingOmits(t *testing.T) {
	out := formatKnowledgeBaseList(u14Corpus(3000))
	require.Contains(t, out, "further knowledge bases are omitted")
	require.Contains(t, out, "knowledge_base_ids scope still cover them")
	// Total output stays near the ceiling, not near the 3.7MB unbounded form.
	require.Less(t, len(out), kbCatalogHardCeilingChars+10_000)
	// The very last bound base is one of the omitted ones.
	require.NotContains(t, out, "kb-02999")
}

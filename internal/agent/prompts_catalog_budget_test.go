package agent

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func docKB(name string, descRunes, docs int) *KnowledgeBaseInfo {
	kb := &KnowledgeBaseInfo{
		ID:           fmt.Sprintf("kb-%s", name),
		Name:         name,
		Type:         "document",
		Description:  strings.Repeat("描", descRunes),
		DocCount:     docs,
		Capabilities: []string{"chunks"},
	}
	for i := 0; i < docs; i++ {
		kb.RecentDocs = append(kb.RecentDocs, RecentDocInfo{
			KnowledgeID: fmt.Sprintf("%s-knowledge-%d", kb.ID, i),
			Title:       fmt.Sprintf("%s doc %d", name, i),
			FileSize:    1024,
			Type:        "pdf",
			CreatedAt:   "2026-01-01",
			Description: strings.Repeat("summary ", 20),
		})
	}
	return kb
}

func faqKB(name string, entries, answerRunes int) *KnowledgeBaseInfo {
	kb := &KnowledgeBaseInfo{
		ID:           fmt.Sprintf("kb-%s", name),
		Name:         name,
		Type:         "faq",
		Description:  strings.Repeat("问", 100),
		DocCount:     entries,
		Capabilities: []string{"chunks"},
	}
	for i := 0; i < entries; i++ {
		kb.RecentDocs = append(kb.RecentDocs, RecentDocInfo{
			ChunkID:             fmt.Sprintf("%s-chunk-%d", kb.ID, i),
			KnowledgeID:         fmt.Sprintf("%s-knowledge-%d", kb.ID, i),
			FAQStandardQuestion: fmt.Sprintf("question %s %d?", name, i),
			FAQAnswers:          []string{strings.Repeat("答", answerRunes)},
			CreatedAt:           "2026-01-01",
		})
	}
	return kb
}

func TestFormatKnowledgeBaseList_Empty(t *testing.T) {
	assert.Equal(t, "<knowledge_bases />", formatKnowledgeBaseList(nil))
}

// A catalog that fits the budget renders at full detail, exactly as before
// budgeting existed (plus per-item caps and escaping).
func TestFormatKnowledgeBaseList_SmallCatalogKeepsFullDetail(t *testing.T) {
	out := formatKnowledgeBaseList([]*KnowledgeBaseInfo{docKB("alpha", 100, 2)})
	require.True(t, utf8.RuneCountInString(out) <= maxRuntimeCatalogRunes)
	assert.Contains(t, out, "<description>")
	assert.Contains(t, out, "<recent_documents>")
	assert.Contains(t, out, "<summary>")
	assert.Contains(t, out, `capabilities="chunks"`)
	assert.NotContains(t, out, "not listed")
}

// The FAQ answer is the first thing sacrificed: questions — the routing
// signal — survive for every KB, answers do not. Fixture sized so the full
// tier exceeds the budget (~9.8k runes) while the answer-free tier fits
// (~5.5k runes).
func TestFormatKnowledgeBaseList_DropsFAQAnswersBeforeQuestions(t *testing.T) {
	var kbs []*KnowledgeBaseInfo
	for i := 0; i < 10; i++ {
		kbs = append(kbs, faqKB(fmt.Sprintf("faq%d", i), 2, faqAnswerMaxRunes+100))
	}
	out := formatKnowledgeBaseList(kbs)
	require.True(t, utf8.RuneCountInString(out) <= maxRuntimeCatalogRunes)
	assert.NotContains(t, out, "<answer>")
	assert.Contains(t, out, "<faq_entries>")
	assert.Contains(t, out, "<question>question faq0 0?</question>")
	assert.Contains(t, out, "<question>question faq9 1?</question>")
}

// When even question-only rendering cannot fit, every KB collapses to its
// identity row — the last thing to go — and the block stays within budget.
func TestFormatKnowledgeBaseList_CollapsesToIdentityRows(t *testing.T) {
	var kbs []*KnowledgeBaseInfo
	for i := 0; i < 60; i++ {
		kbs = append(kbs, docKB(fmt.Sprintf("kb%d", i), kbDescriptionMaxRunes, 2))
	}
	out := formatKnowledgeBaseList(kbs)
	require.True(t, utf8.RuneCountInString(out) <= maxRuntimeCatalogRunes)
	assert.NotContains(t, out, "<description>")
	assert.NotContains(t, out, "<recent_documents>")
	assert.Contains(t, out, `<knowledge_base id="kb-kb0" name="kb0" type="document" doc_count="2"`)
	assert.Contains(t, out, `<knowledge_base id="kb-kb59"`)
}

// The identity tier caps its own row list: arbitrarily many bound KBs still
// produce a block within budget, with the remainder summarized in a note.
func TestFormatKnowledgeBaseList_IdentityRowsTruncateWithNote(t *testing.T) {
	var kbs []*KnowledgeBaseInfo
	for i := 0; i < 541; i++ {
		kb := docKB(fmt.Sprintf("kb%d", i), 10, 0)
		kb.RecentDocs = nil
		kbs = append(kbs, kb)
	}
	out := formatKnowledgeBaseList(kbs)
	require.True(t, utf8.RuneCountInString(out) <= maxRuntimeCatalogRunes)
	assert.Contains(t, out, "more knowledge bases are bound for this turn but not listed")
	// The block is still well-formed.
	assert.True(t, strings.HasPrefix(out, "<knowledge_bases>\n"))
	assert.True(t, strings.HasSuffix(out, "</knowledge_bases>"))
}

// KB names and descriptions are user-controlled and were previously
// interpolated raw into the XML-ish block.
func TestFormatKnowledgeBaseList_EscapesUserContent(t *testing.T) {
	kb := docKB("alpha", 20, 0)
	kb.Name = `<img src=x onerror=alert(1)> "quoted"`
	kb.Description = `a & b <c> "d"`
	kb.RecentDocs = nil
	out := formatKnowledgeBaseList([]*KnowledgeBaseInfo{kb})
	assert.Contains(t, out, `name="&lt;img src=x onerror=alert(1)&gt; &quot;quoted&quot;"`)
	assert.Contains(t, out, `a &amp; b &lt;c&gt; &quot;d&quot;`)
	assert.NotContains(t, out, "<img ")
}

// Per-item caps hold regardless of the tier that rendered the entry.
func TestFormatKnowledgeBaseList_PerItemCaps(t *testing.T) {
	kb := docKB("alpha", 500, 0)
	kb.RecentDocs = nil
	faq := faqKB("beta", 1, 500)
	out := formatKnowledgeBaseList([]*KnowledgeBaseInfo{kb, faq})
	assert.NotContains(t, out, strings.Repeat("描", kbDescriptionMaxRunes+1))
	assert.NotContains(t, out, strings.Repeat("答", faqAnswerMaxRunes+1))
}

func TestTruncateRunes(t *testing.T) {
	cjk := strings.Repeat("中", 300)
	got := truncateRunes(cjk, 200)
	require.Len(t, []rune(got), 203) // 200 runes + "..."
	require.True(t, utf8.ValidString(got), "byte-slicing would produce invalid UTF-8 here")
	assert.True(t, strings.HasSuffix(got, "..."))

	assert.Equal(t, "abc", truncateRunes("abc", 10))
	assert.Equal(t, "", truncateRunes("abc", 0))
}

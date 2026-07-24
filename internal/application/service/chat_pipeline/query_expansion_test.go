package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func testingContext() context.Context {
	return context.Background()
}

func dummyChatManage(query string) *types.ChatManage {
	return &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: query,
		},
		PipelineState: types.PipelineState{
			RewriteQuery: query,
		},
	}
}

// TestExtractKeywords_ChineseProducesMeaningfulWords verifies that
// a Chinese question query yields at least two content-word keywords,
// each spanning at least two runes.
func TestExtractKeywords_ChineseProducesMeaningfulWords(t *testing.T) {
	keywords := extractKeywords("什么是瀑布模型")

	for _, kw := range keywords {
		if len([]rune(kw)) < 2 {
			t.Errorf("extractKeywords(%q) produced single-rune keyword %q. "+
				"Chinese keywords should be multi-character words. Full output: %v",
				"什么是瀑布模型", kw, keywords)
		}
	}

	if len(keywords) < 2 {
		t.Errorf("extractKeywords(%q) produced only %d keywords: %v. "+
			"Expected at least 2 meaningful keywords.",
			"什么是瀑布模型", len(keywords), keywords)
	}

	hasWaterfall := false
	for _, kw := range keywords {
		if kw == "瀑布" || kw == "模型" || kw == "瀑布模型" {
			hasWaterfall = true
			break
		}
	}
	if !hasWaterfall {
		t.Errorf("extractKeywords(%q) = %v does not contain expected keyword like '瀑布' or '模型'",
			"什么是瀑布模型", keywords)
	}

	t.Logf("extractKeywords(%q) = %v", "什么是瀑布模型", keywords)
}

// TestExtractKeywords_ChineseWordBoundaries verifies that multi-word
// Chinese text is segmented into at least two keywords, with no
// single-rune output.
func TestExtractKeywords_ChineseWordBoundaries(t *testing.T) {
	keywords := extractKeywords("瀑布模型软件工程")
	t.Logf("extractKeywords(%q) = %v", "瀑布模型软件工程", keywords)

	if len(keywords) < 2 {
		t.Errorf("extractKeywords(%q) produced only %d keywords: %v. "+
			"Multi-word Chinese should be segmented into at least 2 words.",
			"瀑布模型软件工程", len(keywords), keywords)
	}

	for _, kw := range keywords {
		if len([]rune(kw)) < 2 {
			t.Errorf("extractKeywords produced single-rune keyword %q from %q",
				kw, "瀑布模型软件工程")
		}
	}
}

// TestExpandQueries_ChineseProducesCleanVariants verifies that
// a Chinese query produces both a keyword-based variant (space-separated
// content words) and a question-word-stripped variant, with no isolated
// CJK characters in any variant.
func TestExpandQueries_ChineseProducesCleanVariants(t *testing.T) {
	ctx := testingContext()
	cm := dummyChatManage("什么是瀑布模型")
	expansions := (&PluginSearch{}).expandQueries(ctx, cm)

	foundRemoveWords := false
	foundKeywords := false
	for _, exp := range expansions {
		if exp == "瀑布模型" {
			foundRemoveWords = true
		}
		if strings.Count(exp, " ") >= 1 && strings.Contains(exp, "瀑布") && strings.Contains(exp, "模型") {
			foundKeywords = true
		}
		if strings.TrimSpace(exp) == "" {
			t.Errorf("expandQueries produced empty expansion")
		}
		parts := strings.Fields(exp)
		for _, p := range parts {
			if len([]rune(p)) == 1 && isRunOfCJK(p) {
				t.Errorf("expandQueries produced expansion %q containing isolated CJK character %q",
					exp, p)
			}
		}
	}

	if !foundRemoveWords {
		t.Errorf("expandQueries(%q) should include '瀑布模型' (question-word-stripped variant). Got: %v",
			"什么是瀑布模型", expansions)
	}
	if !foundKeywords {
		t.Errorf("expandQueries(%q) should include a keyword-based variant containing '瀑布' and '模型'. Got: %v",
			"什么是瀑布模型", expansions)
	}

	t.Logf("expandQueries(%q) = %v", "什么是瀑布模型", expansions)
}

func isRunOfCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

// TestExtractKeywords_EnglishPreserveIntegrity verifies that English
// queries produce at least two keywords with stopwords removed.
func TestExtractKeywords_EnglishPreserveIntegrity(t *testing.T) {
	keywords := extractKeywords("how to fix login bug")
	t.Logf("extractKeywords(%q) = %v", "how to fix login bug", keywords)
	if len(keywords) < 2 {
		t.Errorf("extractKeywords(%q) should produce at least 2 keywords, got %d: %v",
			"how to fix login bug", len(keywords), keywords)
	}
	for _, kw := range keywords {
		if kw == "is" || kw == "the" || kw == "to" || kw == "how" {
			t.Errorf("extractKeywords included stopword %q", kw)
		}
	}
}

// TestExtractKeywords_EmptyInput verifies empty and whitespace-only
// inputs return no keywords.
func TestExtractKeywords_EmptyInput(t *testing.T) {
	keywords := extractKeywords("")
	if len(keywords) != 0 {
		t.Errorf("extractKeywords('') should return empty, got %v", keywords)
	}
	keywords = extractKeywords("   ")
	if len(keywords) != 0 {
		t.Errorf("extractKeywords('   ') should return empty, got %v", keywords)
	}
}

// TestExtractKeywords_StopwordOnly verifies that input consisting only
// of stopwords returns no keywords.
func TestExtractKeywords_StopwordOnly(t *testing.T) {
	keywords := extractKeywords("的是")
	if len(keywords) != 0 {
		t.Errorf("extractKeywords(%q) should return empty (only stopwords), got %v", "的是", keywords)
	}

	keywords = extractKeywords("的 是")
	if len(keywords) != 0 {
		t.Errorf("extractKeywords(%q) should return empty (only stopwords), got %v", "的 是", keywords)
	}
}

// TestExtractKeywords_MixedLanguage verifies that mixed CJK+English
// input yields keywords from both languages, with Chinese producing
// multi-rune words and English producing whitespace-separated tokens,
// all without single-rune CJK output.
func TestExtractKeywords_MixedLanguage(t *testing.T) {
	keywords := extractKeywords("fix 软件工程流程")
	t.Logf("extractKeywords(%q) = %v", "fix 软件工程流程", keywords)

	if len(keywords) < 3 {
		t.Errorf("extractKeywords(%q) produced only %d keywords: %v. "+
			"Mixed Chinese should be segmented into at least 3 words.",
			"fix 软件工程流程", len(keywords), keywords)
	}

	for _, kw := range keywords {
		if len([]rune(kw)) == 1 && isRunOfCJK(kw) {
			t.Errorf("extractKeywords produced single CJK rune keyword %q", kw)
		}
	}
}

// TestExpandQueries_ProducesMultipleVariants verifies that a Chinese
// query yields at least two expansion variants: a keyword-based variant
// and a question-word-stripped variant.
func TestExpandQueries_ProducesMultipleVariants(t *testing.T) {
	ctx := testingContext()
	cm := dummyChatManage("什么是瀑布模型")
	expansions := (&PluginSearch{}).expandQueries(ctx, cm)

	if len(expansions) < 2 {
		t.Errorf("expandQueries(%q) produced only %d expansion(s): %v. "+
			"Expected at least 2 (keyword variant + question-word-stripped).",
			"什么是瀑布模型", len(expansions), expansions)
	}
	t.Logf("expandQueries(%q) produced %d variants: %v", "什么是瀑布模型", len(expansions), expansions)
}

// TestExpandQueries_VariantLimit verifies that expandQueries caps output
// at 5 variants even when the input generates many raw candidates.
func TestExpandQueries_VariantLimit(t *testing.T) {
	ctx := testingContext()
	q := `"fix the bug" "refactor handler" "add test" partone, parttwo, partthree`
	cm := dummyChatManage(q)
	expansions := (&PluginSearch{}).expandQueries(ctx, cm)
	if len(expansions) > 5 {
		t.Errorf("expandQueries produced %d variants, expected at most 5: %v",
			len(expansions), expansions)
	}
	t.Logf("expandQueries produced %d variants: %v", len(expansions), expansions)
}

// TestExpandQueries_EnglishInput verifies that English queries produce
// at least one expansion variant with no empty output.
func TestExpandQueries_EnglishInput(t *testing.T) {
	ctx := testingContext()
	cm := dummyChatManage("how to fix a segmentation fault in C")
	expansions := (&PluginSearch{}).expandQueries(ctx, cm)

	if len(expansions) == 0 {
		t.Errorf("expandQueries(%q) produced no expansions, expected at least 1",
			"how to fix a segmentation fault in C")
	}
	for _, exp := range expansions {
		if strings.TrimSpace(exp) == "" {
			t.Errorf("expandQueries produced empty expansion for English input")
		}
	}
	t.Logf("expandQueries(%q) = %v", "how to fix a segmentation fault in C", expansions)
}

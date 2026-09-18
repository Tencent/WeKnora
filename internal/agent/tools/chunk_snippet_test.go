package tools

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func faqChunkForSnippetTest(t *testing.T) *types.Chunk {
	t.Helper()
	chunk := &types.Chunk{
		ChunkType: types.ChunkTypeFAQ,
		Content: `Q: 离职原因是否可以自定义
Similar Questions:
- 离职原因自定义
- 设置离职原因`,
	}
	meta := &types.FAQChunkMetadata{
		StandardQuestion: "离职原因是否可以自定义",
		SimilarQuestions: []string{"离职原因自定义", "设置离职原因"},
		Answers:          []string{"可以，在系统设置中自定义离职原因。"},
	}
	if err := chunk.SetFAQMetadata(meta); err != nil {
		t.Fatalf("SetFAQMetadata: %v", err)
	}
	return chunk
}

func TestFaqMatchSnippet_StandardQuestionAndMetadataAnswer(t *testing.T) {
	chunk := faqChunkForSnippetTest(t)
	re := regexp.MustCompile(`(?i)是否可以自定义`)
	snippet := faqMatchSnippet(chunk, []*regexp.Regexp{re})

	if !strings.Contains(snippet, "Q: 离职原因是否可以自定义") {
		t.Fatalf("want standard question in snippet, got: %s", snippet)
	}
	if !strings.Contains(snippet, "A: 可以，在系统设置中") {
		t.Fatalf("want metadata answer in snippet, got: %s", snippet)
	}
	for _, unwanted := range []string{"Similar Questions", "离职原因自定义", "设置离职原因"} {
		if strings.Contains(snippet, unwanted) {
			t.Errorf("snippet should not include similar-question noise %q: %s", unwanted, snippet)
		}
	}
}

func TestFaqMatchSnippetFromQueries_StandardQuestionAndMetadataAnswer(t *testing.T) {
	meta := &types.FAQChunkMetadata{
		StandardQuestion: "离职原因是否可以自定义",
		SimilarQuestions: []string{"离职原因自定义", "设置离职原因"},
		Answers:          []string{"可以，在系统设置中自定义离职原因。"},
	}
	snippet := faqMatchSnippetFromQueries(meta, []string{"是否可以自定义"})

	if !strings.Contains(snippet, "Q: 离职原因是否可以自定义") {
		t.Fatalf("want standard question, got: %s", snippet)
	}
	if !strings.Contains(snippet, "A: 可以，在系统设置中") {
		t.Fatalf("want metadata answer, got: %s", snippet)
	}
	if strings.Contains(snippet, "Similar Questions") {
		t.Fatalf("should not include similar-question list: %s", snippet)
	}
}

func TestFaqMatchSnippetFromQueries_SimilarQuestionHitShowsMatchedVariant(t *testing.T) {
	meta := &types.FAQChunkMetadata{
		StandardQuestion: "离职原因是否可以自定义",
		SimilarQuestions: []string{"离职原因自定义", "设置离职原因"},
		Answers:          []string{"可以自定义。"},
	}
	snippet := faqMatchSnippetFromQueries(meta, []string{"离职原因自定义"})

	if !strings.Contains(snippet, "Q: 离职原因自定义") {
		t.Fatalf("want matched similar question, got: %s", snippet)
	}
}

func TestFaqMatchSnippet_SimilarQuestionHitShowsMatchedVariant(t *testing.T) {
	chunk := faqChunkForSnippetTest(t)
	re := regexp.MustCompile(`(?i)离职原因自定义`)
	snippet := faqMatchSnippet(chunk, []*regexp.Regexp{re})

	if !strings.Contains(snippet, "Q: 离职原因自定义") {
		t.Fatalf("want matched similar question, got: %s", snippet)
	}
	if strings.Contains(snippet, "是否可以自定义") {
		t.Fatalf("should not show standard question when similar question matched: %s", snippet)
	}
}

func TestExtractChunkMatchSnippet_NonFAQUsesBodyContext(t *testing.T) {
	content := strings.Repeat("前置。", 50) + "TARGET" + strings.Repeat("后续。", 50)
	chunk := &types.Chunk{ChunkType: types.ChunkTypeText, Content: content}
	re := regexp.MustCompile(`TARGET`)
	snippet := extractChunkMatchSnippet(chunk, []*regexp.Regexp{re})

	if !strings.Contains(snippet, "TARGET") {
		t.Fatalf("missing match: %s", snippet)
	}
	if !strings.Contains(snippet, "前置") || !strings.Contains(snippet, "后续") {
		t.Fatalf("expected expanded body context: %s", snippet)
	}
}

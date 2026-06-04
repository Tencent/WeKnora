package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestWriteFAQMetadataXML_IncludesAnswers(t *testing.T) {
	var b strings.Builder
	meta := &types.FAQChunkMetadata{
		StandardQuestion: "如何创建知识库？",
		SimilarQuestions: []string{"怎么创建知识库？"},
		Answers:          []string{"在控制台点击新建知识库。"},
	}
	writeFAQMetadataXML(&b, meta)
	out := b.String()

	for _, want := range []string{
		"<faq>", "<question>如何创建知识库？</question>",
		"<similar_question>怎么创建知识库？</similar_question>",
		"<answer>在控制台点击新建知识库。</answer>", "</faq>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestWriteSimilarQuestionsXML_TruncatesWhenTooMany(t *testing.T) {
	var b strings.Builder
	questions := make([]string, 8)
	for i := range questions {
		questions[i] = fmt.Sprintf("相似问%d", i+1)
	}
	writeSimilarQuestionsXML(&b, questions)
	out := b.String()

	if strings.Count(out, "<similar_question>") != faqMaxSimilarQuestionsDisplay {
		t.Fatalf("want %d similar_question tags, got:\n%s", faqMaxSimilarQuestionsDisplay, out)
	}
	if !strings.Contains(out, `<similar_questions_omitted count="3"`) {
		t.Fatalf("want omitted marker, got:\n%s", out)
	}
}

func TestWriteFAQEntryXML_UsesFaqNotChunk(t *testing.T) {
	chunk := &types.Chunk{
		ID:          "faq-chunk-1",
		ChunkType:   types.ChunkTypeFAQ,
		ChunkIndex:  0,
		KnowledgeID: "kb-doc-1",
		Content:     "Q: test\nSimilar Questions:\n- alt",
	}
	meta := &types.FAQChunkMetadata{
		StandardQuestion: "如何创建知识库？",
		Answers:          []string{"点新建即可。"},
	}
	if err := chunk.SetFAQMetadata(meta); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	writeFAQEntryXML(&b, chunk)
	out := b.String()

	if strings.Contains(out, "<chunk") {
		t.Fatalf("FAQ list output must not use <chunk>, got:\n%s", out)
	}
	for _, want := range []string{
		`<faq faq_id="faq-chunk-1"`,
		"<question>如何创建知识库？</question>",
		"<answer>点新建即可。</answer>",
		"</faq>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

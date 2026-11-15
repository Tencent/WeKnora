package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSplitWikiDocumentCoversTailAndUnicodeBoundary(t *testing.T) {
	content := strings.Repeat("清", maxContentForWiki-2) + "王之涣\n" + strings.Repeat("疆", 4000) + "建省尾章"
	parts := splitWikiDocument(content)
	if len(parts) != 2 {
		t.Fatalf("segments = %d", len(parts))
	}
	if !strings.HasSuffix(parts[1], "建省尾章") || !strings.Contains(parts[1], "王之涣") {
		t.Fatal("tail or boundary entity lost")
	}
	for _, part := range parts {
		if len([]rune(part)) > maxContentForWiki {
			t.Fatal("model budget exceeded")
		}
	}
	if !strings.Contains(content, parts[0]) || !strings.Contains(content, parts[1]) {
		t.Fatal("split changed document text")
	}
}

func TestExtractWikiSegmentsMergesTailCandidatesAndAliases(t *testing.T) {
	calls := 0
	content := strings.Repeat("文", maxContentForWiki+100)
	result, err := extractWikiSegments(content, func(string) (combinedExtraction, error) {
		calls++
		if calls == 1 {
			return combinedExtraction{Entities: []extractedItem{
				{Slug: "entity/zuo", Name: "左宗棠", Aliases: []string{"左公"}},
			}}, nil
		}
		return combinedExtraction{Entities: []extractedItem{
			{Slug: "entity/zuo", Name: "左宗棠", Aliases: []string{"左季高"}},
			{Slug: "entity/wang", Name: "王之涣"},
		}}, nil
	})
	if err != nil || calls != 2 || len(result.Entities) != 2 {
		t.Fatalf("incomplete extraction: %+v %v", result, err)
	}
	if len(result.Entities[0].Aliases) != 2 || result.Entities[1].Name != "王之涣" {
		t.Fatalf("merge lost candidates: %+v", result.Entities)
	}
}

func TestExtractWikiSegmentsDoesNotReportPartialSuccess(t *testing.T) {
	calls := 0
	content := strings.Repeat("文", maxContentForWiki+1)
	result, err := extractWikiSegments(content, func(string) (combinedExtraction, error) {
		calls++
		if calls == 2 {
			return combinedExtraction{}, errors.New("tail failed")
		}
		return combinedExtraction{Entities: []extractedItem{{Slug: "entity/a", Name: "a"}}}, nil
	})
	if err == nil || len(result.Entities) != 0 {
		t.Fatalf("partial success reported: %+v %v", result, err)
	}
}

func TestWikiSummaryIncludesTailAndSynthesizesSectionSummaries(t *testing.T) {
	model := &scriptedTemplateChatModel{responses: []*types.ChatResponse{
		{Content: "前文的摘要", FinishReason: "stop"},
		{Content: "建省尾章的摘要", FinishReason: "stop"},
		{Content: "全文摘要", FinishReason: "stop"},
	}}
	service := &wikiIngestService{}
	result, err := service.generateWikiDocumentSummary(context.Background(), model, map[string]string{
		"Content": strings.Repeat("清", maxContentForWiki+100) + "建省尾章", "Language": "Chinese",
	})
	if err != nil || result != "全文摘要" || len(model.calls) != 3 {
		t.Fatalf("summary failed: %s %v", result, err)
	}
	if !strings.Contains(model.calls[1][0].Content, "建省尾章") {
		t.Fatal("tail not sent to model")
	}
	if !strings.Contains(model.calls[2][0].Content, "前文的摘要") ||
		!strings.Contains(model.calls[2][0].Content, "建省尾章的摘要") {
		t.Fatal("section summary not included in final synthesis")
	}
}

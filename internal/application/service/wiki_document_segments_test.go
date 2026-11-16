package service

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	result, err := extractWikiSegments(
		context.Background(),
		strings.Repeat("文", wikiExtractionMaxRunes+100),
		1,
		func(_ context.Context, _ string) (combinedExtraction, error) {
			calls++
			if calls == 1 {
				return combinedExtraction{
					Entities: []extractedItem{{Slug: "entity/zuo", Name: "左宗棠", Aliases: []string{"左公"}}},
				}, nil
			}
			return combinedExtraction{
				Entities: []extractedItem{
					{Slug: "entity/zuo", Name: "左宗棠", Aliases: []string{"左季高"}},
					{Slug: "entity/wang", Name: "王之涣"},
				},
			}, nil
		},
	)
	if err != nil || calls != 2 || len(result.Entities) != 2 {
		t.Fatalf("incomplete extraction: %+v %v", result, err)
	}
	if len(result.Entities[0].Aliases) != 2 || result.Entities[1].Name != "王之涣" {
		t.Fatalf("merge lost candidates: %+v", result.Entities)
	}
}

func TestExtractWikiSegmentsDoesNotReportPartialSuccess(t *testing.T) {
	calls := 0
	result, err := extractWikiSegments(
		context.Background(),
		strings.Repeat("文", wikiExtractionMaxRunes+1),
		1,
		func(context.Context, string) (combinedExtraction, error) {
			calls++
			if calls == 2 {
				return combinedExtraction{}, errors.New("tail failed")
			}
			return combinedExtraction{Entities: []extractedItem{{Slug: "entity/a", Name: "a"}}}, nil
		},
	)
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

func TestWikiExtractionSplitsAtChapterBoundaries(t *testing.T) {
	chapters := []string{
		"## 清准战争\n\n" + strings.Repeat("先讨论清准战争。\n\n", 420),
		"## 土尔扈特东归\n\n" + strings.Repeat("然后讨论东归经过。\n\n", 420),
		"## 新疆建省\n\n" + strings.Repeat("最后讨论建省制度。\n\n", 420),
	}
	parts := splitWikiExtractionDocument(strings.Join(chapters, ""))
	if len(parts) != len(chapters) {
		t.Fatalf("expected one segment per chapter, got %d", len(parts))
	}
	for i := range chapters {
		if parts[i] != chapters[i] {
			t.Fatalf("chapter %d was split or mixed with a neighbor", i)
		}
	}
}

func TestWikiExtractionPreservesParagraphsAndTail(t *testing.T) {
	first := strings.Repeat("甲", 6000) + "\n\n"
	second := strings.Repeat("乙", 6000) + "建省收尾"
	parts := splitWikiExtractionDocument(first + second)
	if len(parts) != 2 || parts[0] != first || !strings.HasSuffix(parts[1], second) {
		t.Fatalf("paragraph or tail lost: parts=%d", len(parts))
	}
	if !strings.HasPrefix(parts[1], strings.Repeat("甲", wikiExtractionOverlap-2)) {
		t.Fatal("paragraph continuation lost overlap context")
	}
	for _, part := range parts {
		if len([]rune(part)) > wikiExtractionMaxRunes {
			t.Fatal("extraction input exceeds the smaller budget")
		}
	}
}

func TestWikiExtractionIgnoresHeadingsInsideFences(t *testing.T) {
	chapter := "## 正文\n\n" + strings.Repeat(
		"序",
		2500,
	) + "\n\n```markdown\n## 代码里的标题\n\n" + strings.Repeat(
		"码",
		2500,
	) + "\n```\n\n"
	tail := "## 下一章\n\n" + strings.Repeat("尾", 5000)
	parts := splitWikiExtractionDocument(chapter + tail)
	if len(parts) != 2 || parts[0] != chapter || parts[1] != tail {
		t.Fatal("a code-fence heading was treated as a chapter boundary")
	}
}

func TestWikiExtractionMergesNamesAcrossSegmentsWithoutLosingEvidence(t *testing.T) {
	calls := 0
	got, err := extractWikiSegments(
		context.Background(),
		strings.Repeat("文", wikiExtractionMaxRunes+100),
		1,
		func(context.Context, string) (combinedExtraction, error) {
			calls++
			if calls == 1 {
				return combinedExtraction{
					Entities: []extractedItem{
						{
							Name:         "孔子",
							Slug:         "entity/kong-zi",
							Aliases:      []string{"仲尼"},
							Details:      "生于鲁国。",
							SourceChunks: []string{"early"},
						},
					},
				}, nil
			}
			return combinedExtraction{
				Entities: []extractedItem{
					{
						Name:         "孔 子",
						Slug:         "entity/confucius",
						Aliases:      []string{"孔丘"},
						Details:      "后世尊为先师。",
						SourceChunks: []string{"late", "early"},
					},
				},
			}, nil
		},
	)
	if err != nil || len(got.Entities) != 1 {
		t.Fatalf("same subject fragmented: %#v, %v", got, err)
	}
	p := got.Entities[0]
	if p.Slug != "entity/kong-zi" || len(p.Aliases) != 3 || len(p.SourceChunks) != 2 ||
		!strings.Contains(p.Details, "生于鲁国") ||
		!strings.Contains(p.Details, "后世尊为先师") {
		t.Fatalf("cross-segment information lost: %#v", p)
	}
}

func TestWikiExtractionDoesNotMergePinyinCollisions(t *testing.T) {
	calls := 0
	got, err := extractWikiSegments(
		context.Background(),
		strings.Repeat("文", wikiExtractionMaxRunes+100),
		1,
		func(context.Context, string) (combinedExtraction, error) {
			calls++
			if calls == 1 {
				return combinedExtraction{
					Entities: []extractedItem{{Name: "伊犁", Slug: "entity/yi-li", SourceChunks: []string{"geography"}}},
				}, nil
			}
			return combinedExtraction{
				Entities: []extractedItem{{Name: "仪礼", Slug: "entity/yi-li", SourceChunks: []string{"book"}}},
			}, nil
		},
	)
	if err != nil || len(got.Entities) != 2 {
		t.Fatalf("different subjects were merged: %#v, %v", got, err)
	}
	if got.Entities[0].Slug == got.Entities[1].Slug || len(got.Entities[0].SourceChunks) != 1 ||
		len(got.Entities[1].SourceChunks) != 1 {
		t.Fatalf("identity or evidence contaminated: %#v", got.Entities)
	}
}

func TestWikiExtractionKeepsPersonAndWorkSeparate(t *testing.T) {
	result, err := extractWikiSegments(
		context.Background(),
		"原文",
		1,
		func(context.Context, string) (combinedExtraction, error) {
			return combinedExtraction{Entities: []extractedItem{
				{Name: "文王", Slug: "entity/wen-wang", Aliases: []string{"周文王"}},
				{Name: "《文王》", Slug: "entity/wen-wang", Aliases: []string{"文王"}},
			}}, nil
		},
	)
	if err != nil || len(result.Entities) != 2 || result.Entities[0].Slug == result.Entities[1].Slug {
		t.Fatalf("person and same-name poem were merged: %#v %v", result, err)
	}
}

func TestWikiExtractionMergesExplicitAliasesButNotSharedCategories(t *testing.T) {
	result, err := extractWikiSegments(
		context.Background(),
		"原文",
		1,
		func(context.Context, string) (combinedExtraction, error) {
			return combinedExtraction{Entities: []extractedItem{
				{Name: "左宗棠", Slug: "entity/zuo-zong-tang", Aliases: []string{"左季高", "清朝官员"}, Details: "主持西征。"},
				{Name: "左季高", Slug: "entity/zuo-ji-gao", Aliases: []string{"左宗棠"}, Details: "筹办粮饷。"},
				{Name: "李鸿章", Slug: "entity/li-hong-zhang", Aliases: []string{"清朝官员"}, Details: "主张海防。"},
			}}, nil
		},
	)
	if err != nil || len(result.Entities) != 2 || !strings.Contains(result.Entities[0].Details, "筹办粮饷") ||
		result.Entities[1].Name != "李鸿章" {
		t.Fatalf("alias consolidation crossed subject boundaries: %#v %v", result, err)
	}
}

func TestWikiExtractionParallelKeepsDocumentOrderAndEvidence(t *testing.T) {
	content := "## 甲\n\n" + strings.Repeat(
		"甲",
		4500,
	) + "\n\n## 乙\n\n" + strings.Repeat(
		"乙",
		4500,
	) + "\n\n## 丙\n\n" + strings.Repeat(
		"丙",
		4500,
	)
	if n := len(splitWikiExtractionDocument(content)); n != 3 {
		t.Fatalf("segments=%d", n)
	}
	gates := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	close(gates[2])
	started := make(chan int, 3)
	finished := make(chan int, 3)
	var active, peak atomic.Int32
	type outcome struct {
		result combinedExtraction
		err    error
	}
	done := make(chan outcome, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		result, err := extractWikiSegments(
			ctx,
			content,
			2,
			func(ctx context.Context, segment string) (combinedExtraction, error) {
				index := 0
				if strings.HasPrefix(segment, "## 乙") {
					index = 1
				}
				if strings.HasPrefix(segment, "## 丙") {
					index = 2
				}
				current := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); current > old; old = peak.Load() {
					if peak.CompareAndSwap(old, current) {
						break
					}
				}
				started <- index
				select {
				case <-gates[index]:
				case <-ctx.Done():
					return combinedExtraction{}, ctx.Err()
				}
				label := []string{"甲", "乙", "丙"}[index]
				finished <- index
				return combinedExtraction{
					Entities: []extractedItem{
						{
							Name:         "左宗棠",
							Slug:         "entity/zuo-" + label,
							Aliases:      []string{label},
							Details:      label + "独有事实。",
							SourceChunks: []string{label},
						},
					},
					Concepts: []extractedItem{
						{
							Name:         "边防",
							Slug:         "concept/bian-" + label,
							Details:      label + "概念事实。",
							SourceChunks: []string{label},
						},
					},
				}, nil
			},
		)
		done <- outcome{result, err}
	}()
	next := func(ch <-chan int) int {
		t.Helper()
		select {
		case value := <-ch:
			return value
		case <-time.After(5 * time.Second):
			t.Fatal("parallel extraction stalled")
			return -1
		}
	}
	seen := map[int]bool{next(started): true}
	seen[next(started)] = true
	if !seen[0] || !seen[1] {
		t.Fatalf("initial workers=%v", seen)
	}
	select {
	case extra := <-started:
		t.Fatalf("limit exceeded by worker %d", extra)
	default:
	}
	close(gates[1])
	if next(finished) != 1 || next(started) != 2 || next(finished) != 2 {
		t.Fatal("could not complete later segments before the first")
	}
	close(gates[0])
	var got outcome
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("extraction did not finish")
	}
	if got.err != nil || len(got.result.Entities) != 1 || len(got.result.Concepts) != 1 {
		t.Fatalf("merge failed: %#v", got)
	}
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency=%d", peak.Load())
	}
	entity := got.result.Entities[0]
	if entity.Slug != "entity/zuo-甲" || len(entity.SourceChunks) != 3 || len(entity.Aliases) < 3 {
		t.Fatalf("identity or evidence changed: %#v", entity)
	}
	for _, item := range []extractedItem{entity, got.result.Concepts[0]} {
		a, b, c := strings.Index(item.Details, "甲"), strings.Index(item.Details, "乙"), strings.Index(item.Details, "丙")
		if a < 0 || b <= a || c <= b {
			t.Fatalf("facts were lost or reordered: %q", item.Details)
		}
	}
}

func TestWikiExtractionParallelFailureCancelsSiblingsWithoutPartialSuccess(t *testing.T) {
	content := "## 甲\n\n" + strings.Repeat(
		"甲",
		4500,
	) + "\n\n## 乙\n\n" + strings.Repeat(
		"乙",
		4500,
	) + "\n\n## 丙\n\n" + strings.Repeat(
		"丙",
		4500,
	)
	firstStarted := make(chan struct{})
	firstCancelled := make(chan struct{})
	failure := errors.New("second segment failed")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := extractWikiSegments(
		ctx,
		content,
		2,
		func(ctx context.Context, segment string) (combinedExtraction, error) {
			if strings.HasPrefix(segment, "## 甲") {
				close(firstStarted)
				<-ctx.Done()
				close(firstCancelled)
				return combinedExtraction{}, ctx.Err()
			}
			if strings.HasPrefix(segment, "## 乙") {
				select {
				case <-firstStarted:
				case <-ctx.Done():
					return combinedExtraction{}, ctx.Err()
				}
				return combinedExtraction{}, failure
			}
			t.Error("a queued segment was called after failure")
			return combinedExtraction{}, nil
		},
	)
	if !errors.Is(err, failure) || len(result.Entities) != 0 || len(result.Concepts) != 0 {
		t.Fatalf("partial success or lost root error: %#v %v", result, err)
	}
	select {
	case <-firstCancelled:
	default:
		t.Fatal("failed segment did not cancel its running sibling")
	}
}

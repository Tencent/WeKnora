package worker

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/outline"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

func TestParseLLMJSONResponseSupportsFencedAndProseWrappedJSON(t *testing.T) {
	for _, response := range []string{
		"```json\n{\"title\":\"标题\",\"content\":\"正文\"}\n```",
		"结果如下：{\"title\":\"标题\",\"content\":\"正文\"}谢谢。",
		"<think>先分析，再返回 JSON。</think>\n{\"title\":\"标题\",\"content\":\"正文\"}",
	} {
		var output generatedContent
		if err := parseLLMJSONResponse(response, &output); err != nil {
			t.Fatalf("parseLLMJSONResponse returned error: %v", err)
		}
		if output.Title != "标题" || !strings.Contains(output.Content, "正文") {
			t.Fatalf("unexpected parsed output: %+v", output)
		}
	}
}

func TestBuildDirectContentPromptIncludesTranscriptEvidence(t *testing.T) {
	prompt, err := buildDirectContentPrompt(&model.Video{Title: "视频一", VideoType: "training"}, skill.JobOutline, []transcript.Chunk{{ID: "chunk-1", Index: 0, Content: "原文内容"}})
	if err != nil {
		t.Fatalf("buildDirectContentPrompt returned error: %v", err)
	}
	for _, expected := range []string{"视频一", "training", "chunk-1", "原文内容", "章节导航", "schema_version", "chapter_index", "start_seconds", "knowledge_points", "1～2 个", "短标题", "不要输出 Markdown 代码围栏"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt does not contain %q: %s", expected, prompt)
		}
	}
}

func TestOutlineLLMResponseUsesSchemaV1(t *testing.T) {
	var document outline.Document
	response := `{"schema_version":1,"chapters":[{"chapter_index":1,"chapter_title":"视频引入","start_seconds":0,"end_seconds":60,"chapter_summary":"本章介绍视频主题。","evidence_chunk_ids":["chunk-1"],"knowledge_points":[{"title":"观察场景","seconds":12,"evidence_chunk_ids":["chunk-1"]}]}]}`
	if err := parseLLMJSONResponse(response, &document); err != nil {
		t.Fatalf("parseLLMJSONResponse returned error: %v", err)
	}
	if err := outline.Validate(document, 60, map[string]struct{}{"chunk-1": {}}); err != nil {
		t.Fatalf("outline.Validate returned error: %v", err)
	}
}

func TestBuildDirectContentPromptRejectsOversizedTranscript(t *testing.T) {
	_, err := buildDirectContentPrompt(&model.Video{Title: "视频一"}, skill.JobOutline, []transcript.Chunk{{ID: "chunk-1", Index: 0, Content: strings.Repeat("长文本", 50000)}})
	if err == nil || !strings.Contains(err.Error(), "context limit") {
		t.Fatalf("expected context limit error, got %v", err)
	}
}

func TestDirectContentMetadataUsesStableSourceContract(t *testing.T) {
	content := pageContent("typed_summary", "video-1", "generation-1", "总结正文")
	for _, expected := range []string{"type: typed_summary", "source_video_id: video-1", "transcript_generation: generation-1", "总结正文"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("page content does not contain %q: %s", expected, content)
		}
	}
}

func TestFallbackTitleUsesContentKind(t *testing.T) {
	if got := fallbackTitle("", "视频一", skill.JobSummary); got != "视频一_知识总结" {
		t.Fatalf("fallbackTitle = %q", got)
	}
	if got := fallbackTitle("", "视频一", skill.JobSummaryEnhance); got != "视频一_知识总结" {
		t.Fatalf("fallbackTitle enhancement = %q", got)
	}
}

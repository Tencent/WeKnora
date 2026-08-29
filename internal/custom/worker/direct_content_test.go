package worker

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

func TestParseLLMJSONResponseSupportsFencedAndProseWrappedJSON(t *testing.T) {
	for _, response := range []string{
		"```json\n{\"title\":\"标题\",\"content\":\"正文\"}\n```",
		"结果如下：{\"title\":\"标题\",\"content\":\"正文\"}谢谢。",
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
	for _, expected := range []string{"视频一", "training", "chunk-1", "原文内容", "章节大纲"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt does not contain %q: %s", expected, prompt)
		}
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

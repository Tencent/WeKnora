package worker

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/outline"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

func TestParseLLMJSONResponseSupportsFencedAndProseWrappedJSON(t *testing.T) {
	for _, response := range []string{
		"```json\n{\"title\":\"标题\",\"content\":\"正文\"}\n```",
		"结果如下：{\"title\":\"标题\",\"content\":\"正文\"}谢谢。",
		"<think>先分析，再返回 JSON。</think>\n{\"title\":\"标题\",\"content\":\"正文\"}",
	} {
		var output map[string]string
		if err := parseLLMJSONResponse(response, &output); err != nil {
			t.Fatalf("parseLLMJSONResponse returned error: %v", err)
		}
		if output["title"] != "标题" || !strings.Contains(output["content"], "正文") {
			t.Fatalf("unexpected parsed output: %+v", output)
		}
	}
}

func TestSummaryOutputRejectsFencedAndProseWrappedJSON(t *testing.T) {
	for _, response := range []string{
		"```json\n{}\n```",
		"结果如下：{}",
	} {
		if _, err := summary.Parse(response); err == nil {
			t.Fatalf("summary.Parse accepted non-JSON response: %q", response)
		}
	}
}

func TestSummaryLLMResponseStripsReasoningBeforeStrictValidation(t *testing.T) {
	for _, response := range []string{
		`<think>先分析，再返回 JSON。</think>
{"schemaVersion":1,"videoType":"general","sections":[]}`,
		`<think>推理中断，仍然返回结果
{"schemaVersion":1,"videoType":"general","sections":[]}`,
		`<think>推理中包含示例 {"invalid": true}</think>
{"schemaVersion":1,"videoType":"general","sections":[]}`,
	} {
		var document summary.Document
		if err := parseLLMJSONResponse(response, &document); err != nil {
			t.Fatalf("parseLLMJSONResponse returned error: %v", err)
		}
		if document.SchemaVersion != 1 || document.VideoType != "general" {
			t.Fatalf("unexpected summary document: %+v", document)
		}
	}
}

func TestParseLLMJSONResponseRejectsHTMLWithoutJSON(t *testing.T) {
	var output map[string]string
	if err := parseLLMJSONResponse("<!DOCTYPE html><html><body>gateway error</body></html>", &output); err == nil {
		t.Fatal("parseLLMJSONResponse accepted HTML without JSON")
	}
}

func TestBuildDirectContentPromptIncludesTranscriptEvidence(t *testing.T) {
	prompt, err := buildDirectContentPrompt(&model.Video{Title: "视频一", VideoType: "training"}, skill.JobOutline, []transcript.Chunk{{ID: "chunk-1", Index: 0, Content: "原文内容"}})
	if err != nil {
		t.Fatalf("buildDirectContentPrompt returned error: %v", err)
	}
	for _, expected := range []string{"视频一", "training", "chunk-1", "原文内容", "章节导航", "schema_version", "chapter_index", "start_seconds", "knowledge_points", "4～8 章", "1～2 个", "短标题", "不要拼接分块序号", "不要输出 Markdown 代码围栏"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt does not contain %q: %s", expected, prompt)
		}
	}
}

func TestBuildDirectContentPromptIncludesSummaryFrameworkAndJSONContract(t *testing.T) {
	prompt, err := buildDirectContentPrompt(&model.Video{Title: "视频一", VideoType: "training"}, skill.JobSummary, []transcript.Chunk{{ID: "chunk-1", Index: 0, Content: "原文内容"}})
	if err != nil {
		t.Fatalf("buildDirectContentPrompt returned error: %v", err)
	}
	for _, expected := range []string{"schemaVersion", "videoType", "evidenceChunkIds", "一、目标与受众", "六、练习与应用", "不要输出 Markdown"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("summary prompt does not contain %q: %s", expected, prompt)
		}
	}
}

func TestSummaryContractResolvesEvidenceFromTranscriptChunks(t *testing.T) {
	document := summary.Document{
		SchemaVersion: 1,
		VideoType:     "general",
		Sections: []summary.Section{{
			ID: "positioning-problem", Title: "一、定位与问题",
			Blocks: []summary.Block{{ID: "block-1", Kind: summary.BlockKindParagraph, Text: "观点", EvidenceChunkIDs: []string{"chunk-1"}}},
		}},
	}
	for _, section := range []summary.FrameworkSection{
		{ID: "claims-reasoning", Title: "二、主张与论证"},
		{ID: "evidence-cases", Title: "三、证据与案例"},
		{ID: "limitations-counterarguments", Title: "四、限定与反方"},
		{ID: "impact-recommendations", Title: "五、影响与建议"},
	} {
		document.Sections = append(document.Sections, summary.Section{ID: section.ID, Title: section.Title, Blocks: []summary.Block{{ID: section.ID + "-block", Kind: summary.BlockKindParagraph, Text: "内容", EvidenceChunkIDs: []string{"chunk-1"}}}})
	}
	chunks := []transcript.Chunk{{ID: "chunk-1", StartMs: 605000, EndMs: 620500, Content: "## 视频定位信息\n\n## 原文\n\n我们当时决定停止旧产品。"}}
	if err := summary.Validate(document, "general", map[string]struct{}{"chunk-1": {}}); err != nil {
		t.Fatalf("summary.Validate returned error: %v", err)
	}
	if err := summary.ResolveEvidence(&document, chunks); err != nil {
		t.Fatalf("summary.ResolveEvidence returned error: %v", err)
	}
	if got := document.Sections[0].Blocks[0].Evidence[0].Timestamp; got != "10:05–10:20" {
		t.Fatalf("unexpected evidence timestamp: %s", got)
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

func TestNormalizeOutlineEvidenceChunkIDs(t *testing.T) {
	document := outline.Document{
		SchemaVersion: 1,
		Chapters: []outline.Chapter{{
			EvidenceChunkIDs: []string{"chunk-1|000004"},
			KnowledgePoints:  []outline.KnowledgePoint{{EvidenceChunkIDs: []string{"chunk-1|000004"}}},
		}},
	}
	normalizeOutlineEvidenceChunkIDs(&document, []transcript.Chunk{{ID: "chunk-1", Index: 4}})
	if document.Chapters[0].EvidenceChunkIDs[0] != "chunk-1" || document.Chapters[0].KnowledgePoints[0].EvidenceChunkIDs[0] != "chunk-1" {
		t.Fatalf("evidence chunk IDs were not normalized: %+v", document)
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

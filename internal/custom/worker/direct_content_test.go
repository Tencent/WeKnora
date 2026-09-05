package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/mps"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/evidence"
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

func TestSummaryRetryPromptRejectsInventedEvidenceIDs(t *testing.T) {
	prompt := "原始提示"
	errorMessage := `validate summary output: summary section "一、目标与受众" references unknown evidence chunk "unknown"`
	retryPrompt := prompt + "\n上一轮总结未通过严格校验，必须修正后重新输出完整 JSON。校验错误：" + errorMessage + "。只能从上文转写分块列表复制 evidenceChunkIds，不得创造、猜测或引用不存在的 ID；可以使用纯知识 ID或带 |分片序号的显示 ID，系统会归一化。"
	for _, expected := range []string{"上一轮总结未通过严格校验", "unknown", "不得创造、猜测或引用不存在的 ID", "系统会归一化"} {
		if !strings.Contains(retryPrompt, expected) {
			t.Fatalf("retry prompt does not contain %q: %s", expected, retryPrompt)
		}
	}
}

func TestOutlineRetryPromptIncludesContractCorrections(t *testing.T) {
	prompt := outlineRetryPrompt("原始提示", errors.New("validate outline output: chapter 2 overlaps previous chapter"))
	for _, expected := range []string{"上一轮章节导航未通过严格校验", "schema_version", "evidence_chunk_ids", "不得重叠", "覆盖最后一个有效转写时间点"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("outline retry prompt does not contain %q: %s", expected, prompt)
		}
	}
}

func TestCompleteOutlineChaptersExtractsOnlyClosedChapterObjects(t *testing.T) {
	raw := `{"schema_version":1,"chapters":[{"chapter_index":1,"chapter_title":"第一章","start_seconds":0,"end_seconds":10,"chapter_summary":"内容","knowledge_points":[{"title":"重点","seconds":3}]},{"chapter_index":2,"chapter_title":"第二章","start_seconds":10,"end_seconds":20,"chapter_summary":"内容","knowledge_points":[{"title":"重点"}`
	chapters := completeOutlineChapters(raw)
	if len(chapters) != 1 || chapters[0].ChapterIndex != 1 {
		t.Fatalf("chapters = %#v", chapters)
	}
}

func TestBuildDirectContentPromptIncludesTranscriptEvidence(t *testing.T) {
	prompt, err := buildDirectContentPrompt(&model.Video{Title: "视频一", VideoType: "training"}, skill.JobOutline, []transcript.Chunk{{ID: "chunk-1", Index: 0, Content: "原文内容"}})
	if err != nil {
		t.Fatalf("buildDirectContentPrompt returned error: %v", err)
	}
	for _, expected := range []string{"视频一", "training", "chunk-1", "EVIDENCE_SENTENCE_ID", "原文内容", "章节导航", "schema_version", "chapter_index", "start_seconds", "knowledge_points", "4～8 章", "1～2 个", "短标题", "不要拼接分块序号", "不要输出 Markdown 代码围栏"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt does not contain %q: %s", expected, prompt)
		}
	}
}

func TestSummaryDraftAndFinalUseSeparateStableSlugs(t *testing.T) {
	video := &model.Video{ID: "video-1", TranscriptGeneration: "generation-1"}
	contract, ok := skill.Contract(skill.JobSummary)
	if !ok {
		t.Fatal("summary contract is not registered")
	}
	finalSlug := contract.WriteSlug(video.ID)
	draftSlug := finalSlug + "/draft"
	if finalSlug == draftSlug || !strings.HasSuffix(draftSlug, "/draft") {
		t.Fatalf("draft and final slugs are not separated: final=%q draft=%q", finalSlug, draftSlug)
	}
	finalContent := pageContent(contract.ArtifactType, video.ID, video.TranscriptGeneration, `{"schemaVersion":1}`)
	draftContent := pageContent(contract.ArtifactType, video.ID, video.TranscriptGeneration, `{"schemaVersion":1}`)
	if finalContent != draftContent || !strings.Contains(finalContent, "type: typed_summary") || !strings.Contains(finalContent, "transcript_generation: generation-1") {
		t.Fatalf("draft/final content contract is unstable or missing identity metadata")
	}
}

func TestSummaryEnhancementInstructionKeepsStructureAndGrounding(t *testing.T) {
	instruction := enhancementInstruction(true)
	if !strings.Contains(instruction, "知识底座") || !strings.Contains(instruction, "转写共同证明") || !strings.Contains(instruction, "section") {
		t.Fatalf("enhancement instruction is missing grounding or structure constraints: %s", instruction)
	}
}

func TestDraftReferenceCannotOverwriteNewTranscriptGeneration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}))
	require.NoError(t, db.Create(&model.Video{ID: "video-1", Title: "视频", TranscriptGeneration: "generation-2"}).Error)

	h := &DirectContentHandler{DB: db}
	require.NoError(t, h.persistDraftReference(context.Background(), "video-1", "generation-1", "summary_draft_wiki_page_id", "summary_result_stage", "old-page", "old-job"))

	var video model.Video
	require.NoError(t, db.First(&video, "id = ?", "video-1").Error)
	require.Empty(t, video.SummaryDraftWikiPageID)
	require.Empty(t, video.SummaryResultStage)
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

func TestSummaryPromptCoversEverySupportedVideoType(t *testing.T) {
	for videoType, framework := range map[string][]string{
		"interview": {"一、人物背景", "六、反思与边界"},
		"training":  {"一、目标与受众", "六、练习与应用"},
		"salon":     {"一、活动与参与者", "六、探索方向"},
		"general":   {"一、定位与问题", "五、影响与建议"},
	} {
		prompt, err := buildDirectContentPrompt(&model.Video{Title: "视频一", VideoType: videoType}, skill.JobSummary, []transcript.Chunk{{ID: "chunk-1", Index: 0, Content: "原文内容"}})
		if err != nil {
			t.Fatalf("buildDirectContentPrompt(%s) returned error: %v", videoType, err)
		}
		for _, title := range framework {
			if !strings.Contains(prompt, title) {
				t.Fatalf("summary prompt for %s does not contain %q", videoType, title)
			}
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
	chunks := []transcript.Chunk{{ID: "chunk-1", EvidenceSentenceID: "evs:v1:chunk-1", StartMs: 605000, EndMs: 620500, Content: "## 视频定位信息\n\n## 原文\n\n我们当时决定停止旧产品。"}}
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

func TestNormalizeOutlineEvidenceChunkIDsUsesSourceSentenceID(t *testing.T) {
	document := outline.Document{
		SchemaVersion: 1,
		Chapters: []outline.Chapter{{
			EvidenceChunkIDs: []string{"mps:fixed:000000"},
			KnowledgePoints:  []outline.KnowledgePoint{{EvidenceChunkIDs: []string{"mps:fixed:000000"}}},
		}},
	}
	normalizeOutlineEvidenceChunkIDs(&document, []transcript.Chunk{{ID: "knowledge-1", SourceSentenceID: "mps:fixed:000000", Index: 0}})
	if document.Chapters[0].EvidenceChunkIDs[0] != "knowledge-1" || document.Chapters[0].KnowledgePoints[0].EvidenceChunkIDs[0] != "knowledge-1" {
		t.Fatalf("source evidence chunk IDs were not normalized: %+v", document)
	}
}

func TestEvidenceRecordsToTranscriptChunksPreservesOutlineInputs(t *testing.T) {
	records := []evidence.Record{
		{KnowledgeID: "knowledge-1", EvidenceSentenceID: "evs:v1:one", SourceSentenceID: "s1", SpeakerID: "speaker-1", ChunkIndex: 0, Text: "第一句", StartMs: 100, EndMs: 1200},
		{KnowledgeID: "knowledge-2", EvidenceSentenceID: "evs:v1:two", SourceSentenceID: "s2", ChunkIndex: 1, Text: "第二句", StartMs: 1200, EndMs: 2400},
	}
	chunks := evidenceRecordsToTranscriptChunks(records)
	if len(chunks) != 2 || chunks[0].ID != "knowledge-1" || chunks[0].EvidenceSentenceID != "evs:v1:one" || chunks[1].StartMs != 1200 || chunks[1].Content != "第二句" {
		t.Fatalf("chunks = %#v", chunks)
	}
}

func TestReadDraftChunksUsesFixedMPSTaskEvidence(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.VideoProcessingJob{}))
	videoID := "video-existing"
	// Use a synthetic ID so a local/cloud MPS task can never be mistaken for
	// a production binding or copied into a deployment configuration.
	taskID := "mps-test-task-id"
	payload, err := json.Marshal(map[string]any{"mps_result": mps.Result{Segments: []mps.Segment{
		{SourceSegmentID: "mps:fixed:000000", Text: "开场说明", StartMs: 123, EndMs: 19803, SpeakerID: "speaker-1"},
		{SourceSegmentID: "mps:fixed:000001", Text: "方法介绍", StartMs: 19803, EndMs: 30200, SpeakerID: "speaker-1"},
	}}})
	require.NoError(t, err)
	source := model.VideoProcessingJob{ID: "transcription-fixed", VideoID: videoID, JobType: "transcription", ExternalTaskID: taskID, ResultPayload: string(payload)}
	require.NoError(t, db.Create(&source).Error)
	job := &model.VideoProcessingJob{VideoID: videoID, InputPayload: `{"transcription_job_id":"transcription-fixed"}`}
	chunks, err := (&DirectContentHandler{DB: db}).readDraftChunks(context.Background(), videoID, "mps:fixed-generation", job)
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	require.Equal(t, taskID, source.ExternalTaskID)
	require.Equal(t, "evs:v1:", chunks[0].EvidenceSentenceID[:len("evs:v1:")])
	require.Equal(t, 123, chunks[0].StartMs)
	require.Equal(t, 30200, chunks[1].EndMs)
}

func TestSummaryDraftReadsMPSResultWithoutEvidenceIndex(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoProcessingJob{}))
	video := &model.Video{ID: "video-existing", Title: "证据总结", VideoType: "training", TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(video).Error)
	payload, err := json.Marshal(map[string]any{"mps_result": mps.Result{Segments: []mps.Segment{
		{SourceSegmentID: "mps:test:000000", Text: "开场说明", StartMs: 123, EndMs: 19803, SpeakerID: "speaker-1"},
		{SourceSegmentID: "mps:test:000001", Text: "方法介绍", StartMs: 19803, EndMs: 30200, SpeakerID: "speaker-1"},
	}}})
	require.NoError(t, err)
	source := model.VideoProcessingJob{ID: "transcription-test", VideoID: video.ID, JobType: "transcription", ExternalTaskID: "mps-test-task", ResultPayload: string(payload)}
	require.NoError(t, db.Create(&source).Error)
	h := &DirectContentHandler{DB: db, Job: skill.JobSummary}
	job := &model.VideoProcessingJob{VideoID: video.ID, ResultStage: "draft", InputPayload: `{"transcription_job_id":"transcription-test"}`}
	chunks, err := h.readContentChunks(context.Background(), video, video.TranscriptGeneration, job)
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	require.Equal(t, "mps:test:000000", chunks[0].ID)
	require.NotEmpty(t, chunks[0].EvidenceSentenceID)
	require.Equal(t, "开场说明", transcript.OriginalText(chunks[0].Content))
	require.Equal(t, 30200, chunks[1].EndMs)
}

func TestSummaryFinalReadsCurrentEvidenceIndex(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoProcessingJob{}))
	video := &model.Video{ID: "video-existing", Title: "证据总结", VideoType: "training", TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(video).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptChunk{
		VideoID: video.ID, Generation: video.TranscriptGeneration, Revision: 1, ChunkIndex: 0,
		KnowledgeID: "knowledge-1", EvidenceSentenceID: "evs:v1:first", SourceSegmentID: "s1",
		StartMs: 100, EndMs: 1200, ContentHash: "hash-1", Status: "completed",
	}).Error)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/chunks/knowledge-1") {
			payload, _ := json.Marshal(map[string]any{"success": true, "data": []weknora.KnowledgeChunk{{ID: "chunk-1", KnowledgeID: "knowledge-1", Content: evidenceContentForSummary("evs:v1:first", "s1", "generation-1", 100, 1200, "证据原文"), ChunkIndex: 0}}, "total": 1})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := weknora.New(config.WeKnoraConfig{BaseURL: server.URL, KBID: "kb-1"})
	h := &DirectContentHandler{DB: db, WeKnora: client, Job: skill.JobSummary}
	job := &model.VideoProcessingJob{VideoID: video.ID, ResultStage: "final"}
	chunks, err := h.readContentChunks(context.Background(), video, video.TranscriptGeneration, job)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, "knowledge-1", chunks[0].ID)
	require.Equal(t, "evs:v1:first", chunks[0].EvidenceSentenceID)
	require.Equal(t, "证据原文", transcript.OriginalText(chunks[0].Content))
}

func evidenceContentForSummary(id, source, generation string, start, end int, text string) string {
	return fmt.Sprintf("## 视频定位信息\n\n```json\n{\"start_ms\":%d,\"end_ms\":%d,\"sentence_id\":\"%s\",\"evidence_sentence_id\":\"%s\",\"transcript_generation\":\"%s\"}\n```\n\n## 原文\n\n%s", start, end, source, id, generation, text)
}

func TestBuildDirectContentPromptRejectsOversizedTranscript(t *testing.T) {
	_, err := buildDirectContentPrompt(&model.Video{Title: "视频一"}, skill.JobOutline, []transcript.Chunk{{ID: "chunk-1", Index: 0, Content: strings.Repeat("长文本", 50000)}})
	if err == nil || !strings.Contains(err.Error(), "context limit") {
		t.Fatalf("expected context limit error, got %v", err)
	}
}

func TestBuildDirectContentPromptAcceptsLongTranscriptWithoutRepeatedMetadata(t *testing.T) {
	chunks := make([]transcript.Chunk, 0, 961)
	for index := 0; index < 961; index++ {
		chunks = append(chunks, transcript.Chunk{
			ID:      fmt.Sprintf("knowledge-%04d", index),
			Index:   index,
			StartMs: index * 5600,
			EndMs:   (index + 1) * 5600,
			Content: "## 视频定位信息\n\n```json\n" + strings.Repeat("重复定位信息", 60) + "\n```\n\n## 原文\n\n一句原文。",
		})
	}

	prompt, err := buildDirectContentPrompt(&model.Video{Title: "长视频", VideoType: "training"}, skill.JobOutline, chunks)
	if err != nil {
		t.Fatalf("buildDirectContentPrompt rejected compressible transcript: %v", err)
	}
	for _, expected := range []string{"knowledge-0000", "knowledge-0960", "一句原文。"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt does not contain %q", expected)
		}
	}
	if strings.Contains(prompt, "重复定位信息") {
		t.Fatal("prompt retained repeated transcript metadata")
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

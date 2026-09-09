package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/contentprovenance"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	customknowledge "github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgegraph"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	transcriptservice "github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

type sourceReaderStub struct {
	value weknora.ManualKnowledgeResult
}

func TestAuthenticatedProductionJobRequiresPersistedIdentityMatch(t *testing.T) {
	video := &model.Video{ID: "video-1", TranscriptGeneration: "generation-1"}
	job := &model.VideoProcessingJob{
		ID: "job-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, JobType: skill.JobGraph,
	}

	productionJob, err := authenticatedProductionJob(job, video, skill.JobGraph, video.TranscriptGeneration)
	require.NoError(t, err)
	require.Equal(t, "job-1", productionJob.TaskID)

	for _, mutate := range []func(*model.VideoProcessingJob){
		func(value *model.VideoProcessingJob) { value.ID = "" },
		func(value *model.VideoProcessingJob) { value.VideoID = "video-2" },
		func(value *model.VideoProcessingJob) { value.TranscriptGeneration = "generation-2" },
		func(value *model.VideoProcessingJob) { value.JobType = skill.JobSummary },
	} {
		changed := *job
		mutate(&changed)
		_, err := authenticatedProductionJob(&changed, video, skill.JobGraph, video.TranscriptGeneration)
		require.Error(t, err)
	}
}

func (s sourceReaderStub) GetKnowledge(_ context.Context, _ string) (weknora.ManualKnowledgeResult, error) {
	return s.value, nil
}

type countingAgentClient struct {
	calls      int
	triggerErr error
	queries    []string
	titles     []string
	onTrigger  func(query string)
}

type graphProjectionStub struct {
	video *model.Video
	page  *weknora.WikiPage
}

func TestWrapWikiArtifactWaitErrorPreservesContentContractFailure(t *testing.T) {
	err := wrapWikiArtifactWaitError(
		"knowledge_base",
		errors.New("P3 knowledge object validation failed: object-1: core content is required"),
	)
	require.Contains(t, err.Error(), "Wiki 产物页未通过验收")
	require.NotContains(t, err.Error(), "超时")
	category, code := ClassifyProcessingError(err)
	require.Equal(t, ErrorCategoryWikiArtifact, category)
	require.Equal(t, "content_contract_failed", code)
}

func (s *graphProjectionStub) ProjectVideo(_ context.Context, video *model.Video, page *weknora.WikiPage) error {
	s.video = video
	s.page = page
	return nil
}

func (s *graphProjectionStub) ProjectKnowledgeBase(context.Context) error { return nil }

func (s *graphProjectionStub) Query(context.Context, knowledgegraph.Query) (*knowledgegraph.Graph, error) {
	return &knowledgegraph.Graph{}, nil
}

func (s *graphProjectionStub) Close(context.Context) error { return nil }

func (c *countingAgentClient) CreateSession(_ context.Context, title string) (string, error) {
	c.calls++
	c.titles = append(c.titles, title)
	return "session", nil
}

func (c *countingAgentClient) TriggerSkill(_ context.Context, _ string, _ string, _ string, query string, _ []string, _ *contentprovenance.Job) error {
	c.calls++
	c.queries = append(c.queries, query)
	if c.onTrigger != nil {
		c.onTrigger(query)
	}
	return c.triggerErr
}

func TestWikiBaselinePersistsAcrossJobRetries(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.VideoProcessingJob{}))

	job := model.VideoProcessingJob{ID: "job-1", VideoID: "video-1", JobType: skill.JobOutline}
	require.NoError(t, db.Create(&job).Error)

	listCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		listCalls++
		pages := []weknora.WikiPage{}
		if listCalls > 1 {
			pages = []weknora.WikiPage{{ID: "outline-page", Slug: "outline/video-1", Content: "video-1", Version: 1}}
		}
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
			Pages: pages, Total: len(pages), Page: 1, PageSize: 100, TotalPages: 1,
		})
	}))
	defer server.Close()

	wikiClient := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	handler := BaseSkillHandler{
		DB:           db,
		Orchestrator: skill.NewOrchestrator(db, wikiClient, "kb-1"),
	}

	firstBaseline, err := handler.wikiBaseline(t.Context(), &job, job.VideoID)
	require.NoError(t, err)
	require.Empty(t, firstBaseline.Versions)
	require.Equal(t, job.CreatedAt, firstBaseline.JobCreatedAt)
	require.Equal(t, 1, listCalls)

	var stored model.VideoProcessingJob
	require.NoError(t, db.First(&stored, "id = ?", job.ID).Error)
	secondBaseline, err := handler.wikiBaseline(t.Context(), &stored, stored.VideoID)
	require.NoError(t, err)
	require.Empty(t, secondBaseline.Versions)
	require.Equal(t, firstBaseline.JobCreatedAt, secondBaseline.JobCreatedAt)
	require.Equal(t, 1, listCalls)
}

func TestSkillQueryUsesTranscriptKnowledgeIDAsSourceDocument(t *testing.T) {
	video := &model.Video{
		ID:                    "video-1",
		Title:                 "测试视频",
		TranscriptKnowledgeID: "knowledge-1",
	}

	contract, ok := skill.Contract(skill.JobGraph)
	require.True(t, ok)
	query := skillQuery(video, contract, skill.JobGraph)

	require.Contains(t, query, "$extract-video-knowledge")
	require.Contains(t, query, "源文档知识 ID：knowledge-1")
	require.Contains(t, query, "业务视频 ID：video-1")
	require.Contains(t, query, "完整读取 SKILL.md")
	require.Contains(t, query, "references/type-frameworks.md")
	require.Contains(t, query, "references/wiki-schema.md")
	require.Contains(t, query, "references/audit-rules.md")
	require.Contains(t, query, "references/output-examples.md")
	require.Contains(t, query, "file_path 必须包含 references/ 目录")
	require.Contains(t, query, "V2 Skill 只负责五类候选")
	require.Contains(t, query, "复合对象拆解")
	require.Contains(t, query, "一组 evidence_contribution")
	require.Contains(t, query, "不得自行决定最终知识对象 ID、Wiki 页面 ID 或规范 slug")
	require.Contains(t, query, "候选身份只是提案")
	require.Contains(t, query, "写入后必须使用工具返回的 knowledge_object_id、wiki_page_id、规范标题和 slug")
	require.Contains(t, query, "语义不确定或冲突时停止该候选")
	require.Contains(t, query, "证据贡献的源文档只能是 knowledge-1")
	require.Contains(t, query, "证据正文仍只保存在证据知识库")
	require.Contains(t, query, `slug 严格使用 "video/video-1"`)
	require.Contains(t, query, "type: knowledge_base")
	require.Contains(t, query, "title: 测试视频_知识底座")
	require.Contains(t, query, "audit_status: aligned")
	require.Contains(t, query, "索引只引用工具已返回并回读的规范页面")
	require.Contains(t, query, "连续语义窗口")
	require.Contains(t, query, "最终报告不能代替 wiki_write_page 工具调用")
	require.Contains(t, query, "evidence_sentence_ids")
	require.Contains(t, query, "继续处理其他候选")
	require.Contains(t, query, "至少一次成功写入并回读")
	require.Contains(t, query, "不得使用示例、占位内容或 mock 数据")
	require.NotContains(t, query, "每个实体和每个知识原子都要写入独立 Wiki 页面")
	require.NotContains(t, query, "methodology: input、steps、criteria、output、applicability")
	require.NotContains(t, query, "写入唯一产物页")
}

func TestSkillQueryForSummaryEnhancementUsesKnowledgeBase(t *testing.T) {
	video := &model.Video{
		ID: "video-1", Title: "测试视频", TranscriptKnowledgeID: "knowledge-1",
		TranscriptGeneration: "generation-1", KnowledgeBaseWikiPageID: "knowledge-base-1",
	}
	contract, ok := skill.Contract(skill.JobSummaryEnhance)
	require.True(t, ok)

	query := skillQuery(video, contract, skill.JobSummaryEnhance)

	require.Contains(t, query, "知识底座索引页 ID：knowledge-base-1")
	require.Contains(t, query, "不是重新生成基础总结")
	require.Contains(t, query, `slug 严格使用 "typed-summary/video-1"`)
}

func TestGraphIndexSeedContentIsPendingAndGenerationBound(t *testing.T) {
	content := graphIndexSeedContent("video-1", "generation-1", "测试视频")
	page := weknora.WikiPage{Content: content}
	frontmatter := page.ParsedFrontmatter()
	require.Equal(t, "knowledge_base", frontmatter["type"])
	require.Equal(t, "video-1", frontmatter["source_video_id"])
	require.Equal(t, "generation-1", frontmatter["transcript_generation"])
	require.Equal(t, "pending", frontmatter["audit_status"])
	require.Equal(t, "测试视频_知识底座", frontmatter["title"])
	require.Contains(t, content, "待 extract-video-knowledge 完成知识对象抽取与审计后更新")
}

func TestTranscriptKnowledgeIDsUsesEveryCurrentChunk(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}))
	video := model.Video{ID: "video-1", TranscriptGeneration: "generation-1", TranscriptKnowledgeID: "legacy-first"}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create([]model.VideoTranscriptChunk{
		{VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 0, KnowledgeID: "knowledge-1", ContentHash: "hash-1", Status: "completed"},
		{VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 1, KnowledgeID: "knowledge-2", ContentHash: "hash-2", Status: "completed"},
	}).Error)

	handler := BaseSkillHandler{DB: db}
	ids, err := handler.transcriptKnowledgeIDs(t.Context(), &model.VideoProcessingJob{TranscriptGeneration: video.TranscriptGeneration}, &video)
	require.NoError(t, err)
	require.Equal(t, []string{"knowledge-1", "knowledge-2"}, ids)
}

func TestWikiInputFullDocumentValidatesTranscriptChunks(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoTranscriptSource{}))
	video := model.Video{ID: "video-1", Title: "测试视频", DurationSeconds: 20, TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptChunk{
		VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 0,
		EvidenceSentenceID: "e-1", SourceSegmentID: "s-1", SpeakerID: "0", StartMs: 100, EndMs: 1000,
		KnowledgeID: "chunk-1", ContentHash: "chunk-hash", Status: "completed",
	}).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptSource{ID: "binding-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, KnowledgeBaseID: "knowledge-kb", KnowledgeID: "source-1", ContentHash: "hash", Status: transcriptservice.SourceStatusCreated}).Error)
	doc, err := transcriptservice.Build(transcriptservice.Input{
		VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, Title: video.Title, DurationSeconds: video.DurationSeconds,
		Chapters: []transcriptservice.InputChapter{{Index: 0, Title: "开场", Paragraphs: []transcriptservice.InputParagraph{{Index: 0, Sentences: []transcriptservice.InputSentence{{SourceSentenceID: "s-1", EvidenceSentenceID: "e-1", Text: "正文", StartMs: 100, EndMs: 1000}}}}}},
	})
	require.NoError(t, err)
	jsonText, err := doc.JSON()
	require.NoError(t, err)
	handler := BaseSkillHandler{DB: db, KnowledgeBaseID: "knowledge-kb", SourceReader: sourceReaderStub{value: weknora.ManualKnowledgeResult{ID: "source-1", KnowledgeBaseID: "knowledge-kb", Content: transcriptservice.SourceContent(doc, jsonText, "hash")}}}
	job := &model.VideoProcessingJob{ID: "job-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, InputPayload: `{"transcript_input_mode":"full_document","transcript_source_knowledge_id":"source-1"}`}
	input, err := handler.wikiInput(t.Context(), job, &video, skill.JobGraph)
	require.NoError(t, err)
	require.Equal(t, TranscriptInputModeFullDocument, input.Mode)
	require.Equal(t, []string{"source-1"}, input.KnowledgeIDs)
}

func TestGraphRunRejectsSourceEvidenceMismatchBeforeAgent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoTranscriptSource{}))
	video := model.Video{ID: "video-1", Title: "测试视频", DurationSeconds: 20, TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptChunk{
		VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 0,
		EvidenceSentenceID: "evs:v1:active", SourceSegmentID: "s-1", SpeakerID: "0", StartMs: 100, EndMs: 1000,
		KnowledgeID: "chunk-1", ContentHash: "chunk-hash", Status: "completed",
	}).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptSource{
		ID: "binding-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration,
		KnowledgeBaseID: "knowledge-kb", KnowledgeID: "source-1", ContentHash: "source-hash", Status: transcriptservice.SourceStatusCreated,
	}).Error)
	doc, err := transcriptservice.Build(transcriptservice.Input{
		VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, Title: video.Title, DurationSeconds: video.DurationSeconds,
		Chapters: []transcriptservice.InputChapter{{Index: 0, Title: "开场", Paragraphs: []transcriptservice.InputParagraph{{Index: 0, Sentences: []transcriptservice.InputSentence{{
			SourceSentenceID: "s-1", EvidenceSentenceID: "evs:v1:stale", Text: "正文", StartMs: 100, EndMs: 1000,
		}}}}}},
	})
	require.NoError(t, err)
	jsonText, err := doc.JSON()
	require.NoError(t, err)
	agent := &countingAgentClient{}
	handler := BaseSkillHandler{
		DB: db, AgentClient: agent, KnowledgeBaseID: "knowledge-kb",
		SourceReader: sourceReaderStub{value: weknora.ManualKnowledgeResult{
			ID: "source-1", KnowledgeBaseID: "knowledge-kb", Content: transcriptservice.SourceContent(doc, jsonText, "source-hash"),
		}},
	}
	job := &model.VideoProcessingJob{
		ID: "job-1", VideoID: video.ID, JobType: skill.JobGraph, TranscriptGeneration: video.TranscriptGeneration,
		InputPayload: `{"transcript_input_mode":"full_document","transcript_source_knowledge_id":"source-1"}`,
	}

	err = handler.run(t.Context(), job, &video, skill.JobGraph)
	require.ErrorContains(t, err, transcriptservice.SourceValidationEvidence)
	category, code := ClassifyProcessingError(err)
	require.Equal(t, ErrorCategoryResponseParse, category)
	require.Equal(t, transcriptservice.SourceValidationEvidence, code)
	require.Zero(t, agent.calls)
}

func TestWikiInputFullDocumentRejectsMissingSource(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptSource{}))
	video := model.Video{ID: "video-1", Title: "测试视频", DurationSeconds: 20, TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(&video).Error)
	handler := BaseSkillHandler{DB: db, KnowledgeBaseID: "knowledge-kb"}
	_, err = handler.wikiInput(t.Context(), &model.VideoProcessingJob{VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, InputPayload: `{"transcript_input_mode":"full_document"}`}, &video, skill.JobGraph)
	require.Error(t, err)
	category, code := ClassifyProcessingError(err)
	require.Equal(t, ErrorCategoryResponseParse, category)
	require.Equal(t, "source_missing", code)
}

func TestWikiInputFullDocumentDoesNotReuseLegacyUnownedBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptSource{}))
	video := model.Video{ID: "video-1", DurationSeconds: 20, TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptSource{
		ID: "legacy", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration,
		KnowledgeID: "legacy-source", ContentHash: "hash", Status: transcriptservice.SourceStatusCreated,
	}).Error)
	handler := BaseSkillHandler{DB: db, KnowledgeBaseID: "knowledge-kb"}
	_, err = handler.wikiInput(t.Context(), &model.VideoProcessingJob{
		VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration,
		InputPayload: `{"transcript_input_mode":"full_document","transcript_source_knowledge_id":"legacy-source"}`,
	}, &video, skill.JobGraph)
	require.Error(t, err)
	require.Contains(t, err.Error(), "source_binding_missing")
}

func TestSkillQueryFullDocumentForbidsChunkInput(t *testing.T) {
	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	contract, ok := skill.Contract(skill.JobGraph)
	require.True(t, ok)
	query := skillQueryWithInput(video, contract, skill.JobGraph, "source-1", TranscriptInputModeFullDocument)
	require.Contains(t, query, "完整视频源文档知识 ID：source-1")
	require.Contains(t, query, "不得读取字幕分块知识 ID")
	require.Contains(t, query, "完整读取 SKILL.md")
	require.Contains(t, query, "references/wiki-schema.md")
	require.Contains(t, query, customknowledge.WikiObjectContractVersion)
	require.NotContains(t, query, "完整转写分块清单已通过调用上下文提供")
}

func TestWikiInputGraphRejectsEvidenceChunkMode(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	handler := BaseSkillHandler{DB: db}
	video := &model.Video{ID: "video-1", TranscriptGeneration: "generation-1"}
	_, err = handler.wikiInput(t.Context(), &model.VideoProcessingJob{InputPayload: `{"transcript_input_mode":"evidence_chunks"}`}, video, skill.JobGraph)
	require.Error(t, err)
	_, code := ClassifyProcessingError(err)
	require.Equal(t, "input_mode_invalid", code)
}

func graphSourceReader(t *testing.T, video *model.Video) sourceReaderStub {
	doc, err := transcriptservice.Build(transcriptservice.Input{
		VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration,
		Title: video.Title, DurationSeconds: video.DurationSeconds,
		Chapters: []transcriptservice.InputChapter{{
			Index: 0, Title: "开场",
			Paragraphs: []transcriptservice.InputParagraph{{
				Index: 0,
				Sentences: []transcriptservice.InputSentence{{
					SourceSentenceID: "s-1", EvidenceSentenceID: "e-1",
					Text: "正文", StartMs: 100, EndMs: 1000,
				}},
			}},
		}},
	})
	require.NoError(t, err)
	jsonText, err := doc.JSON()
	require.NoError(t, err)
	return sourceReaderStub{value: weknora.ManualKnowledgeResult{
		ID: "source-1", KnowledgeBaseID: "knowledge-kb",
		Content: transcriptservice.SourceContent(doc, jsonText, "hash"),
	}}
}

func graphTranscriptChunk(video *model.Video) *model.VideoTranscriptChunk {
	return &model.VideoTranscriptChunk{
		VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 0,
		KnowledgeID: "chunk-1", EvidenceSentenceID: "e-1", SourceSegmentID: "s-1",
		SpeakerID: "0", StartMs: 100, EndMs: 1000, ContentHash: "hash", Status: "completed",
	}
}

func p3IndexContent(videoID, generation, title string) string {
	return "---\n" +
		"type: knowledge_base\n" +
		"source_video_id: " + videoID + "\n" +
		"transcript_generation: " + generation + "\n" +
		"title: " + title + "_知识底座\n" +
		"audit_status: aligned\n" +
		"---\n\n# " + title + "_知识底座\n"
}

func p3ConceptContent(videoID, generation string) string {
	return "---\n" +
		"knowledge_object_id: object-1\n" +
		"type: concept\n" +
		"primary_type: concept\n" +
		"source_video_id: " + videoID + "\n" +
		"transcript_generation: " + generation + "\n" +
		"audit_status: passed\n" +
		"information_nature: 概念\n" +
		"classification_confidence: 0.9\n" +
		"core_content: 这是可展示的概念内容。\n" +
		"evidence_ids: [e-1]\n" +
		"source_refs: [source-1]\n" +
		"structure_fields:\n" +
		"  definition: 概念定义\n" +
		"  mechanism: 运行机制\n" +
		"---\n\n# 概念\n\n一句话概述：这是可展示的概念内容。\n"
}

func p3EvidenceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.VideoTranscriptChunk{}))
	require.NoError(t, db.Create(&model.VideoTranscriptChunk{
		VideoID: "video-1", Generation: "generation-1", ChunkIndex: 0,
		KnowledgeID: "chunk-1", EvidenceSentenceID: "e-1", ContentHash: "hash", Status: "completed",
	}).Error)
	return db
}

func p3WikiServer(t *testing.T, pages []weknora.WikiPage) *httptest.Server {
	t.Helper()
	bySlug := make(map[string]weknora.WikiPage, len(pages))
	for _, page := range pages {
		bySlug[page.Slug] = page
	}
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		const listPath = "/api/v1/knowledgebase/knowledge-kb/wiki/pages"
		if request.Method == http.MethodPost && request.URL.Path == listPath {
			var payload struct {
				Slug     string `json:"slug"`
				Title    string `json:"title"`
				PageType string `json:"page_type"`
				Content  string `json:"content"`
			}
			require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
			page := weknora.WikiPage{ID: "created-" + strconv.Itoa(len(pages)+1), Slug: payload.Slug, Title: payload.Title, PageType: payload.PageType, Content: payload.Content, Version: 1}
			pages = append(pages, page)
			bySlug[page.Slug] = page
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(page)
			return
		}
		if request.Method == http.MethodGet && request.URL.Path == listPath {
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages: pages, Total: len(pages), Page: 1, PageSize: 100, TotalPages: 1,
			})
			return
		}
		const pagePrefix = listPath + "/"
		if slug, ok := strings.CutPrefix(request.URL.Path, pagePrefix); ok {
			if page, exists := bySlug[slug]; exists {
				_ = json.NewEncoder(writer).Encode(page)
				return
			}
		}
		http.NotFound(writer, request)
	}))
}

func TestGraphHandlerRejectsUncontractedNativeWiki(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoTranscriptSource{}, &model.VideoProcessingJob{}))
	video := &model.Video{ID: "video-1", Title: "测试视频", DurationSeconds: 20, TranscriptGeneration: "generation-1", SummaryWikiPageID: "summary-1"}
	require.NoError(t, db.Create(video).Error)
	require.NoError(t, db.Create(graphTranscriptChunk(video)).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptSource{ID: "binding-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, KnowledgeBaseID: "knowledge-kb", KnowledgeID: "source-1", ContentHash: "source-hash", Status: "created"}).Error)
	job := &model.VideoProcessingJob{ID: "job-1", VideoID: video.ID, JobType: skill.JobGraph, TranscriptGeneration: video.TranscriptGeneration, InputPayload: `{"transcript_source_knowledge_id":"source-1"}`}
	require.NoError(t, db.Create(job).Error)
	server := p3WikiServer(t, []weknora.WikiPage{
		{ID: "source-page", Slug: "entity/source", PageType: "entity", Content: "source", SourceRefs: []string{"source-1"}},
		{ID: "index-1", Slug: "index", PageType: "index", Content: "index"},
	})
	defer server.Close()
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	orchestrator := skill.NewOrchestrator(db, wiki, "knowledge-kb")
	agent := &countingAgentClient{}
	handler := GraphHandler{BaseSkillHandler: BaseSkillHandler{
		DB: db, AgentClient: agent, SourceReader: graphSourceReader(t, video),
		KnowledgeBaseID: "knowledge-kb", Orchestrator: orchestrator,
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err = handler.Run(ctx, job, video)
	require.Error(t, err)
	require.Equal(t, 4, agent.calls, "a missing P3 artifact must trigger extraction and one bounded contract repair")
	require.Equal(t, []string{
		"content-pipeline/video-1/generation-1/graph/job-1",
		"content-pipeline/video-1/generation-1/graph-contract-repair/job-1",
	}, agent.titles)
	seed, readErr := wiki.GetPage(t.Context(), "knowledge-kb", "video/video-1")
	require.NoError(t, readErr)
	require.NotNil(t, seed)
	require.Equal(t, "pending", seed.ParsedFrontmatter()["audit_status"])
}

func TestGraphHandlerReconcilesCompliantP3WithoutAgent(t *testing.T) {
	db := p3EvidenceDB(t)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}))
	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(video).Error)
	index := weknora.WikiPage{
		ID: "index-1", Slug: "video/video-1", PageType: "index",
		Content: p3IndexContent(video.ID, video.TranscriptGeneration, video.Title), Version: 1,
	}
	object := weknora.WikiPage{
		ID: "object-1", Slug: "concept/object-1", PageType: "index",
		Content: p3ConceptContent(video.ID, video.TranscriptGeneration), Version: 1,
	}
	server := p3WikiServer(t, []weknora.WikiPage{index, object})
	defer server.Close()
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	orchestrator := skill.NewOrchestrator(db, wiki, "knowledge-kb")
	agent := &countingAgentClient{}
	projection := &graphProjectionStub{}
	handler := GraphHandler{BaseSkillHandler: BaseSkillHandler{
		DB: db, AgentClient: agent, KnowledgeBaseID: "knowledge-kb", Orchestrator: orchestrator,
	}, Graph: projection}
	err := handler.Run(t.Context(), &model.VideoProcessingJob{ID: "job-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration}, video)
	require.NoError(t, err)
	require.Zero(t, agent.calls)
	var stored model.Video
	require.NoError(t, db.First(&stored, "id = ?", video.ID).Error)
	require.Equal(t, index.ID, stored.KnowledgeBaseWikiPageID)
	require.Equal(t, "passed", stored.KnowledgeAuditStatus)
	require.NotNil(t, projection.video)
	require.Equal(t, video.ID, projection.video.ID)
	require.NotNil(t, projection.page)
	require.Equal(t, index.ID, projection.page.ID)
}

func TestInspectP3KnowledgeRejectsMultiObjectBatchWithoutFormalRelations(t *testing.T) {
	db := p3EvidenceDB(t)
	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	index := weknora.WikiPage{
		ID: "index-1", Slug: "video/video-1", PageType: "index",
		Content: p3IndexContent(video.ID, video.TranscriptGeneration, video.Title), Version: 1,
	}
	first := weknora.WikiPage{
		ID: "object-1", Slug: "concept/object-1", PageType: "index",
		Content: p3ConceptContent(video.ID, video.TranscriptGeneration), Version: 1,
	}
	secondContent := strings.Replace(
		p3ConceptContent(video.ID, video.TranscriptGeneration),
		"knowledge_object_id: object-1", "knowledge_object_id: object-2", 1,
	)
	secondContent = strings.NewReplacer(
		"这是可展示的概念内容。", "订阅制是按周期持续付费获取产品服务的商业模式。",
		"概念定义", "用户按月或按年支付持续使用费用",
		"运行机制", "服务在付费周期内开放并按期续费",
		"# 概念", "# 订阅制",
	).Replace(secondContent)
	second := weknora.WikiPage{
		ID: "object-2", Slug: "concept/object-2", PageType: "index",
		Content: secondContent, Version: 1,
	}
	server := p3WikiServer(t, []weknora.WikiPage{index, first, second})
	defer server.Close()
	handler := BaseSkillHandler{
		DB: db, KnowledgeBaseID: "knowledge-kb",
		Orchestrator: skill.NewOrchestrator(
			db,
			weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL}),
			"knowledge-kb",
		),
	}

	artifacts, invalid, err := handler.inspectP3Knowledge(t.Context(), video.ID, video.TranscriptGeneration, video.Title)
	require.NoError(t, err)
	require.Nil(t, artifacts)
	require.Contains(t, strings.Join(invalid, "; "), "formal relation")
}

func TestInspectP3KnowledgeRejectsEvidenceOutsideCurrentTranscriptGeneration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.VideoTranscriptChunk{}))
	require.NoError(t, db.Create(&model.VideoTranscriptChunk{
		VideoID: "video-1", Generation: "generation-1", ChunkIndex: 0,
		KnowledgeID: "chunk-current", EvidenceSentenceID: "e-current",
		ContentHash: "hash", Status: "completed",
	}).Error)

	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	index := weknora.WikiPage{
		ID: "index-1", Slug: "video/video-1", PageType: "index",
		Content: p3IndexContent(video.ID, video.TranscriptGeneration, video.Title), Version: 1,
	}
	object := weknora.WikiPage{
		ID: "object-1", Slug: "concept/object-1", PageType: "index",
		Content: p3ConceptContent(video.ID, video.TranscriptGeneration), Version: 1,
	}
	server := p3WikiServer(t, []weknora.WikiPage{index, object})
	defer server.Close()
	handler := BaseSkillHandler{
		DB: db, KnowledgeBaseID: "knowledge-kb",
		Orchestrator: skill.NewOrchestrator(
			db,
			weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL}),
			"knowledge-kb",
		),
	}

	artifacts, invalid, err := handler.inspectP3Knowledge(t.Context(), video.ID, video.TranscriptGeneration, video.Title)
	require.NoError(t, err)
	require.Nil(t, artifacts)
	require.Contains(t, strings.Join(invalid, "; "), `evidence "e-1" is missing from the current transcript generation`)
}

func TestValidateP3KnowledgeEvidenceRejectsMissingRelationEvidence(t *testing.T) {
	chunks := []model.VideoTranscriptChunk{{
		VideoID: "video-1", Generation: "generation-1", ChunkIndex: 0,
		KnowledgeID: "chunk-1", EvidenceSentenceID: "e-1", Status: "completed",
	}}
	byEvidence, byIndex := knowledgegraph.BuildEvidenceChunkIndex(chunks)
	object := customknowledge.WikiObjectValidation{
		SourceVideoID: "video-1", TranscriptGeneration: "generation-1", EvidenceIDs: []string{"e-1"},
		Relations: []customknowledge.StructuredRelation{{
			RelationID: "relation-1", EvidenceIDs: []string{"e-missing"},
		}},
	}

	err := validateP3KnowledgeEvidence(object, byEvidence, byIndex)
	require.ErrorContains(t, err, `relation "relation-1": evidence "e-missing" is missing from the current transcript generation`)
}

func TestGraphHandlerRepairsMixedInvalidP3InsteadOfMarkingItComplete(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoTranscriptSource{}, &model.VideoProcessingJob{}))
	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(video).Error)
	require.NoError(t, db.Create(graphTranscriptChunk(video)).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptSource{ID: "binding-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, KnowledgeBaseID: "knowledge-kb", KnowledgeID: "source-1", ContentHash: "source-hash", Status: "created"}).Error)
	job := &model.VideoProcessingJob{ID: "job-1", VideoID: video.ID, JobType: skill.JobGraph, TranscriptGeneration: video.TranscriptGeneration, InputPayload: `{"transcript_source_knowledge_id":"source-1"}`}
	require.NoError(t, db.Create(job).Error)
	index := weknora.WikiPage{ID: "index-1", Slug: "video/video-1", PageType: "index", Content: p3IndexContent(video.ID, video.TranscriptGeneration, video.Title), Version: 1}
	valid := weknora.WikiPage{ID: "object-1", Slug: "concept/object-1", PageType: "index", Content: p3ConceptContent(video.ID, video.TranscriptGeneration), Version: 1}
	invalid := weknora.WikiPage{
		ID: "object-2", Slug: "concept/object-2", PageType: "index", Version: 1,
		Content: strings.Replace(p3ConceptContent(video.ID, video.TranscriptGeneration), "knowledge_object_id: object-1", "knowledge_object_id: object-2", 1),
	}
	invalid.Content = strings.Replace(invalid.Content, "audit_status: passed", "audit_status: aligned", 1)
	server := p3WikiServer(t, []weknora.WikiPage{index, valid, invalid})
	defer server.Close()
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	agent := &countingAgentClient{triggerErr: errors.New("stop after proving repair was requested")}
	handler := GraphHandler{BaseSkillHandler: BaseSkillHandler{
		DB: db, AgentClient: agent, SourceReader: graphSourceReader(t, video),
		KnowledgeBaseID: "knowledge-kb", Orchestrator: skill.NewOrchestrator(db, wiki, "knowledge-kb"),
	}}

	err = handler.Run(t.Context(), job, video)
	require.ErrorContains(t, err, "stop after proving repair was requested")
	require.Equal(t, 2, agent.calls)
	var stored model.Video
	require.NoError(t, db.First(&stored, "id = ?", video.ID).Error)
	require.Empty(t, stored.KnowledgeBaseWikiPageID)
}

func TestWaitForP3KnowledgeReportsInvalidObjectWithoutTimeout(t *testing.T) {
	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	index := weknora.WikiPage{ID: "index-1", Slug: "video/video-1", PageType: "index", Content: p3IndexContent(video.ID, video.TranscriptGeneration, video.Title), Version: 1}
	invalid := weknora.WikiPage{ID: "object-1", Slug: "concept/object-1", PageType: "index", Content: p3ConceptContent(video.ID, video.TranscriptGeneration), Version: 1}
	invalid.Content = strings.Replace(invalid.Content, "audit_status: passed", "audit_status: aligned", 1)
	server := p3WikiServer(t, []weknora.WikiPage{index, invalid})
	defer server.Close()
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	handler := BaseSkillHandler{KnowledgeBaseID: "knowledge-kb", Orchestrator: skill.NewOrchestrator(nil, wiki, "knowledge-kb")}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := handler.waitForP3Knowledge(ctx, video.ID, video.TranscriptGeneration, video.Title, skill.WikiPageBaseline{}, 10*time.Minute)
	require.ErrorContains(t, err, "object-1")
	require.ErrorContains(t, err, "audit_status must be passed")
}

func TestGraphHandlerRequestsOneContractRepairWithValidationDetails(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoTranscriptSource{}, &model.VideoProcessingJob{}))
	video := &model.Video{ID: "video-1", Title: "测试视频", DurationSeconds: 20, TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(video).Error)
	require.NoError(t, db.Create(graphTranscriptChunk(video)).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptSource{ID: "binding-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, KnowledgeBaseID: "knowledge-kb", KnowledgeID: "source-1", ContentHash: "source-hash", Status: "created"}).Error)
	job := &model.VideoProcessingJob{ID: "job-1", VideoID: video.ID, JobType: skill.JobGraph, TranscriptGeneration: video.TranscriptGeneration, InputPayload: `{"transcript_source_knowledge_id":"source-1"}`}
	require.NoError(t, db.Create(job).Error)
	index := weknora.WikiPage{ID: "index-1", Slug: "video/video-1", PageType: "index", Content: p3IndexContent(video.ID, video.TranscriptGeneration, video.Title), Version: 1}
	invalid := weknora.WikiPage{ID: "object-1", Slug: "concept/object-1", PageType: "index", Content: p3ConceptContent(video.ID, video.TranscriptGeneration), Version: 1}
	invalid.Content = strings.Replace(invalid.Content, "audit_status: passed", "audit_status: aligned", 1)
	server := p3WikiServer(t, []weknora.WikiPage{index, invalid})
	defer server.Close()
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	agent := &countingAgentClient{}
	handler := GraphHandler{BaseSkillHandler: BaseSkillHandler{
		DB: db, AgentClient: agent, SourceReader: graphSourceReader(t, video),
		KnowledgeBaseID: "knowledge-kb", Orchestrator: skill.NewOrchestrator(db, wiki, "knowledge-kb"),
	}}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err = handler.Run(ctx, job, video)
	require.Error(t, err)
	require.Equal(t, 4, agent.calls, "one initial session and one contract-repair session are expected")
	require.Len(t, agent.queries, 2)
	require.Contains(t, agent.queries[1], "video index page was not created or updated in the current attempt")
	require.Contains(t, agent.queries[1], "完整覆盖")
}

func TestRepairP3KnowledgeReportsInvalidIndexAndObjectContracts(t *testing.T) {
	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	indexContent := strings.Replace(
		p3IndexContent(video.ID, video.TranscriptGeneration, video.Title),
		"title: 测试视频_知识底座\n", "", 1,
	)
	invalidObjectContent := strings.Replace(
		p3ConceptContent(video.ID, video.TranscriptGeneration),
		"  mechanism: 运行机制\n", "  contrast: 相邻区别\n", 1,
	)
	server := p3WikiServer(t, []weknora.WikiPage{
		{ID: "index-1", Slug: "video/video-1", PageType: "index", Content: indexContent, Version: 1},
		{ID: "object-1", Slug: "concept/object-1", PageType: "index", Content: invalidObjectContent, Version: 1},
	})
	defer server.Close()
	agent := &countingAgentClient{}
	handler := BaseSkillHandler{
		AgentClient: agent, KnowledgeBaseID: "knowledge-kb",
		Orchestrator: skill.NewOrchestrator(nil, weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL}), "knowledge-kb"),
	}

	err := handler.repairP3KnowledgeOnce(t.Context(), video, "基础请求。", []string{"source-1"}, skill.WikiPageBaseline{}, &contentprovenance.Job{
		TaskID: "job-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, JobType: skill.JobGraph,
	})
	require.NoError(t, err)
	require.Len(t, agent.queries, 1)
	repairQuery := agent.queries[0]
	require.Contains(t, repairQuery, "index-1: frontmatter.title is required")
	require.Contains(t, repairQuery, "object-1: structure_fields.contrast is not valid for this knowledge type")
	require.Contains(t, repairQuery, "slug、title、summary、content、page_type、source_refs")
}

func TestMalformedCurrentGenerationObjectIsReportedInsteadOfIgnored(t *testing.T) {
	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	index := weknora.WikiPage{ID: "index-1", Slug: "video/video-1", PageType: "index", Content: p3IndexContent(video.ID, video.TranscriptGeneration, video.Title), Version: 1}
	malformed := weknora.WikiPage{ID: "object-bad", Slug: "concept/object-bad", PageType: "index", Version: 1, Content: "---\n" +
		"type: concept\ntype: concept\nsource_video_id: video-1\ntranscript_generation: generation-1\n---\n# 损坏候选\n"}
	server := p3WikiServer(t, []weknora.WikiPage{index, malformed})
	defer server.Close()
	handler := BaseSkillHandler{KnowledgeBaseID: "knowledge-kb", Orchestrator: skill.NewOrchestrator(nil, weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL}), "knowledge-kb")}

	_, invalid, err := handler.inspectP3Knowledge(t.Context(), video.ID, video.TranscriptGeneration, video.Title)
	require.NoError(t, err)
	diagnostics := strings.Join(invalid, "; ")
	require.Contains(t, diagnostics, "object-bad")
	require.Contains(t, diagnostics, "当前代次没有任何知识对象候选页")
}

func TestInspectP3KnowledgeAfterIgnoresUnchangedHistoricalInvalidPage(t *testing.T) {
	db := p3EvidenceDB(t)
	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	index := weknora.WikiPage{
		ID: "index-1", Slug: "video/video-1", PageType: "index",
		Content: p3IndexContent(video.ID, video.TranscriptGeneration, video.Title), Version: 2,
	}
	historicalInvalid := weknora.WikiPage{
		ID: "object-old", Slug: "concept/object-old", PageType: "concept", Version: 1,
		Content: strings.Replace(p3ConceptContent(video.ID, video.TranscriptGeneration), "knowledge_object_id: object-1", "knowledge_object_id: object-old", 1),
	}
	currentValid := weknora.WikiPage{
		ID: "object-new", Slug: "concept/object-new", PageType: "index", Version: 1,
		Content: strings.Replace(p3ConceptContent(video.ID, video.TranscriptGeneration), "knowledge_object_id: object-1", "knowledge_object_id: object-new", 1),
	}
	server := p3WikiServer(t, []weknora.WikiPage{index, historicalInvalid, currentValid})
	defer server.Close()
	handler := BaseSkillHandler{
		DB: db, KnowledgeBaseID: "knowledge-kb",
		Orchestrator: skill.NewOrchestrator(
			db, weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL}), "knowledge-kb",
		),
	}
	baseline := skill.WikiPageBaseline{Versions: skill.WikiPageVersionSnapshot{
		"index-1": 1, "object-old": 1,
	}}

	artifacts, invalid, err := handler.inspectP3KnowledgeAfter(t.Context(), video.ID, video.TranscriptGeneration, video.Title, baseline)
	require.NoError(t, err)
	require.Empty(t, invalid)
	require.NotNil(t, artifacts)
	require.Equal(t, "index-1", artifacts.Index.ID)
	require.Equal(t, 1, artifacts.ObjectCount)

	baseline.Versions["index-1"] = 2
	artifacts, invalid, err = handler.inspectP3KnowledgeAfter(t.Context(), video.ID, video.TranscriptGeneration, video.Title, baseline)
	require.NoError(t, err)
	require.Nil(t, artifacts)
	require.Contains(t, strings.Join(invalid, "; "), "video index page was not created or updated in the current attempt")
}

func TestInspectP3KnowledgeAfterRejectsUnchangedHistoricalSemanticDuplicate(t *testing.T) {
	db := p3EvidenceDB(t)
	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	index := weknora.WikiPage{
		ID: "index-1", Slug: "video/video-1", PageType: "index",
		Content: p3IndexContent(video.ID, video.TranscriptGeneration, video.Title), Version: 2,
	}
	baseContent := strings.NewReplacer(
		"这是可展示的概念内容。", "第二大脑是由本地知识库和 AI Agent 组成、能够调用知识并执行工作的系统。",
		"概念定义", "把静态档案库升级为可执行的知识系统",
		"运行机制", "AI Agent 调用知识、执行方法并回写经验",
	).Replace(p3ConceptContent(video.ID, video.TranscriptGeneration))
	historical := weknora.WikiPage{
		ID: "object-old", Slug: "concept/second-brain", Title: "第二大脑", PageType: "index", Version: 1,
		Content: strings.Replace(baseContent, "knowledge_object_id: object-1", "knowledge_object_id: second-brain", 1),
	}
	current := weknora.WikiPage{
		ID: "object-new", Slug: "concept/second-brain-v2", Title: "第二大脑（概念）", PageType: "index", Version: 1,
		Content: strings.Replace(baseContent, "knowledge_object_id: object-1", "knowledge_object_id: second-brain-v2", 1),
	}
	server := p3WikiServer(t, []weknora.WikiPage{index, historical, current})
	defer server.Close()
	handler := BaseSkillHandler{
		DB: db, KnowledgeBaseID: "knowledge-kb",
		Orchestrator: skill.NewOrchestrator(
			db, weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL}), "knowledge-kb",
		),
	}
	baseline := skill.WikiPageBaseline{Versions: skill.WikiPageVersionSnapshot{
		"index-1": 1, "object-old": 1,
	}}

	artifacts, invalid, err := handler.inspectP3KnowledgeAfter(t.Context(), video.ID, video.TranscriptGeneration, video.Title, baseline)
	require.NoError(t, err)
	require.Nil(t, artifacts)
	require.Contains(t, strings.Join(invalid, "; "), "semantic identity")
}

func TestRecordedP3KnowledgeIndexDoesNotRescanHistoricalObjects(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}))
	video := &model.Video{
		ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1",
		KnowledgeBaseWikiPageID: "index-1",
	}
	require.NoError(t, db.Create(video).Error)
	index := weknora.WikiPage{
		ID: "index-1", Slug: "video/video-1", PageType: "index",
		Content: p3IndexContent(video.ID, video.TranscriptGeneration, video.Title), Version: 2,
	}
	historicalInvalid := weknora.WikiPage{
		ID: "object-old", Slug: "concept/object-old", PageType: "concept", Version: 1,
		Content: p3ConceptContent(video.ID, video.TranscriptGeneration),
	}
	server := p3WikiServer(t, []weknora.WikiPage{index, historicalInvalid})
	defer server.Close()
	handler := GraphHandler{BaseSkillHandler: BaseSkillHandler{
		DB: db, KnowledgeBaseID: "knowledge-kb",
		Orchestrator: skill.NewOrchestrator(db, weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL}), "knowledge-kb"),
	}}

	page, err := handler.recordedP3KnowledgeIndex(t.Context(), video.ID, video.TranscriptGeneration, video.Title)
	require.NoError(t, err)
	require.NotNil(t, page)
	require.Equal(t, index.ID, page.ID)
}

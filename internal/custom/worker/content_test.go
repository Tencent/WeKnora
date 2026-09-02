package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	transcriptservice "github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

type sourceReaderStub struct {
	value weknora.ManualKnowledgeResult
}

func (s sourceReaderStub) GetKnowledge(_ context.Context, _ string) (weknora.ManualKnowledgeResult, error) {
	return s.value, nil
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
	require.Contains(t, query, "每个实体和每个知识原子都要写入独立 Wiki 页面")
	require.Contains(t, query, "references/type-frameworks.md")
	require.Contains(t, query, "对应结构维度")
	require.Contains(t, query, `slug 严格使用 "video/video-1"`)
	require.Contains(t, query, "type: knowledge_base")
	require.Contains(t, query, "索引页目标可能尚不存在")
	require.Contains(t, query, "读取返回 not found 不是失败")
	require.Contains(t, query, "连续语义窗口")
	require.Contains(t, query, "实体、概念、案例、方法论和洞察五类知识")
	require.Contains(t, query, "不得用示例、占位内容或 mock 数据")
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

func TestWikiInputFullDocumentDoesNotReadTranscriptChunks(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoTranscriptSource{}))
	video := model.Video{ID: "video-1", Title: "测试视频", DurationSeconds: 20, TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptSource{ID: "binding-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, KnowledgeID: "source-1", ContentHash: "hash", Status: transcriptservice.SourceStatusCreated}).Error)
	doc, err := transcriptservice.Build(transcriptservice.Input{
		VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, Title: video.Title, DurationSeconds: video.DurationSeconds,
		Chapters: []transcriptservice.InputChapter{{Index: 0, Title: "开场", Paragraphs: []transcriptservice.InputParagraph{{Index: 0, Sentences: []transcriptservice.InputSentence{{SourceSentenceID: "s-1", EvidenceSentenceID: "e-1", Text: "正文", StartMs: 100, EndMs: 1000}}}}}},
	})
	require.NoError(t, err)
	jsonText, err := doc.JSON()
	require.NoError(t, err)
	handler := BaseSkillHandler{DB: db, SourceReader: sourceReaderStub{value: weknora.ManualKnowledgeResult{ID: "source-1", Content: transcriptservice.SourceContent(doc, jsonText, "hash")}}}
	job := &model.VideoProcessingJob{ID: "job-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, InputPayload: `{"transcript_input_mode":"full_document","transcript_source_knowledge_id":"source-1"}`}
	input, err := handler.wikiInput(t.Context(), job, &video, skill.JobGraph)
	require.NoError(t, err)
	require.Equal(t, TranscriptInputModeFullDocument, input.Mode)
	require.Equal(t, []string{"source-1"}, input.KnowledgeIDs)
}

func TestWikiInputFullDocumentRejectsMissingSource(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptSource{}))
	video := model.Video{ID: "video-1", Title: "测试视频", DurationSeconds: 20, TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(&video).Error)
	handler := BaseSkillHandler{DB: db}
	_, err = handler.wikiInput(t.Context(), &model.VideoProcessingJob{VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration, InputPayload: `{"transcript_input_mode":"full_document"}`}, &video, skill.JobGraph)
	require.Error(t, err)
	category, code := ClassifyProcessingError(err)
	require.Equal(t, ErrorCategoryResponseParse, category)
	require.Equal(t, "source_missing", code)
}

func TestSkillQueryFullDocumentForbidsChunkInput(t *testing.T) {
	video := &model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	contract, ok := skill.Contract(skill.JobGraph)
	require.True(t, ok)
	query := skillQueryWithInput(video, contract, skill.JobGraph, "source-1", TranscriptInputModeFullDocument)
	require.Contains(t, query, "完整视频源文档知识 ID：source-1")
	require.Contains(t, query, "不得读取字幕分块知识 ID")
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

package worker

import (
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
)

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
	require.Contains(t, query, `slug 严格使用 "video/video-1"`)
	require.Contains(t, query, "type: knowledge_base")
	require.Contains(t, query, "目标产物页可能尚不存在")
	require.Contains(t, query, "读取返回 not found 不是失败")
	require.Contains(t, query, "连续语义窗口")
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

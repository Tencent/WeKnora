package skill

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

func newOrchestratorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoTranscriptSource{}, &model.VideoProcessingJob{}))
	return db
}

func TestEnqueueContentPipelinePersistsCurrentTranscriptManifest(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoTranscriptSource{}, &model.VideoProcessingJob{}))
	video := model.Video{ID: "video-pipeline", Title: "test", TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create([]model.VideoTranscriptChunk{
		{VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 0, KnowledgeID: "knowledge-1", ContentHash: "hash-1", Status: "completed"},
		{VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 1, KnowledgeID: "knowledge-2", ContentHash: "hash-2", Status: "completed"},
	}).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptSource{
		ID: "source-binding-1", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration,
		KnowledgeID: "source-knowledge-1", ContentHash: "source-hash-1", Status: "created",
	}).Error)

	orchestrator := NewOrchestrator(db, nil, "kb-1")
	require.NoError(t, orchestrator.EnqueueContentPipeline(t.Context(), video.ID))

	var jobs []model.VideoProcessingJob
	require.NoError(t, db.Where("video_id = ?", video.ID).Order("job_type ASC").Find(&jobs).Error)
	require.Len(t, jobs, 3)
	for _, job := range jobs {
		require.Contains(t, job.InputPayload, "knowledge-1")
		require.Contains(t, job.InputPayload, "knowledge-2")
		require.Contains(t, job.InputPayload, "source-knowledge-1")
		require.Equal(t, video.TranscriptGeneration, job.TranscriptGeneration)
	}
}

func TestAfterSkillCompleteWithIDDoesNotRequireDownstreamJobTable(t *testing.T) {
	db := newOrchestratorTestDB(t)
	video := model.Video{ID: "video-1", Title: "test"}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Migrator().DropTable(&model.VideoProcessingJob{}))

	orchestrator := NewOrchestrator(db, nil, "kb-1")
	_, _, err := orchestrator.AfterSkillCompleteWithID(
		context.Background(), video.ID, JobGraph, "wiki-1",
	)
	require.NoError(t, err)

	var stored model.Video
	require.NoError(t, db.First(&stored, "id = ?", video.ID).Error)
	require.Equal(t, "wiki-1", stored.KnowledgeBaseWikiPageID)
}

func TestAfterSkillCompleteWithIDWritesEnhancementWithoutEnqueuingFoundation(t *testing.T) {
	db := newOrchestratorTestDB(t)
	video := model.Video{ID: "video-1", Title: "test", TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(&video).Error)

	orchestrator := NewOrchestrator(db, nil, "kb-1")
	wikiPageID, nextJobID, err := orchestrator.AfterSkillCompleteWithID(
		context.Background(), video.ID, JobGraph, "wiki-1",
	)
	require.NoError(t, err)
	require.Equal(t, "wiki-1", wikiPageID)
	require.Empty(t, nextJobID)

	var stored model.Video
	require.NoError(t, db.First(&stored, "id = ?", video.ID).Error)
	require.Equal(t, "wiki-1", stored.KnowledgeBaseWikiPageID)

	var jobCount int64
	require.NoError(t, db.Model(&model.VideoProcessingJob{}).Where("video_id = ?", video.ID).Count(&jobCount).Error)
	require.Zero(t, jobCount)
}

func TestAssembleMarksVideoCompletedOnlyWhenAllArtifactsExist(t *testing.T) {
	db := newOrchestratorTestDB(t)
	video := model.Video{
		ID: "video-complete", Title: "test", Status: model.VideoStatusProcessing,
		TranscriptGeneration: "generation-1", KnowledgeBaseWikiPageID: "knowledge-base",
		OutlineWikiPageID: "outline", OverviewWikiPageID: "overview", SummaryWikiPageID: "summary",
	}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptChunk{
		VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 0,
		KnowledgeID: "transcript-1", ContentHash: "hash-1", Status: "completed",
	}).Error)

	orchestrator := NewOrchestrator(db, nil, "kb-1")
	_, nextJobID, err := orchestrator.AfterSkillCompleteWithID(
		context.Background(), video.ID, JobAssemble, "transcript-page",
	)
	require.NoError(t, err)
	require.Empty(t, nextJobID)

	var stored model.Video
	require.NoError(t, db.First(&stored, "id = ?", video.ID).Error)
	require.Equal(t, model.VideoStatusCompleted, stored.Status)
	require.Equal(t, "transcript-page", stored.TranscriptPageWikiPageID)
}

func TestAssembleRejectsIncompleteArtifactSet(t *testing.T) {
	db := newOrchestratorTestDB(t)
	video := model.Video{
		ID: "video-incomplete", Title: "test", Status: model.VideoStatusProcessing,
		TranscriptGeneration: "generation-1", KnowledgeBaseWikiPageID: "knowledge-base",
		OutlineWikiPageID: "outline", OverviewWikiPageID: "overview",
	}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptChunk{
		VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 0,
		KnowledgeID: "transcript-1", ContentHash: "hash-1", Status: "completed",
	}).Error)

	orchestrator := NewOrchestrator(db, nil, "kb-1")
	_, _, err := orchestrator.AfterSkillCompleteWithID(
		context.Background(), video.ID, JobAssemble, "transcript-page",
	)
	require.ErrorContains(t, err, "incomplete content artifacts")

	var stored model.Video
	require.NoError(t, db.First(&stored, "id = ?", video.ID).Error)
	require.Equal(t, model.VideoStatusProcessing, stored.Status)
	require.Empty(t, stored.TranscriptPageWikiPageID)
}

func TestFindWikiPageDoesNotFallbackToTranscriptKnowledgeID(t *testing.T) {
	db := newOrchestratorTestDB(t)
	video := model.Video{ID: "video-1", Title: "test", TranscriptKnowledgeID: "transcript-page"}
	require.NoError(t, db.Create(&video).Error)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
			Pages: nil, Total: 0, Page: 1, PageSize: 100, TotalPages: 0,
		})
	}))
	defer server.Close()

	wikiClient := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	orchestrator := NewOrchestrator(db, wikiClient, "kb-1")
	wikiPageID, pageCount, err := orchestrator.FindWikiPage(t.Context(), video.ID, JobGraph)
	require.NoError(t, err)
	require.Empty(t, wikiPageID)
	require.Zero(t, pageCount)
}

func TestFindWikiPageRequiresReadableDetailPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages: []weknora.WikiPage{{
					ID: "outline-page", Slug: "outline/video-1", PageType: "index", Content: "video-1",
				}},
				Total: 1, Page: 1, PageSize: 100, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			http.NotFound(writer, request)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	wikiClient := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	orchestrator := NewOrchestrator(nil, wikiClient, "kb-1")
	wikiPageID, pageCount, err := orchestrator.FindWikiPage(t.Context(), "video-1", JobOutline)
	require.NoError(t, err)
	require.Empty(t, wikiPageID)
	require.Equal(t, 1, pageCount)
}

func TestFindWikiPageRejectsGraphSynthesisFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
			Pages: []weknora.WikiPage{{
				ID: "wrong-graph-page", Slug: "video/video-1", PageType: "synthesis",
				Content: "---\ntype: knowledge_base\nsource_video_id: video-1\n---\ncontent",
			}},
			Total: 1, Page: 1, PageSize: 100, TotalPages: 1,
		})
	}))
	defer server.Close()

	wikiClient := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	orchestrator := NewOrchestrator(nil, wikiClient, "kb-1")
	wikiPageID, pageCount, err := orchestrator.FindWikiPage(t.Context(), "video-1", JobGraph)
	require.NoError(t, err)
	require.Empty(t, wikiPageID)
	require.Equal(t, 1, pageCount)
}

func TestFindWikiPageAfterRejectsUnchangedPageFromBeforeSkillRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages: []weknora.WikiPage{{
					ID: "outline-page", Slug: "outline/video-1", PageType: "index",
					Content: "---\ntype: outline\nsource_video_id: video-1\n---\nvideo-1", Version: 3,
				}},
				Total: 1, Page: 1, PageSize: 100, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "outline-page", Slug: "outline/video-1", PageType: "index",
				Content: "---\ntype: outline\nsource_video_id: video-1\n---\nvideo-1", Version: 3,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	wikiClient := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	orchestrator := NewOrchestrator(nil, wikiClient, "kb-1")
	wikiPageID, pageCount, err := orchestrator.FindWikiPageAfter(
		t.Context(), "video-1", JobOutline, WikiPageBaseline{
			Versions:     WikiPageVersionSnapshot{"outline-page": 3},
			JobCreatedAt: time.Now().UTC(),
		},
	)
	require.NoError(t, err)
	require.Empty(t, wikiPageID)
	require.Equal(t, 1, pageCount)
}

func TestFindWikiPageAfterAcceptsUpdatedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages: []weknora.WikiPage{{
					ID: "outline-page", Slug: "outline/video-1", PageType: "index",
					Content: "---\ntype: outline\nsource_video_id: video-1\n---\nvideo-1 updated", Version: 4,
				}},
				Total: 1, Page: 1, PageSize: 100, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "outline-page", Slug: "outline/video-1", PageType: "index",
				Content: "---\ntype: outline\nsource_video_id: video-1\n---\nvideo-1 updated", Version: 4,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	wikiClient := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	orchestrator := NewOrchestrator(nil, wikiClient, "kb-1")
	wikiPageID, pageCount, err := orchestrator.FindWikiPageAfter(
		t.Context(), "video-1", JobOutline, WikiPageBaseline{
			Versions:     WikiPageVersionSnapshot{"outline-page": 3},
			JobCreatedAt: time.Now().UTC(),
		},
	)
	require.NoError(t, err)
	require.Equal(t, "outline-page", wikiPageID)
	require.Equal(t, 1, pageCount)
}

func TestFindWikiPageAfterAcceptsPageWrittenByEarlierAttemptOfSameJob(t *testing.T) {
	jobCreatedAt := time.Now().UTC().Add(-time.Minute)
	pageUpdatedAt := jobCreatedAt.Add(30 * time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages: []weknora.WikiPage{{
					ID: "outline-page", Slug: "outline/video-1", PageType: "index",
					Content: "---\ntype: outline\nsource_video_id: video-1\n---\nvideo-1", Version: 3,
					UpdatedAt: pageUpdatedAt,
				}},
				Total: 1, Page: 1, PageSize: 100, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "outline-page", Slug: "outline/video-1", PageType: "index",
				Content: "---\ntype: outline\nsource_video_id: video-1\n---\nvideo-1", Version: 3,
				UpdatedAt: pageUpdatedAt,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	wikiClient := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	orchestrator := NewOrchestrator(nil, wikiClient, "kb-1")
	wikiPageID, pageCount, err := orchestrator.FindWikiPageAfter(
		t.Context(), "video-1", JobOutline, WikiPageBaseline{
			Versions:     WikiPageVersionSnapshot{"outline-page": 3},
			JobCreatedAt: jobCreatedAt,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "outline-page", wikiPageID)
	require.Equal(t, 1, pageCount)
}

func TestFindWikiPageAfterRejectsMismatchedFrontmatterTypeWithMatchingSlug(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages: []weknora.WikiPage{{
					ID: "outline-page", Slug: "outline/video-1", PageType: "index",
					Content: "---\ntype: overview\nsource_video_id: video-1\n---\n概览内容", Version: 2,
				}},
				Total: 1, Page: 1, PageSize: 100, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "outline-page", Slug: "outline/video-1", PageType: "index",
				Content: "---\ntype: overview\nsource_video_id: video-1\n---\n概览内容", Version: 2,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	wikiClient := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})
	orchestrator := NewOrchestrator(nil, wikiClient, "kb-1")
	wikiPageID, pageCount, err := orchestrator.FindWikiPageAfter(
		t.Context(), "video-1", JobOutline, WikiPageBaseline{},
	)
	require.NoError(t, err)
	require.Empty(t, wikiPageID)
	require.Equal(t, 1, pageCount)
}

func TestJobContractsCoverParallelPipeline(t *testing.T) {
	for _, jobType := range append(append([]string{}, FoundationJobs...), EnhancementJobs...) {
		contract, ok := Contract(jobType)
		require.True(t, ok)
		require.NotEmpty(t, contract.SkillName)
		require.NotEmpty(t, contract.ArtifactType)
		require.NotEmpty(t, contract.WikiPageTypes)
		require.NotEmpty(t, contract.VideoField)

		require.Empty(t, NextJob(jobType))
	}
}

func TestProviderForJobSeparatesDirectLLMAndAgentJobs(t *testing.T) {
	for _, jobType := range []string{JobOutline, JobSummary, JobSummaryEnhance} {
		require.Equal(t, "llm", providerForJob(jobType))
	}
	require.Equal(t, "weknora", providerForJob(JobGraph))
}

func TestConcurrentEnqueueReusesOneEffectiveJob(t *testing.T) {
	db := newOrchestratorTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	video := model.Video{ID: "video-concurrent", Title: "test", TranscriptGeneration: "generation-1"}
	require.NoError(t, db.Create(&video).Error)
	orchestrator := NewOrchestrator(db, nil, "kb-1")

	const callers = 12
	ids := make(chan string, callers)
	errs := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			id, err := orchestrator.EnqueueJob(context.Background(), video.ID, JobOutline)
			ids <- id
			errs <- err
		}()
	}
	group.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	uniqueIDs := map[string]bool{}
	for id := range ids {
		uniqueIDs[id] = true
	}
	require.Len(t, uniqueIDs, 1)
	var count int64
	require.NoError(t, db.Model(&model.VideoProcessingJob{}).Where("video_id = ? AND job_type = ?", video.ID, JobOutline).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

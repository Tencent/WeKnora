package skill

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}))
	return db
}

func TestAfterSkillCompleteWithIDRollsBackVideoWriteWhenNextJobEnqueueFails(t *testing.T) {
	db := newOrchestratorTestDB(t)
	video := model.Video{ID: "video-1", Title: "test"}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Migrator().DropTable(&model.VideoProcessingJob{}))

	orchestrator := NewOrchestrator(db, nil, "kb-1")
	_, _, err := orchestrator.AfterSkillCompleteWithID(
		context.Background(), video.ID, JobGraph, "wiki-1",
	)
	require.Error(t, err)

	var stored model.Video
	require.NoError(t, db.First(&stored, "id = ?", video.ID).Error)
	require.Empty(t, stored.KnowledgeBaseWikiPageID)
}

func TestAfterSkillCompleteWithIDWritesVideoAndEnqueuesNextJobAtomically(t *testing.T) {
	db := newOrchestratorTestDB(t)
	video := model.Video{ID: "video-1", Title: "test"}
	require.NoError(t, db.Create(&video).Error)

	orchestrator := NewOrchestrator(db, nil, "kb-1")
	wikiPageID, nextJobID, err := orchestrator.AfterSkillCompleteWithID(
		context.Background(), video.ID, JobGraph, "wiki-1",
	)
	require.NoError(t, err)
	require.Equal(t, "wiki-1", wikiPageID)
	require.NotEmpty(t, nextJobID)

	var stored model.Video
	require.NoError(t, db.First(&stored, "id = ?", video.ID).Error)
	require.Equal(t, "wiki-1", stored.KnowledgeBaseWikiPageID)

	var nextJob model.VideoProcessingJob
	require.NoError(t, db.First(&nextJob, "id = ?", nextJobID).Error)
	require.Equal(t, JobOutline, nextJob.JobType)
	require.Equal(t, "pending", nextJob.Status)
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
					ID: "outline-page", Slug: "outline/video-1", PageType: "index", Content: "video-1", Version: 3,
				}},
				Total: 1, Page: 1, PageSize: 100, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "outline-page", Slug: "outline/video-1", PageType: "index", Content: "video-1", Version: 3,
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
					ID: "outline-page", Slug: "outline/video-1", PageType: "index", Content: "video-1 updated", Version: 4,
				}},
				Total: 1, Page: 1, PageSize: 100, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "outline-page", Slug: "outline/video-1", PageType: "index", Content: "video-1 updated", Version: 4,
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
					ID: "outline-page", Slug: "outline/video-1", PageType: "index", Content: "video-1", Version: 3,
					UpdatedAt: pageUpdatedAt,
				}},
				Total: 1, Page: 1, PageSize: 100, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "outline-page", Slug: "outline/video-1", PageType: "index", Content: "video-1", Version: 3,
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

func TestJobContractsCoverEntireChain(t *testing.T) {
	for index, jobType := range ChainOrder {
		contract, ok := Contract(jobType)
		require.True(t, ok)
		require.NotEmpty(t, contract.SkillName)
		require.NotEmpty(t, contract.ArtifactType)
		require.NotEmpty(t, contract.WikiPageTypes)
		require.NotEmpty(t, contract.VideoField)

		if index+1 < len(ChainOrder) {
			require.Equal(t, ChainOrder[index+1], NextJob(jobType))
		} else {
			require.Empty(t, NextJob(jobType))
		}
	}
}

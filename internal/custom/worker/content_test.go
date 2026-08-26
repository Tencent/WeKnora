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

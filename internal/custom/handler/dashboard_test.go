package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestDashboardReadsPersistedStatisticsWithoutSyntheticRows(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.DashboardQuestionStat{}, &model.DashboardQuestionCluster{}, &model.DashboardQuestionEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	lastAsked := time.Date(2026, 8, 20, 14, 30, 0, 0, time.UTC)
	if err := db.Create(&model.DashboardQuestionStat{
		ID: "stat-1", StatDate: "2026-08-20", QuestionCount: 4, ActiveVideoCount: 2,
		ClusterCount: 1, TopVideos: `[{"video_id":"video-1","title":"真实视频","count":3}]`,
	}).Error; err != nil {
		t.Fatalf("create stat: %v", err)
	}
	if err := db.Create(&model.DashboardQuestionCluster{
		ID: "cluster-1", RepresentativeQuestion: "真实问题", QuestionCount: 3,
		RelatedVideoCount: 1, LastAskedAt: &lastAsked,
		Videos: `[{"video_id":"video-1","title":"真实视频","video_category":"training","first_seconds":12,"first_timestamp":"00:12"}]`,
	}).Error; err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/dashboard?range=custom&from=2026-08-20&to=2026-08-20", nil)
	NewDashboardHandler(db).Get(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Trend    []dashboardTrendPoint `json:"trend"`
			Clusters []dashboardCluster    `json:"clusters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !response.Success || len(response.Data.Trend) != 1 || response.Data.Trend[0].Count != 4 {
		t.Fatalf("response trend = %#v", response.Data.Trend)
	}
	if len(response.Data.Clusters) != 1 || response.Data.Clusters[0].RepresentativeQuestion != "真实问题" {
		t.Fatalf("response clusters = %#v", response.Data.Clusters)
	}
}

func TestDashboardReturnsEmptyPayloadWhenNoPersistedStatisticsExist(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.DashboardQuestionStat{}, &model.DashboardQuestionCluster{}, &model.DashboardQuestionEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/dashboard?range=custom&from=2026-08-20&to=2026-08-20", nil)
	NewDashboardHandler(db).Get(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Trend    []dashboardTrendPoint `json:"trend"`
			Clusters []dashboardCluster    `json:"clusters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Data.Trend) != 0 || len(response.Data.Clusters) != 0 {
		t.Fatalf("empty database must remain empty: %#v", response.Data)
	}
}

func TestParseDashboardQueryUsesDashboardTimezone(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/dashboard?range=7d", nil)

	query, err := parseDashboardQuery(ctx)
	if err != nil {
		t.Fatalf("parseDashboardQuery returned error for current 7d range: %v", err)
	}
	if query.To != localToday().Format("2006-01-02") {
		t.Fatalf("to = %q, want dashboard local date %q", query.To, localToday().Format("2006-01-02"))
	}
}

func TestDashboardRecordsRealQuestionIdempotently(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(
		&model.Video{},
		&model.DashboardQuestionStat{},
		&model.DashboardQuestionCluster{},
		&model.DashboardQuestionEvent{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	video := model.Video{ID: uuid.NewString(), Title: "真实课程", VideoType: "training"}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}

	record := func(eventID, question string) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/custom/dashboard/questions", strings.NewReader(
			fmt.Sprintf(`{"event_id":%q,"session_id":"session-1","video_id":%q,"video_seconds":125,"question":%q}`, eventID, video.ID, question),
		))
		ctx.Request.Header.Set("Content-Type", "application/json")
		NewDashboardHandler(db).RecordQuestion(ctx)
		if recorder.Code != http.StatusOK {
			t.Fatalf("record status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	}

	record("event-1", "如何搭建知识库？")
	record("event-1", "如何搭建知识库？")
	record("event-2", "如何搭建知识库")

	var events []model.DashboardQuestionEvent
	if err := db.Find(&events).Error; err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 after idempotent retry", len(events))
	}
	var clusters []model.DashboardQuestionCluster
	if err := db.Find(&clusters).Error; err != nil {
		t.Fatalf("load clusters: %v", err)
	}
	if len(clusters) != 1 || clusters[0].QuestionCount != 2 || clusters[0].RelatedVideoCount != 1 {
		t.Fatalf("clusters = %#v, want one aggregated cluster", clusters)
	}
	var stats []model.DashboardQuestionStat
	if err := db.Find(&stats).Error; err != nil {
		t.Fatalf("load stats: %v", err)
	}
	if len(stats) != 1 || stats[0].QuestionCount != 2 || stats[0].ActiveVideoCount != 1 || stats[0].ClusterCount != 1 {
		t.Fatalf("stats = %#v, want real aggregated counts", stats)
	}
}

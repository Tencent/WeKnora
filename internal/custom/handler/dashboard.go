package handler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

type DashboardHandler struct {
	db *gorm.DB
}

type dashboardQuery struct {
	Range string
	From  string
	To    string
}

type recordQuestionRequest struct {
	EventID      string `json:"event_id"`
	SessionID    string `json:"session_id"`
	VideoID      string `json:"video_id"`
	VideoSeconds int    `json:"video_seconds"`
	Question     string `json:"question"`
}

type dashboardTopVideo struct {
	VideoID string `json:"video_id"`
	Title   string `json:"title"`
	Count   int    `json:"count"`
}

type dashboardTrendPoint struct {
	Date      string              `json:"date"`
	Count     int                 `json:"count"`
	TopVideos []dashboardTopVideo `json:"top_videos"`
}

type dashboardClusterVideo struct {
	VideoID        string `json:"video_id"`
	Title          string `json:"title"`
	VideoCategory  string `json:"video_category"`
	FirstSeconds   int    `json:"first_seconds"`
	FirstTimestamp string `json:"first_timestamp"`
	Deleted        bool   `json:"deleted,omitempty"`
}

type dashboardCluster struct {
	ID                     string                  `json:"id"`
	RepresentativeQuestion string                  `json:"representative_question"`
	Count                  int                     `json:"count"`
	RelatedVideoCount      int                     `json:"related_video_count"`
	LastAskedAt            string                  `json:"last_asked_at"`
	Videos                 []dashboardClusterVideo `json:"videos"`
}

type dashboardPayload struct {
	Range string `json:"range"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	KPI   struct {
		TotalQuestions       int `json:"total_questions"`
		ActiveVideos         int `json:"active_videos"`
		ClusterCount         int `json:"cluster_count"`
		AvgQuestionsPerVideo int `json:"avg_questions_per_video"`
		Trend                struct {
			TotalQuestions       int `json:"total_questions"`
			ActiveVideos         int `json:"active_videos"`
			ClusterCount         int `json:"cluster_count"`
			AvgQuestionsPerVideo int `json:"avg_questions_per_video"`
		} `json:"trend"`
	} `json:"kpi"`
	Trend    []dashboardTrendPoint `json:"trend"`
	Clusters []dashboardCluster    `json:"clusters"`
}

func NewDashboardHandler(db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{db: db}
}

// RecordQuestion persists one real submitted question and updates the
// read-optimized dashboard aggregates in the same transaction.
func (h *DashboardHandler) RecordQuestion(c *gin.Context) {
	var input recordQuestionRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "提问数据格式无效"})
		return
	}
	input.EventID = strings.TrimSpace(input.EventID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.VideoID = strings.TrimSpace(input.VideoID)
	input.Question = strings.TrimSpace(input.Question)
	if input.EventID == "" || input.Question == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "event_id 和 question 不能为空"})
		return
	}
	if len([]rune(input.Question)) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "问题长度不能超过 2000 个字符"})
		return
	}
	if input.VideoSeconds < 0 {
		input.VideoSeconds = 0
	}

	event, created, err := h.recordQuestion(c, input)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "保存提问统计失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"event_id": event.EventID, "recorded": created},
	})
}

func (h *DashboardHandler) recordQuestion(c *gin.Context, input recordQuestionRequest) (*model.DashboardQuestionEvent, bool, error) {
	var event model.DashboardQuestionEvent
	created := false
	err := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("event_id = ?", input.EventID).First(&event).Error; err == nil {
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}

		videoTitle, videoCategory := "", ""
		if input.VideoID != "" {
			var video model.Video
			if err := tx.Select("id", "title", "video_type").Where("id = ?", input.VideoID).First(&video).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return fmt.Errorf("video not found: %s", input.VideoID)
				}
				return err
			}
			videoTitle, videoCategory = video.Title, video.VideoType
		}

		questionKey := normalizeQuestionKey(input.Question)
		if questionKey == "" {
			return fmt.Errorf("question has no searchable content")
		}
		clusterID := stableClusterID(questionKey)
		now := time.Now().In(dashboardLocation)
		event = model.DashboardQuestionEvent{
			ID:            uuid.NewString(),
			EventID:       input.EventID,
			SessionID:     input.SessionID,
			VideoID:       input.VideoID,
			VideoTitle:    videoTitle,
			VideoCategory: videoCategory,
			VideoSeconds:  input.VideoSeconds,
			ClusterID:     clusterID,
			Question:      input.Question,
			AskedAt:       now,
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// A concurrent retry inserted the same idempotency key.
			if err := tx.Where("event_id = ?", input.EventID).First(&event).Error; err != nil {
				return err
			}
			return nil
		}
		created = true

		if err := upsertQuestionCluster(tx, event, questionKey); err != nil {
			return err
		}
		return upsertQuestionStat(tx, now)
	})
	return &event, created, err
}

var dashboardLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func normalizeQuestionKey(question string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(strings.TrimSpace(question)) {
		switch {
		case unicode.IsLetter(char), unicode.IsDigit(char):
			builder.WriteRune(char)
		case unicode.IsSpace(char):
			continue
		case unicode.IsPunct(char), unicode.IsSymbol(char):
			continue
		default:
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func stableClusterID(questionKey string) string {
	sum := sha256.Sum256([]byte(questionKey))
	return "cluster-" + fmt.Sprintf("%x", sum[:16])
}

func upsertQuestionCluster(tx *gorm.DB, event model.DashboardQuestionEvent, questionKey string) error {
	var cluster model.DashboardQuestionCluster
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", event.ClusterID).First(&cluster).Error
	if err == gorm.ErrRecordNotFound {
		cluster = model.DashboardQuestionCluster{
			ID: event.ClusterID, QuestionKey: questionKey,
			RepresentativeQuestion: event.Question, QuestionCount: 1,
			LastAskedAt: &event.AskedAt, Videos: "[]",
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&cluster)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			return nil
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", event.ClusterID).First(&cluster).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		cluster.QuestionCount++
		if cluster.LastAskedAt == nil || event.AskedAt.After(*cluster.LastAskedAt) {
			cluster.LastAskedAt = &event.AskedAt
		}
	}

	videos := decodeClusterVideos(cluster.Videos)
	if event.VideoID != "" {
		found := false
		for index := range videos {
			if videos[index].VideoID != event.VideoID {
				continue
			}
			found = true
			if event.AskedAt.Before(videos[index].askedAt) {
				videos[index].FirstSeconds = event.VideoSeconds
				videos[index].FirstTimestamp = formatClock(event.VideoSeconds)
				videos[index].askedAt = event.AskedAt
			}
			break
		}
		if !found {
			videos = append(videos, clusterVideoRecord{
				VideoID: event.VideoID, Title: event.VideoTitle, VideoCategory: event.VideoCategory,
				FirstSeconds: event.VideoSeconds, FirstTimestamp: formatClock(event.VideoSeconds), askedAt: event.AskedAt,
			})
		}
	}
	sort.Slice(videos, func(i, j int) bool {
		if videos[i].askedAt.Equal(videos[j].askedAt) {
			return videos[i].VideoID < videos[j].VideoID
		}
		return videos[i].askedAt.Before(videos[j].askedAt)
	})
	cluster.RelatedVideoCount = len(videos)
	cluster.Videos = encodeClusterVideos(videos)
	if cluster.ID == "" {
		cluster.ID = event.ClusterID
	}
	if err == gorm.ErrRecordNotFound {
		return tx.Create(&cluster).Error
	}
	return tx.Save(&cluster).Error
}

type clusterVideoRecord struct {
	VideoID        string
	Title          string
	VideoCategory  string
	FirstSeconds   int
	FirstTimestamp string
	askedAt        time.Time
}

func decodeClusterVideos(raw string) []clusterVideoRecord {
	var stored []struct {
		VideoID        string `json:"video_id"`
		Title          string `json:"title"`
		VideoCategory  string `json:"video_category"`
		FirstSeconds   int    `json:"first_seconds"`
		FirstTimestamp string `json:"first_timestamp"`
		AskedAt        string `json:"asked_at"`
	}
	if json.Unmarshal([]byte(raw), &stored) != nil {
		return nil
	}
	out := make([]clusterVideoRecord, 0, len(stored))
	for _, item := range stored {
		askedAt, _ := time.Parse(time.RFC3339Nano, item.AskedAt)
		out = append(out, clusterVideoRecord{
			VideoID: item.VideoID, Title: item.Title, VideoCategory: item.VideoCategory,
			FirstSeconds: item.FirstSeconds, FirstTimestamp: item.FirstTimestamp, askedAt: askedAt,
		})
	}
	return out
}

func encodeClusterVideos(videos []clusterVideoRecord) string {
	stored := make([]map[string]any, 0, len(videos))
	for _, item := range videos {
		stored = append(stored, map[string]any{
			"video_id": item.VideoID, "title": item.Title, "video_category": item.VideoCategory,
			"first_seconds": item.FirstSeconds, "first_timestamp": item.FirstTimestamp,
			"asked_at": item.askedAt.Format(time.RFC3339Nano),
		})
	}
	raw, _ := json.Marshal(stored)
	return string(raw)
}

func upsertQuestionStat(tx *gorm.DB, now time.Time) error {
	statDate := now.In(dashboardLocation).Format("2006-01-02")
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, dashboardLocation)
	end := start.AddDate(0, 0, 1)
	var events []model.DashboardQuestionEvent
	if err := tx.Where("asked_at >= ? AND asked_at < ?", start, end).Find(&events).Error; err != nil {
		return err
	}
	topVideos := make(map[string]dashboardTopVideo)
	clusterIDs := make(map[string]struct{})
	videoIDs := make(map[string]struct{})
	for _, event := range events {
		if event.ClusterID != "" {
			clusterIDs[event.ClusterID] = struct{}{}
		}
		if event.VideoID == "" {
			continue
		}
		videoIDs[event.VideoID] = struct{}{}
		item := topVideos[event.VideoID]
		item.VideoID, item.Title = event.VideoID, event.VideoTitle
		item.Count++
		topVideos[event.VideoID] = item
	}
	top := make([]dashboardTopVideo, 0, len(topVideos))
	for _, item := range topVideos {
		top = append(top, item)
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Count == top[j].Count {
			return top[i].Title < top[j].Title
		}
		return top[i].Count > top[j].Count
	})
	if len(top) > 3 {
		top = top[:3]
	}
	encodedTop, err := json.Marshal(top)
	if err != nil {
		return err
	}

	var stat model.DashboardQuestionStat
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("stat_date = ?", statDate).First(&stat).Error
	if err == gorm.ErrRecordNotFound {
		stat = model.DashboardQuestionStat{ID: uuid.NewString(), StatDate: statDate}
	} else if err != nil {
		return err
	}
	stat.QuestionCount = len(events)
	stat.ActiveVideoCount = len(videoIDs)
	stat.ClusterCount = len(clusterIDs)
	stat.TopVideos = string(encodedTop)
	if stat.ID == "" {
		stat.ID = uuid.NewString()
	}
	if err == gorm.ErrRecordNotFound {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&stat)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			return nil
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("stat_date = ?", statDate).First(&stat).Error; err != nil {
			return err
		}
	}
	return tx.Save(&stat).Error
}

func (h *DashboardHandler) Get(c *gin.Context) {
	query, err := parseDashboardQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	stats, err := h.loadStats(c, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "读取提问统计失败: " + err.Error()})
		return
	}
	events, err := h.loadEvents(c, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "读取提问事件失败: " + err.Error()})
		return
	}
	clusters, err := h.loadClusters(c, query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "读取问题聚类失败: " + err.Error()})
		return
	}

	payload := buildDashboardPayload(query, stats, clusters, events)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": payload})
}

func parseDashboardQuery(c *gin.Context) (dashboardQuery, error) {
	query := dashboardQuery{Range: strings.TrimSpace(c.DefaultQuery("range", "7d"))}
	switch query.Range {
	case "7d", "30d", "90d":
		days, _ := strconv.Atoi(strings.TrimSuffix(query.Range, "d"))
		to := localToday()
		from := to.AddDate(0, 0, -(days - 1))
		query.From, query.To = from.Format("2006-01-02"), to.Format("2006-01-02")
	case "custom":
		query.From = strings.TrimSpace(c.Query("from"))
		query.To = strings.TrimSpace(c.Query("to"))
		if query.From == "" || query.To == "" {
			return dashboardQuery{}, fmt.Errorf("请选择自定义日期范围")
		}
	default:
		return dashboardQuery{}, fmt.Errorf("不支持的时间范围")
	}

	from, err := time.Parse("2006-01-02", query.From)
	if err != nil {
		return dashboardQuery{}, fmt.Errorf("开始日期格式无效")
	}
	to, err := time.Parse("2006-01-02", query.To)
	if err != nil {
		return dashboardQuery{}, fmt.Errorf("结束日期格式无效")
	}
	if from.After(to) {
		return dashboardQuery{}, fmt.Errorf("开始日期不能晚于结束日期")
	}
	if to.Sub(from)/(24*time.Hour)+1 > 90 {
		return dashboardQuery{}, fmt.Errorf("自定义时间范围最长 90 天")
	}
	if to.After(localToday()) {
		return dashboardQuery{}, fmt.Errorf("结束日期不能晚于今天")
	}
	return query, nil
}

func localToday() time.Time {
	now := time.Now().In(dashboardLocation)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, dashboardLocation)
}

func (h *DashboardHandler) loadStats(c *gin.Context, query dashboardQuery) ([]model.DashboardQuestionStat, error) {
	var stats []model.DashboardQuestionStat
	err := h.db.WithContext(c.Request.Context()).
		Where("stat_date >= ? AND stat_date <= ?", query.From, query.To).
		Order("stat_date ASC").
		Find(&stats).Error
	return stats, err
}

func (h *DashboardHandler) loadEvents(c *gin.Context, query dashboardQuery) ([]model.DashboardQuestionEvent, error) {
	start, end, err := dashboardRange(query)
	if err != nil {
		return nil, err
	}
	var events []model.DashboardQuestionEvent
	err = h.db.WithContext(c.Request.Context()).
		Where("asked_at >= ? AND asked_at < ?", start, end).
		Order("asked_at ASC, id ASC").
		Find(&events).Error
	return events, err
}

func (h *DashboardHandler) loadClusters(c *gin.Context, query dashboardQuery) ([]model.DashboardQuestionCluster, error) {
	var clusters []model.DashboardQuestionCluster
	start, endExclusive, err := dashboardRange(query)
	if err != nil {
		return nil, err
	}
	err = h.db.WithContext(c.Request.Context()).
		Where("last_asked_at >= ? AND last_asked_at < ?", start, endExclusive).
		Order("question_count DESC, last_asked_at DESC").
		Find(&clusters).Error
	return clusters, err
}

func dashboardRange(query dashboardQuery) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation("2006-01-02", query.From, dashboardLocation)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("开始日期格式无效")
	}
	end, err := time.ParseInLocation("2006-01-02", query.To, dashboardLocation)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("结束日期格式无效")
	}
	return start, end.AddDate(0, 0, 1), nil
}

func buildDashboardPayload(query dashboardQuery, stats []model.DashboardQuestionStat, clusters []model.DashboardQuestionCluster, events []model.DashboardQuestionEvent) dashboardPayload {
	payload := dashboardPayload{Range: query.Range, From: query.From, To: query.To, Trend: make([]dashboardTrendPoint, 0, len(stats)), Clusters: make([]dashboardCluster, 0, len(clusters))}
	activeVideos, clusterCount := 0, 0
	for _, stat := range stats {
		topVideos := make([]dashboardTopVideo, 0)
		var raw []dashboardTopVideo
		if json.Unmarshal([]byte(stat.TopVideos), &raw) == nil {
			topVideos = raw
		}
		payload.Trend = append(payload.Trend, dashboardTrendPoint{Date: stat.StatDate, Count: stat.QuestionCount, TopVideos: topVideos})
		if stat.ActiveVideoCount > activeVideos {
			activeVideos = stat.ActiveVideoCount
		}
		if stat.ClusterCount > clusterCount {
			clusterCount = stat.ClusterCount
		}
		payload.KPI.TotalQuestions += stat.QuestionCount
	}

	if len(events) > 0 {
		videoIDs := make(map[string]struct{})
		clusterIDs := make(map[string]struct{})
		for _, event := range events {
			if event.VideoID != "" {
				videoIDs[event.VideoID] = struct{}{}
			}
			if event.ClusterID != "" {
				clusterIDs[event.ClusterID] = struct{}{}
			}
		}
		payload.KPI.TotalQuestions = len(events)
		payload.KPI.ActiveVideos = len(videoIDs)
		payload.KPI.ClusterCount = len(clusterIDs)
		if payload.KPI.ActiveVideos > 0 {
			payload.KPI.AvgQuestionsPerVideo = payload.KPI.TotalQuestions / payload.KPI.ActiveVideos
		}
	} else if activeVideos > 0 {
		payload.KPI.ActiveVideos = activeVideos
		payload.KPI.AvgQuestionsPerVideo = payload.KPI.TotalQuestions / activeVideos
	}
	if len(events) == 0 {
		payload.KPI.ClusterCount = clusterCount
	}
	payload.KPI.Trend.TotalQuestions = 0
	payload.KPI.Trend.ActiveVideos = 0
	payload.KPI.Trend.ClusterCount = 0
	payload.KPI.Trend.AvgQuestionsPerVideo = 0

	if len(events) > 0 {
		clusters = clustersForEvents(events, clusters)
	}
	for _, cluster := range clusters {
		var videos []dashboardClusterVideo
		if json.Unmarshal([]byte(cluster.Videos), &videos) != nil {
			videos = []dashboardClusterVideo{}
		}
		lastAskedAt := ""
		if cluster.LastAskedAt != nil {
			lastAskedAt = cluster.LastAskedAt.Format("2006-01-02 15:04")
		}
		relatedVideoCount := cluster.RelatedVideoCount
		if relatedVideoCount == 0 {
			relatedVideoCount = len(videos)
		}
		payload.Clusters = append(payload.Clusters, dashboardCluster{
			ID: cluster.ID, RepresentativeQuestion: cluster.RepresentativeQuestion,
			Count: cluster.QuestionCount, RelatedVideoCount: relatedVideoCount,
			LastAskedAt: lastAskedAt, Videos: videos,
		})
	}

	return payload
}

func clustersForEvents(events []model.DashboardQuestionEvent, persisted []model.DashboardQuestionCluster) []model.DashboardQuestionCluster {
	persistedByID := make(map[string]model.DashboardQuestionCluster, len(persisted))
	for _, cluster := range persisted {
		persistedByID[cluster.ID] = cluster
	}
	type groupedCluster struct {
		cluster model.DashboardQuestionCluster
		videos  map[string]clusterVideoRecord
	}
	grouped := make(map[string]*groupedCluster)
	for _, event := range events {
		if event.ClusterID == "" {
			continue
		}
		item := grouped[event.ClusterID]
		if item == nil {
			cluster := persistedByID[event.ClusterID]
			if cluster.RepresentativeQuestion == "" {
				cluster.RepresentativeQuestion = event.Question
			}
			item = &groupedCluster{
				cluster: model.DashboardQuestionCluster{
					ID:                     event.ClusterID,
					RepresentativeQuestion: cluster.RepresentativeQuestion,
				},
				videos: make(map[string]clusterVideoRecord),
			}
			grouped[event.ClusterID] = item
		}
		item.cluster.QuestionCount++
		if item.cluster.LastAskedAt == nil || event.AskedAt.After(*item.cluster.LastAskedAt) {
			askedAt := event.AskedAt
			item.cluster.LastAskedAt = &askedAt
		}
		if event.VideoID == "" {
			continue
		}
		video, exists := item.videos[event.VideoID]
		if !exists || event.AskedAt.Before(video.askedAt) {
			item.videos[event.VideoID] = clusterVideoRecord{
				VideoID: event.VideoID, Title: event.VideoTitle, VideoCategory: event.VideoCategory,
				FirstSeconds: event.VideoSeconds, FirstTimestamp: formatClock(event.VideoSeconds), askedAt: event.AskedAt,
			}
		}
	}
	out := make([]model.DashboardQuestionCluster, 0, len(grouped))
	for _, item := range grouped {
		videos := make([]clusterVideoRecord, 0, len(item.videos))
		for _, video := range item.videos {
			videos = append(videos, video)
		}
		sort.Slice(videos, func(i, j int) bool {
			if videos[i].askedAt.Equal(videos[j].askedAt) {
				return videos[i].VideoID < videos[j].VideoID
			}
			return videos[i].askedAt.Before(videos[j].askedAt)
		})
		item.cluster.RelatedVideoCount = len(videos)
		item.cluster.Videos = encodeClusterVideos(videos)
		out = append(out, item.cluster)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].QuestionCount == out[j].QuestionCount {
			if out[i].LastAskedAt == nil || out[j].LastAskedAt == nil {
				return out[i].ID < out[j].ID
			}
			return out[i].LastAskedAt.After(*out[j].LastAskedAt)
		}
		return out[i].QuestionCount > out[j].QuestionCount
	})
	return out
}

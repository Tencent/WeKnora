package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestTranscriptImportCreatesIndexPipeline(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}, &model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	videoID := uuid.NewString()
	if err := db.Create(&model.Video{ID: videoID, Title: "本地导入测试", VideoType: "general", DurationSeconds: 10, Status: model.VideoStatusUploaded}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	storage, err := objstore.New(config.MinIOConfig{Backend: "local", Bucket: "vidsage", PublicURL: "http://127.0.0.1:8090/api/custom/files", LocalDir: t.TempDir()})
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}
	srt := "1\n00:00:00,000 --> 00:00:05,000\n第一句\n\n2\n00:00:05,000 --> 00:00:10,000\n[说话人 2] 第二句\n"
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "sample.srt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte(srt)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: videoID}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/custom/videos/"+videoID+"/transcript/import", body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	NewTranscriptImportHandler(db, storage).Import(c)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		SubtitleCount int    `json:"subtitle_count"`
		IndexJobID    string `json:"index_job_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.SubtitleCount != 2 || response.IndexJobID == "" {
		t.Fatalf("response = %#v", response)
	}
	var jobs []model.VideoProcessingJob
	if err := db.Where("video_id = ?", videoID).Find(&jobs).Error; err != nil {
		t.Fatalf("load jobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("job count = %d, want 3", len(jobs))
	}
	var video model.Video
	if err := db.First(&video, "id = ?", videoID).Error; err != nil {
		t.Fatalf("load video: %v", err)
	}
	if video.TranscriptRevision != 1 || video.Status != model.VideoStatusProcessing || video.SubtitleFileURL == "" {
		t.Fatalf("video = %#v", video)
	}
}

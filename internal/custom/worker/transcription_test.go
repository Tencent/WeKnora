package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/tongyi"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestTranscriptionUsesPersistentSourceForValidationAndTask(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	video := model.Video{
		ID:                     uuid.NewString(),
		Title:                  "AV1 source",
		FileURL:                "https://cdn.example.com/video-av1.mp4",
		TranscriptionSourceURL: "https://cdn.example.com/video-h264-aac.mp4",
		Status:                 model.VideoStatusReady,
	}
	job := model.VideoProcessingJob{
		ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Status: "running", MaxAttempts: 3,
		IdempotencyKey: "transcription:" + video.ID,
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	client := &recordingTongyiClient{createErr: errors.New("stop after source contract check")}
	err = NewTranscriptionHandler(db, client).Run(context.Background(), &job, &video)
	if err == nil || client.validatedURL != video.TranscriptionSourceURL || client.createdURL != video.TranscriptionSourceURL {
		t.Fatalf("source contract = err=%v validated=%q created=%q", err, client.validatedURL, client.createdURL)
	}
}

func TestTranscriptionFallsBackToPlaybackSourceForLegacyVideo(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	video := model.Video{ID: uuid.NewString(), Title: "legacy", FileURL: "https://cdn.example.com/legacy.mp4", Status: model.VideoStatusReady}
	job := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Status: "running", MaxAttempts: 3, IdempotencyKey: "transcription:" + video.ID}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	client := &recordingTongyiClient{validateErr: errors.New("stop after source contract check")}
	_ = NewTranscriptionHandler(db, client).Run(context.Background(), &job, &video)
	if client.validatedURL != video.FileURL {
		t.Fatalf("legacy source = %q, want %q", client.validatedURL, video.FileURL)
	}
}

type recordingTongyiClient struct {
	validatedURL string
	createdURL   string
	validateErr  error
	createErr    error
}

func (c *recordingTongyiClient) ValidateSourceFile(_ context.Context, fileURL string) error {
	c.validatedURL = fileURL
	return c.validateErr
}

func (c *recordingTongyiClient) CreateTask(_ context.Context, req tongyi.CreateTaskRequest) (*tongyi.CreateTaskResponse, error) {
	c.createdURL = req.FileURL
	if c.createErr != nil {
		return nil, c.createErr
	}
	return &tongyi.CreateTaskResponse{TaskID: "task-1"}, nil
}

func (c *recordingTongyiClient) GetTask(context.Context, string) (*tongyi.GetTaskResponse, error) {
	return &tongyi.GetTaskResponse{Status: "FAILED", ErrorCode: "test", ErrorMessage: "test"}, nil
}

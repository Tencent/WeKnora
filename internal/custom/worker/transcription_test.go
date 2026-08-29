package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	err = NewTranscriptionHandler(db, client, "http://custom-backend:8090").Run(context.Background(), &job, &video)
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

func TestTranscriptionPersistsPreparedCompatibleSource(t *testing.T) {
	db := openTranscriptionTestDB(t)
	video := model.Video{ID: uuid.NewString(), Title: "AV1 source", FileURL: "https://cdn.example.com/source.mp4", Status: model.VideoStatusReady}
	job := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Status: "running", MaxAttempts: 3, IdempotencyKey: "transcription:" + video.ID}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	client := &recordingTongyiClient{createErr: errors.New("stop after source contract check")}
	handler := NewTranscriptionHandler(db, client)
	handler.SourcePreparer = &recordingSourcePreparer{preparedURL: "https://cdn.example.com/transcription-source-h264-aac.mp4"}

	err := handler.Run(context.Background(), &job, &video)
	if err == nil || client.createdURL != "https://cdn.example.com/transcription-source-h264-aac.mp4" {
		t.Fatalf("prepared source = err=%v created=%q", err, client.createdURL)
	}
	var got model.Video
	if err := db.First(&got, "id = ?", video.ID).Error; err != nil {
		t.Fatalf("reload video: %v", err)
	}
	if got.TranscriptionSourceURL != "https://cdn.example.com/transcription-source-h264-aac.mp4" {
		t.Fatalf("persisted source = %q", got.TranscriptionSourceURL)
	}
}

func TestTranscriptionDoesNotCreateTaskWhenSourcePreparationFails(t *testing.T) {
	db := openTranscriptionTestDB(t)
	video := model.Video{ID: uuid.NewString(), Title: "AV1 source", FileURL: "https://cdn.example.com/source.mp4", Status: model.VideoStatusReady}
	job := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Status: "running", MaxAttempts: 3, IdempotencyKey: "transcription:" + video.ID}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	client := &recordingTongyiClient{}
	handler := NewTranscriptionHandler(db, client)
	handler.SourcePreparer = &recordingSourcePreparer{prepareErr: errors.New("ffmpeg failed")}

	if err := handler.Run(context.Background(), &job, &video); err == nil {
		t.Fatal("expected source preparation error")
	}
	if client.validatedURL != "" || client.createdURL != "" {
		t.Fatalf("tingwu called after preparation failure: validated=%q created=%q", client.validatedURL, client.createdURL)
	}
}

func TestTranscriptionSourcePreparationTimeoutDoesNotCreateTask(t *testing.T) {
	db := openTranscriptionTestDB(t)
	video := model.Video{ID: uuid.NewString(), Title: "slow source", FileURL: "https://cdn.example.com/source.mp4", Status: model.VideoStatusReady}
	job := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Status: "running", MaxAttempts: 3, IdempotencyKey: "transcription:" + video.ID}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	client := &recordingTongyiClient{}
	handler := NewTranscriptionHandler(db, client)
	handler.SourcePreparationTimeout = 10 * time.Millisecond
	handler.SourcePreparer = &blockingSourcePreparer{}

	err := handler.Run(context.Background(), &job, &video)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v, want deadline exceeded", err)
	}
	if client.validatedURL != "" || client.createdURL != "" {
		t.Fatalf("tingwu called after preparation timeout: validated=%q created=%q", client.validatedURL, client.createdURL)
	}
}

func TestProbedMediaCompatibilityRequiresH264AACMP4(t *testing.T) {
	tests := []struct {
		name       string
		videoCodec string
		audioCodec string
		format     string
		want       bool
	}{
		{name: "compatible", videoCodec: "h264", audioCodec: "aac", format: "mov,mp4,m4a,3gp,3g2,mj2", want: true},
		{name: "av1 video", videoCodec: "av1", audioCodec: "aac", format: "mov,mp4,m4a,3gp,3g2,mj2"},
		{name: "non aac audio", videoCodec: "h264", audioCodec: "opus", format: "mov,mp4,m4a,3gp,3g2,mj2"},
		{name: "non mp4 container", videoCodec: "h264", audioCodec: "aac", format: "matroska,webm"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			media := probedMedia{
				Streams: []struct {
					CodecType string `json:"codec_type"`
					CodecName string `json:"codec_name"`
				}{{CodecType: "video", CodecName: test.videoCodec}, {CodecType: "audio", CodecName: test.audioCodec}},
				Format: mediaFormat{FormatName: test.format},
			}
			if got := media.isCompatible(); got != test.want {
				t.Fatalf("isCompatible() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMediaDurationAcceptsStringAndNumber(t *testing.T) {
	for _, raw := range []string{`{"duration":"12.5"}`, `{"duration":12.5}`} {
		var media mediaFormat
		if err := json.Unmarshal([]byte(raw), &media); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if float64(media.Duration) != 12.5 {
			t.Fatalf("duration from %s = %v, want 12.5", raw, media.Duration)
		}
	}
}

func TestMediaSourcePreparerUsesInternalURLForExistingPublicSource(t *testing.T) {
	preparer := &mediaSourcePreparer{InternalFrontendBaseURL: "http://custom-backend:8090"}
	got := preparer.internalSourceURL("http://42.194.211.189/api/custom/files/videos/source.mp4?download=1")
	want := "http://custom-backend:8090/api/custom/files/videos/source.mp4?download=1"
	if got != want {
		t.Fatalf("internal source URL = %q, want %q", got, want)
	}
}

func TestMediaSourcePreparerKeepsSignedObjectURL(t *testing.T) {
	preparer := &mediaSourcePreparer{InternalFrontendBaseURL: "http://custom-backend:8090"}
	source := "http://minio:9000/vidsage/videos/source.mp4?X-Amz-Signature=redacted"
	if got := preparer.internalSourceURL(source); got != source {
		t.Fatalf("signed object URL = %q, want unchanged", got)
	}
}

func openTranscriptionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

type recordingSourcePreparer struct {
	preparedURL string
	prepareErr  error
}

type blockingSourcePreparer struct{}

func (p *blockingSourcePreparer) Prepare(ctx context.Context, _ *model.Video) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (p *recordingSourcePreparer) Prepare(context.Context, *model.Video) (string, error) {
	return p.preparedURL, p.prepareErr
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

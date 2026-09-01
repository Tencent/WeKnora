package worker

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/mps"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestMPSegmentsToParagraphsPreservesStableEvidenceAndTiming(t *testing.T) {
	paragraphs := mpsSegmentsToParagraphs([]mps.Segment{{
		SourceSegmentID: "mps:task-1:000007", Text: "章节定位文本", StartMs: 1200, EndMs: 3400, SpeakerID: "2",
	}})
	require.Len(t, paragraphs, 1)
	require.Equal(t, "mps:task-1:000007", paragraphs[0].ParagraphID)
	require.Equal(t, "mps:task-1:000007", paragraphs[0].Sentences[0].SentenceID)
	require.Equal(t, 1200, paragraphs[0].Sentences[0].StartMs)
	require.Equal(t, 3400, paragraphs[0].Sentences[0].EndMs)
}

func TestNormalizeProviderKeepsHistoricalTingwuAndMPSIsolated(t *testing.T) {
	require.Equal(t, "aliyun_tingwu", normalizeProvider(""))
	require.Equal(t, "aliyun_tingwu", normalizeProvider("tingwu"))
	require.Equal(t, "aliyun_tingwu", normalizeProvider("aliyun_tingwu"))
	require.Equal(t, "tencent_mps", normalizeProvider("tencent_mps"))
}

func TestMPSSessionIDUsesProviderSafeCharacters(t *testing.T) {
	require.Equal(t, "transcription-job-123", mpsSessionID("job:123"))
	require.NotContains(t, mpsSessionID("job/123"), "/")
	require.LessOrEqual(t, len(mpsSessionID("abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz")), 50)
}

type fakeMPSClient struct {
	task      *mps.Task
	sourceURL string
}

func (f *fakeMPSClient) CreateTask(_ context.Context, sourceURL, _ string) (string, error) {
	f.sourceURL = sourceURL
	return f.task.TaskID, nil
}
func (f *fakeMPSClient) GetTask(context.Context, string) (*mps.Task, error) { return f.task, nil }
func (f *fakeMPSClient) Timeout() time.Duration                             { return time.Second }
func (f *fakeMPSClient) PollInterval() time.Duration                        { return time.Millisecond }

func TestMPSRunPersistsResultAndEnqueuesProviderBoundJobs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}))
	video := model.Video{ID: uuid.NewString(), FileURL: "https://cdn.example/video.mp4", Status: model.VideoStatusReady}
	job := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Provider: mps.Provider, Status: "running", MaxAttempts: 3, IdempotencyKey: "transcription:" + video.ID}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create(&job).Error)
	client := &fakeMPSClient{task: &mps.Task{TaskID: "mps-task", Status: "FINISH", Progress: 100, Result: mps.Result{Segments: []mps.Segment{{SourceSegmentID: "mps:mps-task:000000", Text: "内容", StartMs: 0, EndMs: 1000}}}}}
	h := NewTranscriptionHandler(db, nil)
	h.MPS = client
	require.NoError(t, h.Run(context.Background(), &job, &video))
	var jobs []model.VideoProcessingJob
	require.NoError(t, db.Where("video_id = ?", video.ID).Order("job_type").Find(&jobs).Error)
	require.Len(t, jobs, 4)
	for _, child := range jobs[1:] {
		require.Equal(t, "mps:mps-task", child.TranscriptGeneration)
		if child.JobType == "subtitle_generate" {
			require.Equal(t, mps.Provider, child.Provider)
		}
		if child.JobType == "outline" || child.JobType == "summary" {
			require.Equal(t, "draft", child.ResultStage)
		}
	}
}

type recordingMPSInputPreparer struct {
	preparedURL string
	called      int
}

func (p *recordingMPSInputPreparer) Prepare(context.Context, *model.Video, string) (string, error) {
	p.called++
	return p.preparedURL, nil
}

func TestMPSRunPreparesPrivateSourceBeforeCreatingTask(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}))
	video := model.Video{ID: uuid.NewString(), FileURL: "http://localhost/api/custom/files/videos/local/source.mp4", Status: model.VideoStatusReady}
	job := model.VideoProcessingJob{ID: uuid.NewString(), VideoID: video.ID, JobType: "transcription", Provider: mps.Provider, Status: "running", MaxAttempts: 3, IdempotencyKey: "transcription:" + video.ID}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create(&job).Error)

	client := &fakeMPSClient{task: &mps.Task{TaskID: "mps-task", Status: "FINISH", Progress: 100, Result: mps.Result{Segments: []mps.Segment{{Text: "content", StartMs: 0, EndMs: 1000}}}}}
	preparer := &recordingMPSInputPreparer{preparedURL: "https://mps-input.example.com/local/source.mp4?signature=test"}
	handler := NewTranscriptionHandler(db, nil)
	handler.MPS = client
	handler.MPSInputPreparer = preparer

	require.NoError(t, handler.Run(context.Background(), &job, &video))
	require.Equal(t, 1, preparer.called)
	require.Equal(t, preparer.preparedURL, client.sourceURL)
}

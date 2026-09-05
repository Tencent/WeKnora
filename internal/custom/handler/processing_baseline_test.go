package handler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestOutlineStatusDoesNotRegressFromRunningToPending(t *testing.T) {
	now := time.Now().UTC()
	video := model.Video{
		ID:                   "video-baseline",
		Status:               model.VideoStatusProcessing,
		TranscriptGeneration: "generation-baseline",
	}
	status := buildProcessingStatus(video, []model.VideoProcessingJob{
		{
			ID:                   "outline-draft-running",
			VideoID:              video.ID,
			JobType:              "outline",
			TranscriptGeneration: video.TranscriptGeneration,
			ResultStage:          "draft",
			Status:               "running",
			UpdatedAt:            now,
		},
		{
			ID:                   "outline-final-pending",
			VideoID:              video.ID,
			JobType:              "outline",
			TranscriptGeneration: video.TranscriptGeneration,
			ResultStage:          "final",
			Status:               "pending",
			UpdatedAt:            now.Add(time.Second),
		},
	})

	outlineJob := requireJob(t, status.Jobs, "outline")
	require.Equal(t, "running", outlineJob.Status,
		"a newer final pending job must not hide the already-running outline draft")
}

func requireJob(t *testing.T, jobs []ProcessingJobStatus, jobType string) ProcessingJobStatus {
	t.Helper()
	for _, job := range jobs {
		if job.JobType == jobType {
			return job
		}
	}
	t.Fatalf("job %q not found in %#v", jobType, jobs)
	return ProcessingJobStatus{}
}

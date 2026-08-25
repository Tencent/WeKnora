package worker

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

func TestCleanupStuckUploadsMarksOnlyOrphanedRecordsFailed(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	orphanID := uuid.NewString()
	activeID := uuid.NewString()
	withJobID := uuid.NewString()
	for _, video := range []model.Video{
		{ID: orphanID, Title: "orphan", Status: model.VideoStatusUploading, CreatedAt: now.Add(-31 * time.Minute), UpdatedAt: now.Add(-31 * time.Minute)},
		{ID: activeID, Title: "active", Status: model.VideoStatusUploading, CreatedAt: now.Add(-31 * time.Minute), UpdatedAt: now.Add(-5 * time.Minute)},
		{ID: withJobID, Title: "job", Status: model.VideoStatusUploading, CreatedAt: now.Add(-31 * time.Minute), UpdatedAt: now.Add(-31 * time.Minute)},
	} {
		if err := db.Create(&video).Error; err != nil {
			t.Fatalf("create video: %v", err)
		}
	}
	if err := db.Create(&model.VideoProcessingJob{
		ID:             uuid.NewString(),
		VideoID:        withJobID,
		JobType:        "thumbnail",
		Provider:       "local",
		IdempotencyKey: "thumbnail:" + withJobID,
		Status:         "pending",
	}).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	updated, err := CleanupStuckUploads(db, now, 30*time.Minute)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated rows = %d, want 1", updated)
	}

	var orphan, active, withJob model.Video
	for id, target := range map[string]*model.Video{orphanID: &orphan, activeID: &active, withJobID: &withJob} {
		if err := db.First(target, "id = ?", id).Error; err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
	}
	if orphan.Status != model.VideoStatusFailed || orphan.ProcessingErrorSummary == "" {
		t.Fatalf("orphan video not failed: status=%q reason=%q", orphan.Status, orphan.ProcessingErrorSummary)
	}
	if active.Status != model.VideoStatusUploading {
		t.Fatalf("active video status = %q, want %q", active.Status, model.VideoStatusUploading)
	}
	if withJob.Status != model.VideoStatusUploading {
		t.Fatalf("video with job status = %q, want %q", withJob.Status, model.VideoStatusUploading)
	}
}

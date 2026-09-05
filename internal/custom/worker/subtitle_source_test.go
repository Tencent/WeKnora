package worker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/model"
	transcriptservice "github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

func TestPersistSourceResultStoresMetadataInResultPayload(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.VideoProcessingJob{}))
	job := model.VideoProcessingJob{ID: "index-job", VideoID: "video-1", JobType: "index", ResultPayload: `{"paragraphs":[]}`, InputPayload: `{"revision":1}`}
	require.NoError(t, db.Create(&job).Error)

	handler := &IndexHandler{DB: db}
	err = handler.persistSourceResult(&job, transcriptservice.SourceResult{
		KnowledgeID: "source-knowledge-1", ContentHash: "hash-1", Action: "created",
	})
	require.NoError(t, err)
	require.Contains(t, job.ResultPayload, "source-knowledge-1")
	require.NotContains(t, job.InputPayload, "source-knowledge-1")

	var stored model.VideoProcessingJob
	require.NoError(t, db.First(&stored, "id = ?", job.ID).Error)
	require.Contains(t, stored.ResultPayload, "hash-1")
}

func TestPersistSourceResultRejectsMalformedResultPayload(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.VideoProcessingJob{}))
	job := model.VideoProcessingJob{ID: "index-job-invalid", VideoID: "video-1", JobType: "index", ResultPayload: "not-json"}
	require.NoError(t, db.Create(&job).Error)

	handler := &IndexHandler{DB: db}
	err = handler.persistSourceResult(&job, transcriptservice.SourceResult{KnowledgeID: "source-knowledge-1"})
	require.ErrorContains(t, err, "parse index result")
}

func TestRetirePendingContentJobsKeepsCurrentGeneration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"-retire?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.VideoProcessingJob{}))
	old := model.VideoProcessingJob{ID: "old", VideoID: "video-1", JobType: "summary", TranscriptGeneration: "old-generation", Status: "pending", IdempotencyKey: "old"}
	current := model.VideoProcessingJob{ID: "current", VideoID: "video-1", JobType: "summary", TranscriptGeneration: "new-generation", Status: "pending", IdempotencyKey: "current"}
	running := model.VideoProcessingJob{ID: "running", VideoID: "video-1", JobType: "outline", TranscriptGeneration: "old-generation", Status: "running", IdempotencyKey: "running"}
	require.NoError(t, db.Create(&old).Error)
	require.NoError(t, db.Create(&current).Error)
	require.NoError(t, db.Create(&running).Error)
	require.NoError(t, retirePendingContentJobs(db, "video-1", "new-generation"))
	var gotOld, gotCurrent, gotRunning model.VideoProcessingJob
	require.NoError(t, db.First(&gotOld, "id = ?", old.ID).Error)
	require.NoError(t, db.First(&gotCurrent, "id = ?", current.ID).Error)
	require.NoError(t, db.First(&gotRunning, "id = ?", running.ID).Error)
	require.Equal(t, "cancelled", gotOld.Status)
	require.Equal(t, "stale_transcript_generation", gotOld.ErrorCode)
	require.False(t, gotOld.CompletedAt == nil || gotOld.CompletedAt.After(time.Now().UTC()))
	require.Equal(t, "pending", gotCurrent.Status)
	require.Equal(t, "running", gotRunning.Status)
}

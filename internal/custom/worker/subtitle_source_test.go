package worker

import (
	"testing"

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

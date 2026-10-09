package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type imageJobKB struct {
	interfaces.KnowledgeBaseService
	kb    *types.KnowledgeBase
	image []byte
	fail  bool
}

func (s *imageJobKB) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s *imageJobKB) ReadChunkImage(context.Context, *types.SearchResult) ([]byte, error) {
	if s.fail {
		return nil, errors.New("temporary storage failure")
	}
	return s.image, nil
}

type imageJobModels struct {
	interfaces.ModelService
	row   *types.Model
	model embedding.Embedder
}

func (s *imageJobModels) GetModelByID(context.Context, string) (*types.Model, error) {
	return s.row, nil
}

func (s *imageJobModels) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return s.model, nil
}

type imageJobTenant struct{ interfaces.TenantRepository }

func (s imageJobTenant) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: 1, RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
		RetrieverType: types.VectorRetrieverType, RetrieverEngineType: types.PostgresRetrieverEngineType,
	}}}}, nil
}

type imageJobQueue struct{ tasks []*asynq.Task }

func (q *imageJobQueue) Enqueue(t *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, t)
	return &asynq.TaskInfo{}, nil
}

func setupImageJobTest(t *testing.T) (
	*ImageVectorService, *gorm.DB, *imageJobKB, *imageJobQueue, *imageModel, *indexRecorder,
) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}, &types.Knowledge{}, &types.KnowledgeTag{}))
	for _, name := range []string{"000032_chunk_images", "000034_image_vector_jobs"} {
		sql, err := os.ReadFile("../../../migrations/sqlite/" + name + ".up.sql")
		require.NoError(t, err)
		require.NoError(t, db.Exec(string(sql)).Error)
	}
	kb := imageVectorKB()
	kb.TenantID = 1
	kb.EmbeddingModelID = "model"
	store := &imageJobKB{kb: kb, image: testPNG(t)}
	model := &imageModel{dims: 3}
	models := &imageJobModels{row: &types.Model{ID: "model", Name: "image-model"}, model: model}
	queue := &imageJobQueue{}
	recorder := &indexRecorder{}
	jobs := repository.NewImageVectorRepository(db)
	chunks := repository.NewChunkRepository(db)
	svc := NewImageVectorService(jobs, store, models, chunks, imageJobTenant{},
		parentChildRetrieveRegistry{engine: recorder}, nil, queue)
	ctx := types.WithExecutionTenant(t.Context(), 1)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", Title: "pictures",
	}).Error)
	source := &types.Chunk{
		ID: "source", TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "kb", ChunkType: types.ChunkTypeText,
		Content: "![image](resource://original)", ImageInfo: `[{"url":"resource://original"}]`, IsEnabled: true,
	}
	require.NoError(t, chunks.CreateChunks(ctx, []*types.Chunk{source}))
	return svc, db, store, queue, model, recorder
}

func TestIndependentImageVectorJobWithoutCaptionRetriesAndDeduplicates(t *testing.T) {
	svc, _, store, queue, model, recorder := setupImageJobTest(t)
	ctx := types.WithExecutionTenant(t.Context(), 1)
	jobs, chunks := svc.jobs, svc.chunks
	sourceContent := "![image](resource://original)"
	require.NoError(t, svc.Schedule(ctx, "kb", "doc", ""))
	require.Len(t, queue.tasks, 1)
	require.NoError(t, svc.Handle(ctx, queue.tasks[0]))
	require.Len(t, queue.tasks, 2)
	task := queue.tasks[1]
	store.fail = true
	require.Error(t, svc.Handle(ctx, task))
	coverage, err := svc.Coverage(ctx, "kb")
	require.NoError(t, err)
	require.EqualValues(t, 1, coverage.Failed)
	store.fail = false
	require.NoError(t, svc.Handle(ctx, task))
	require.NoError(t, svc.Handle(ctx, task))
	require.Len(t, model.images, 1, "successful retries must not call the model twice")
	require.Len(t, recorder.rows, 1)
	coverage, err = svc.Coverage(ctx, "kb")
	require.NoError(t, err)
	require.EqualValues(t, 1, coverage.Completed)
	var payload types.ImageVectorPayload
	require.NoError(t, json.Unmarshal(task.Payload(), &payload))
	job, err := jobs.Get(ctx, 1, payload.JobID)
	require.NoError(t, err)
	vector, err := chunks.GetChunkByID(ctx, 1, job.ChunkID)
	require.NoError(t, err)
	require.Equal(t, sourceContent, vector.Content)
	require.True(t, vector.IsEnabled)
	require.Positive(t, vector.SeqID)
	require.NoError(t, chunks.DeleteChunk(ctx, 1, vector.ID))
	require.NoError(t, svc.Handle(ctx, queue.tasks[0]))
	require.Len(t, queue.tasks, 3, "a reparse that removed the vector makes it missing again")
	require.NoError(t, svc.Handle(ctx, queue.tasks[2]))
	require.Len(t, model.images, 2)
	require.Error(t, svc.Schedule(types.WithExecutionTenant(t.Context(), 2), "kb", "", ""))
}

func TestImageVectorJobChecksCurrentConfigurationAndScannedPages(t *testing.T) {
	for _, tc := range []string{"disabled", "changed", "scanned"} {
		t.Run(tc, func(t *testing.T) {
			svc, db, store, queue, model, recorder := setupImageJobTest(t)
			ctx := types.WithExecutionTenant(t.Context(), 1)
			require.NoError(t, svc.Schedule(ctx, "kb", "doc", ""))
			require.NoError(t, svc.Handle(ctx, queue.tasks[0]))
			switch tc {
			case "disabled":
				store.kb.ImageProcessingConfig.ImageVectorEnabled = false
			case "changed":
				svc.models.(*imageJobModels).row.Name = "new-vector-space"
			case "scanned":
				require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "source").
					Update("image_info", `[{"url":"resource://original","source_type":"scanned_pdf"}]`).Error)
			}
			require.NoError(t, svc.Handle(ctx, queue.tasks[1]))
			require.Empty(t, model.images)
			require.Empty(t, recorder.rows)
			var job types.ImageVectorJob
			require.NoError(t, db.First(&job).Error)
			require.Equal(t, "skipped", job.Status)
		})
	}
}

func TestImageVectorBackfillScansBeyondFirstPage(t *testing.T) {
	svc, _, _, queue, _, _ := setupImageJobTest(t)
	ctx := types.WithExecutionTenant(t.Context(), 1)
	var chunks []*types.Chunk
	for i := 0; i < 105; i++ {
		chunks = append(chunks, &types.Chunk{
			ID: fmt.Sprintf("source-%03d", i), TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc",
			ChunkType: types.ChunkTypeText, IsEnabled: true,
			ImageInfo: fmt.Sprintf(`[{"url":"resource://image-%03d"}]`, i),
		})
	}
	require.NoError(t, svc.chunks.CreateChunks(ctx, chunks))
	require.NoError(t, svc.Schedule(ctx, "kb", "", ""))
	require.NoError(t, svc.Handle(ctx, queue.tasks[0]))
	require.Len(t, queue.tasks, 107)
	coverage, err := svc.Coverage(ctx, "kb")
	require.NoError(t, err)
	require.EqualValues(t, 106, coverage.Pending)
}

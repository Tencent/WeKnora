package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/middleware/asynqdl"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type tableSummaryDeadLetterRecorder struct {
	interfaces.TaskDeadLetterRepository
	inserted []*types.TaskDeadLetter
}

func (r *tableSummaryDeadLetterRecorder) Insert(_ context.Context, row *types.TaskDeadLetter) error {
	r.inserted = append(r.inserted, row)
	return nil
}

func tableSummaryTask(t *testing.T, tenantID uint64, knowledgeID string) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(DataTableSummaryPayload{TenantID: tenantID, KnowledgeID: knowledgeID})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeDataTableSummary, payload)
}

func TestDataTableSummaryHandleSkipsDeletedKnowledgeWithoutDeadLetter(t *testing.T) {
	f := newDocumentWriteFixture(t)
	require.NoError(t, f.db.Delete(&types.Knowledge{}, "id = ?", "doc").Error)
	// Downstream dependencies are deliberately unset: a missing document must
	// finish before attempting to load its KB, models, file or retrieval engine.
	worker := &DataTableSummaryService{knowledgeService: f.svc}
	deadLetters := &tableSummaryDeadLetterRecorder{}
	handler := asynqdl.Middleware(deadLetters)(asynq.HandlerFunc(worker.Handle))

	err := handler.ProcessTask(context.Background(), tableSummaryTask(t, 7, "doc"))

	require.NoError(t, err, "an orphaned task must succeed, not return SkipRetry")
	require.Empty(t, deadLetters.inserted)
}

func TestDataTableSummaryHandleSkipsKnowledgeMissingFromTaskTenant(t *testing.T) {
	for _, tc := range []struct {
		name        string
		knowledgeID string
		tenantID    uint64
	}{
		{name: "never existed", knowledgeID: "missing", tenantID: 7},
		{name: "exists in another tenant", knowledgeID: "doc", tenantID: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			worker := &DataTableSummaryService{knowledgeService: f.svc}

			require.NoError(t, worker.Handle(context.Background(), tableSummaryTask(t, tc.tenantID, tc.knowledgeID)))
		})
	}
}

type tableSummaryKnowledgeLookup struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
	err       error
}

func (r *tableSummaryKnowledgeLookup) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return r.knowledge, r.err
}

func TestDataTableSummaryHandleClassifiesInitialLookupErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		skip bool
	}{
		{name: "wrapped not found", err: fmt.Errorf("lookup: %w", repository.ErrKnowledgeNotFound), skip: true},
		{name: "database failure", err: errors.New("database unavailable")},
		{name: "same text but not the sentinel", err: errors.New("knowledge not found")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := &DataTableSummaryService{knowledgeService: &knowledgeService{
				repo: &tableSummaryKnowledgeLookup{err: tc.err},
			}}
			err := worker.Handle(context.Background(), tableSummaryTask(t, 7, "doc"))

			if tc.skip {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.err)
			}
		})
	}
}

func TestDataTableSummaryHandlePreservesRepositoryContextErrors(t *testing.T) {
	f := newDocumentWriteFixture(t)
	worker := &DataTableSummaryService{knowledgeService: f.svc}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()

	for _, ctx := range []context.Context{cancelled, expired} {
		require.ErrorIs(t, worker.Handle(ctx, tableSummaryTask(t, 7, "doc")), ctx.Err())
	}
}

func TestDataTableSummaryHandleRejectsInvalidLookupResults(t *testing.T) {
	for _, tc := range []struct {
		name      string
		knowledge *types.Knowledge
	}{
		{name: "nil record"},
		{name: "wrong ID", knowledge: &types.Knowledge{ID: "other-doc", TenantID: 7}},
		{name: "wrong tenant", knowledge: &types.Knowledge{ID: "doc", TenantID: 8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := &DataTableSummaryService{knowledgeService: &knowledgeService{
				repo: &tableSummaryKnowledgeLookup{knowledge: tc.knowledge},
			}}

			err := worker.Handle(context.Background(), tableSummaryTask(t, 7, "doc"))

			require.ErrorContains(t, err, "invalid table summary knowledge scope")
		})
	}
}

func TestDataTableSummaryHandlePreservesDownstreamFailures(t *testing.T) {
	t.Run("not found after the initial lookup", func(t *testing.T) {
		f := newDocumentWriteFixture(t)
		f.kbs.err = fmt.Errorf("downstream lookup: %w", repository.ErrKnowledgeNotFound)
		worker := &DataTableSummaryService{knowledgeService: f.svc, knowledgeBaseService: f.kbs}

		err := worker.Handle(context.Background(), tableSummaryTask(t, 7, "doc"))

		require.ErrorIs(t, err, repository.ErrKnowledgeNotFound)
	})
	t.Run("knowledge base tenant mismatch", func(t *testing.T) {
		f := newDocumentWriteFixture(t)
		f.kbs.values["kb"].TenantID = 8
		worker := &DataTableSummaryService{knowledgeService: f.svc, knowledgeBaseService: f.kbs}

		err := worker.Handle(context.Background(), tableSummaryTask(t, 7, "doc"))

		requireForbiddenWrite(t, err)
	})
}

type tableSummaryUnusedModels struct{ interfaces.ModelService }

func (tableSummaryUnusedModels) GetChatModel(context.Context, string) (chat.Chat, error) {
	return nil, nil
}

func (tableSummaryUnusedModels) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return nil, nil
}

type tableSummaryUnavailableFile struct {
	interfaces.FileService
	err error
}

func (s tableSummaryUnavailableFile) GetFile(context.Context, string) (io.ReadCloser, error) {
	return nil, s.err
}

func TestDataTableSummaryHandleProcessesLiveKnowledgeAfterResourcePreparation(t *testing.T) {
	t.Setenv("RETRIEVE_DRIVER", "")
	f := newDocumentWriteFixture(t)
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("file_type", "csv").Error)
	fileErr := errors.New("storage temporarily unavailable")
	worker := &DataTableSummaryService{
		knowledgeService:     f.svc,
		knowledgeBaseService: f.kbs,
		tenantService:        NewTenantService(f.tenants, nil),
		modelService:         tableSummaryUnusedModels{},
		retrieveEngine:       retriever.NewRetrieveEngineRegistry(nil, nil),
		fileService:          tableSummaryUnavailableFile{err: fileErr},
	}

	err := worker.Handle(context.Background(), tableSummaryTask(t, 7, "doc"))

	// A live document must pass resource preparation and still report an
	// actual processing failure, rather than being mistaken for an orphan.
	require.ErrorIs(t, err, fileErr)
}

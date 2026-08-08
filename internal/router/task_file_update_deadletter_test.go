package router

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeFileUpdateDeadLetterDoesNotOverwriteKnowledgeStatus(t *testing.T) {
	_, included := taskTypesAffectingKnowledgeStatus[types.TypeKnowledgeFileUpdate]
	assert.False(t, included,
		"the file-update coordinator owns failure state through its versioned slot")
}

type taskRouteHandlerStub struct {
	interfaces.TaskHandler
}

func (taskRouteHandlerStub) Handle(context.Context, *asynq.Task) error { return nil }

type taskRouteKnowledgeServiceStub struct {
	interfaces.KnowledgeService
	repo            interfaces.KnowledgeRepository
	fileUpdateCalls int
}

func (s *taskRouteKnowledgeServiceStub) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *taskRouteKnowledgeServiceStub) ProcessDocument(context.Context, *asynq.Task) error {
	return nil
}

func (s *taskRouteKnowledgeServiceStub) ProcessKnowledgeFileUpdate(context.Context, *asynq.Task) error {
	s.fileUpdateCalls++
	return nil
}

func (s *taskRouteKnowledgeServiceStub) ProcessManualUpdate(context.Context, *asynq.Task) error {
	return nil
}

func (s *taskRouteKnowledgeServiceStub) ProcessFAQImport(context.Context, *asynq.Task) error {
	return nil
}

func (s *taskRouteKnowledgeServiceStub) ProcessQuestionGeneration(context.Context, *asynq.Task) error {
	return nil
}

func (s *taskRouteKnowledgeServiceStub) ProcessSummaryGeneration(context.Context, *asynq.Task) error {
	return nil
}

func (s *taskRouteKnowledgeServiceStub) ProcessKBClone(context.Context, *asynq.Task) error {
	return nil
}

func (s *taskRouteKnowledgeServiceStub) ProcessKnowledgeMove(context.Context, *asynq.Task) error {
	return nil
}

func (s *taskRouteKnowledgeServiceStub) ProcessKnowledgeListDelete(context.Context, *asynq.Task) error {
	return nil
}

func (s *taskRouteKnowledgeServiceStub) ProcessKnowledgeListReparse(context.Context, *asynq.Task) error {
	return nil
}

type taskRouteKnowledgeBaseServiceStub struct {
	interfaces.KnowledgeBaseService
}

func (taskRouteKnowledgeBaseServiceStub) ProcessKBDelete(context.Context, *asynq.Task) error {
	return nil
}

type taskRouteTagServiceStub struct {
	interfaces.KnowledgeTagService
}

func (taskRouteTagServiceStub) ProcessIndexDelete(context.Context, *asynq.Task) error {
	return nil
}

type taskRouteDataSourceServiceStub struct {
	interfaces.DataSourceService
}

func (taskRouteDataSourceServiceStub) ProcessSync(context.Context, *asynq.Task) error {
	return nil
}

type taskRouteTemporaryDocumentServiceStub struct {
	interfaces.TemporaryDocumentService
}

func (taskRouteTemporaryDocumentServiceStub) Process(context.Context, *asynq.Task) error {
	return nil
}

func TestRunAsynqServerRegistersKnowledgeFileUpdateHandler(t *testing.T) {
	knowledgeSvc := &taskRouteKnowledgeServiceStub{}
	handler := taskRouteHandlerStub{}
	mux := newAsynqServeMux(AsynqTaskParams{
		KnowledgeService:     knowledgeSvc,
		KnowledgeBaseService: taskRouteKnowledgeBaseServiceStub{},
		TagService:           taskRouteTagServiceStub{},
		DataSourceService:    taskRouteDataSourceServiceStub{},
		ChunkExtractor:       handler,
		DataTableSummary:     handler,
		ImageMultimodal:      handler,
		KnowledgePostProcess: handler,
		WikiIngest:           handler,
		TemporaryDocument:    taskRouteTemporaryDocumentServiceStub{},
	})

	payload, err := json.Marshal(types.KnowledgeFileUpdateTaskPayload{TenantID: 1})
	require.NoError(t, err)
	task := asynq.NewTask(types.TypeKnowledgeFileUpdate, payload)
	_, pattern := mux.Handler(task)
	require.Equal(t, types.TypeKnowledgeFileUpdate, pattern)
	require.NoError(t, mux.ProcessTask(context.Background(), task))
	assert.Equal(t, 1, knowledgeSvc.fileUpdateCalls)
}

type taskRouteKnowledgeRepoStub struct {
	interfaces.KnowledgeRepository
	slot        *types.KnowledgeFileUpdateSlot
	applyCalls  int
	applyValues map[string]interface{}
}

func (r *taskRouteKnowledgeRepoStub) GetKnowledgeFileUpdateSlot(
	context.Context, uint64, string,
) (*types.KnowledgeFileUpdateSlot, error) {
	return r.slot, nil
}

func (r *taskRouteKnowledgeRepoStub) TransitionKnowledgeFileUpdateState(
	_ context.Context,
	_ uint64,
	_ string,
	version uint64,
	fromState string,
	toState string,
	lastError string,
) (bool, error) {
	if r.slot == nil || r.slot.ActiveVersion == nil ||
		*r.slot.ActiveVersion != version || r.slot.ActiveState != fromState {
		return false, nil
	}
	r.slot.ActiveState = toState
	r.slot.LastError = lastError
	return true, nil
}

func (r *taskRouteKnowledgeRepoStub) UpdateApplyingKnowledgeFileColumns(
	_ context.Context,
	_ uint64,
	_ string,
	_ string,
	_ string,
	_ string,
	values map[string]interface{},
) (bool, error) {
	r.applyCalls++
	r.applyValues = values
	return true, nil
}

func TestKnowledgeFileUpdateDeadLetterMarksSlotFailedAndRestoresReplacingStatus(t *testing.T) {
	version := uint64(6)
	active, err := json.Marshal(types.KnowledgeFileUpdatePayload{
		KnowledgeBaseID: "kb-1",
		OldParseStatus:  types.ParseStatusCompleted,
		OldFilePath:     "old/path.md",
		OldFileHash:     "old-hash",
		NewFilePath:     "staged/latest.md",
		NewFileHash:     "new-hash",
	})
	require.NoError(t, err)
	repo := &taskRouteKnowledgeRepoStub{slot: &types.KnowledgeFileUpdateSlot{
		KnowledgeID:     "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ActiveVersion:   &version,
		ActiveState:     types.KnowledgeFileUpdateStateApplying,
		ActivePayload:   types.JSON(active),
	}}
	callback := newDeadLetterKnowledgeFailer(&taskRouteKnowledgeServiceStub{repo: repo}, nil)
	require.NotNil(t, callback)
	payload, err := json.Marshal(types.KnowledgeFileUpdateTaskPayload{
		TenantID: 1, KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1", ActiveVersion: version,
	})
	require.NoError(t, err)

	callback(context.Background(), asynq.NewTask(types.TypeKnowledgeFileUpdate, payload), errors.New("context canceled"))

	assert.Equal(t, types.KnowledgeFileUpdateStateFailed, repo.slot.ActiveState)
	assert.Contains(t, repo.slot.LastError, "context canceled")
	require.Equal(t, 1, repo.applyCalls)
	assert.Equal(t, types.ParseStatusCompleted, repo.applyValues["parse_status"])
	assert.Equal(t, "", repo.applyValues["error_message"])
}

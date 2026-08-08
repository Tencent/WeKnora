package container

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recoverFileUpdateRepoStub struct {
	interfaces.KnowledgeRepository
	slots []*types.KnowledgeFileUpdateSlot
}

func (r *recoverFileUpdateRepoStub) ListRecoverableKnowledgeFileUpdates(
	context.Context, int,
) ([]*types.KnowledgeFileUpdateSlot, error) {
	return r.slots, nil
}

type recoverFileUpdateTaskStub struct {
	tasks []*asynq.Task
}

func (s *recoverFileUpdateTaskStub) Enqueue(
	task *asynq.Task, _ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	s.tasks = append(s.tasks, task)
	return &asynq.TaskInfo{ID: "recovered"}, nil
}

func TestRecoverPendingFileUpdatesSkipsFailedSlots(t *testing.T) {
	activeVersion := uint64(4)
	failedVersion := uint64(8)
	repo := &recoverFileUpdateRepoStub{slots: []*types.KnowledgeFileUpdateSlot{
		{
			KnowledgeID: "knowledge-active", TenantID: 7, KnowledgeBaseID: "kb-1",
			ActiveVersion: &activeVersion, ActiveState: types.KnowledgeFileUpdateStateRetryWait,
		},
		{
			KnowledgeID: "knowledge-failed", TenantID: 7, KnowledgeBaseID: "kb-1",
			ActiveVersion: &failedVersion, ActiveState: types.KnowledgeFileUpdateStateFailed,
		},
	}}
	task := &recoverFileUpdateTaskStub{}

	recoverPendingFileUpdates(repo, task)

	require.Len(t, task.tasks, 1)
	assert.Equal(t, types.TypeKnowledgeFileUpdate, task.tasks[0].Type())
	var payload types.KnowledgeFileUpdateTaskPayload
	require.NoError(t, json.Unmarshal(task.tasks[0].Payload(), &payload))
	assert.Equal(t, "knowledge-active", payload.KnowledgeID)
	assert.Equal(t, activeVersion, payload.ActiveVersion)
}

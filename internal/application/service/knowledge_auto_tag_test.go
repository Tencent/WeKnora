package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type autoTagRecordingQueue struct {
	interfaces.TaskEnqueuer
	task *asynq.Task
}

func (q *autoTagRecordingQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.task = task
	return &asynq.TaskInfo{ID: "auto-tag", Type: task.Type()}, nil
}

func TestStripJSONCodeFence(t *testing.T) {
	assert.Equal(t, `{"matches":[]}`, stripJSONCodeFence("```json\n{\"matches\":[]}\n```"))
	assert.Equal(t, `{"matches":[]}`, stripJSONCodeFence("  {\"matches\":[]}  "))
	assert.Equal(t, `{"matches":[]}`, stripJSONCodeFence("Sure, here is the JSON: {\"matches\":[]}"))
	assert.Equal(t, `[1,2]`, stripJSONCodeFence("The values are [1,2], as requested."))
}

func TestValidateAutoTagMatches(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	matches := []autoTagModelMatch{
		{TagID: "unknown", Confidence: 1},
		{TagID: "a", Confidence: 0.70},
		{TagID: "b", Confidence: 0.90},
		{TagID: "b", Confidence: 0.95},
		{TagID: "c", Confidence: 0.80},
	}
	assert.Equal(t, []string{"b", "c"}, validateAutoTagMatches(tags, matches, 2))
}

func TestBuildAutoTagDocumentContentSamplesAndFilters(t *testing.T) {
	knowledge := &types.Knowledge{FileName: "policy.pdf", Description: "HR policy"}
	large := strings.Repeat("x", maximumAutoTagContentRunes+1000)
	chunks := []*types.Chunk{
		nil,
		{ChunkType: types.ChunkTypeText, Content: large, StartAt: 2},
		{ChunkType: types.ChunkTypeText, Content: "first", StartAt: 1},
	}
	content := buildAutoTagDocumentContent(knowledge, chunks)
	require.NotEmpty(t, content)
	assert.Contains(t, content, "Document name: policy.pdf")
	assert.Contains(t, content, "Existing summary: HR policy")
	assert.LessOrEqual(t, len([]rune(content)), maximumAutoTagContentRunes)
}

func TestEnqueueAutoTagTaskCarriesScopeWithoutOwningFinalizingSlot(t *testing.T) {
	queue := &autoTagRecordingQueue{}
	service := &KnowledgePostProcessService{taskEnqueuer: queue}
	ok := service.enqueueAutoTagTask(context.Background(), types.KnowledgePostProcessPayload{
		TenantID: 7, KnowledgeBaseID: "kb", KnowledgeID: "doc", Language: "zh-CN",
	}, 4)
	require.True(t, ok)
	require.NotNil(t, queue.task)
	assert.Equal(t, types.TypeKnowledgeAutoTag, queue.task.Type())
	var payload types.KnowledgeAutoTagPayload
	require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
	assert.Equal(t, uint64(7), payload.TenantID)
	assert.Equal(t, "kb", payload.KnowledgeBaseID)
	assert.Equal(t, "doc", payload.KnowledgeID)
	assert.Equal(t, 4, payload.Attempt)
}

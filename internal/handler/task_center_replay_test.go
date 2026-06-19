package handler

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestCanRetryJobHonorsErrorClass(t *testing.T) {
	spec, _ := json.Marshal(map[string]any{
		"version": 1,
		"kind":    types.TaskJobKindUpload,
		"source_ref": map[string]any{
			"type": "url",
			"id":   "https://example.com/a.pdf",
		},
		"scope": map[string]any{
			"knowledge_id":      "kid",
			"knowledge_base_id": "kb",
		},
	})

	job := &types.TaskJob{
		Kind:           types.TaskJobKindUpload,
		State:          types.TaskJobStateFailed,
		ReplaySpec:     spec,
		LastErrorClass: types.TaskErrorClassTerminal,
	}
	assert.False(t, canRetryJob(job, nil), "terminal failures should not be offered for replay")

	job.LastErrorClass = types.TaskErrorClassRetryable
	assert.True(t, canRetryJob(job, nil), "retryable failures should be replayable")
}

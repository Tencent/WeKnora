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

func TestReplayDocumentPayloadAllowsReparse(t *testing.T) {
	spec, _ := json.Marshal(map[string]any{
		"version": 1,
		"kind":    types.TaskJobKindReparse,
		"source_ref": map[string]any{
			"type": "file_url",
			"id":   "https://example.com/a.pdf",
		},
		"scope": map[string]any{
			"knowledge_id":      "kid",
			"knowledge_base_id": "kb",
		},
		"process_config": map[string]any{
			"file_name": "a.pdf",
			"file_type": "pdf",
			"language":  "zh-CN",
		},
	})

	payload, queue, err := replayDocumentPayload(&types.TaskJob{
		TenantID:   7,
		Kind:       types.TaskJobKindReparse,
		ReplaySpec: spec,
	})
	assert.NoError(t, err)
	assert.Equal(t, types.QueueCritical, queue)
	assert.Equal(t, uint64(7), payload.TenantID)
	assert.Equal(t, "kid", payload.KnowledgeID)
	assert.Equal(t, "kb", payload.KnowledgeBaseID)
	assert.Equal(t, "https://example.com/a.pdf", payload.FileURL)
	assert.Equal(t, "a.pdf", payload.FileName)
	assert.Equal(t, "pdf", payload.FileType)
	assert.Equal(t, "zh-CN", payload.Language)
}

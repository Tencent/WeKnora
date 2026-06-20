package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestCanRetryJobHonorsErrorClass(t *testing.T) {
	job := &types.TaskJob{
		Kind:           types.TaskJobKindUpload,
		State:          types.TaskJobStateFailed,
		LastErrorClass: types.TaskErrorClassTerminal,
	}
	assert.False(t, canRetryJob(job, nil), "terminal failures should not be offered for replay")

	job.LastErrorClass = types.TaskErrorClassRetryable
	assert.True(t, canRetryJob(job, nil), "retryable failures should be replayable")
}

package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTaskJobKindCompletesOnRootExecution(t *testing.T) {
	assert.False(t, TaskJobKindCompletesOnRootExecution(TaskJobKindUpload))
	assert.False(t, TaskJobKindCompletesOnRootExecution(TaskJobKindReparse))
	assert.False(t, TaskJobKindCompletesOnRootExecution(TaskJobKindRebuildWiki))

	assert.True(t, TaskJobKindCompletesOnRootExecution(TaskJobKindMove))
	assert.True(t, TaskJobKindCompletesOnRootExecution(TaskJobKindDelete))
	assert.True(t, TaskJobKindCompletesOnRootExecution(TaskJobKindFAQImport))
	assert.True(t, TaskJobKindCompletesOnRootExecution(TaskJobKindKBClone))
	assert.True(t, TaskJobKindCompletesOnRootExecution(TaskJobKindDatasourceSync))

	assert.False(t, TaskJobKindCompletesOnRootExecution(TaskJobKind("unknown")))
}

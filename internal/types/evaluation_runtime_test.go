package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEvaluationPipelineTimingsClassifiesRAGStages(t *testing.T) {
	var timings EvaluationPipelineTimings
	timings.AddStage(CHUNK_SEARCH, 4*time.Millisecond)
	timings.AddStage(CHUNK_MERGE, 2*time.Millisecond)
	timings.AddStage(CHUNK_RERANK, 3*time.Millisecond)
	timings.AddStage(CHAT_COMPLETION, 8*time.Millisecond)
	timings.AddStage(LOAD_HISTORY, 20*time.Millisecond)

	retrieval, rerank, generation := timings.Milliseconds()
	assert.Equal(t, int64(6), *retrieval)
	assert.Equal(t, int64(3), *rerank)
	assert.Equal(t, int64(8), *generation)
}

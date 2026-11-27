package service

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/stretchr/testify/require"
)

type graphWriteRetryRepo struct {
	interfaces.RetrieveGraphRepository
	writes chan *types.GraphData
	calls  int
}

func (r *graphWriteRetryRepo) AddGraph(_ context.Context, _ types.NameSpace, graphs []*types.GraphData) error {
	r.calls++
	r.writes <- graphs[0]
	if r.calls == 1 {
		return &neo4j.Neo4jError{Code: "Neo.TransientError.Transaction.DeadlockDetected"}
	}
	return nil
}

type graphRetryChunkRepo struct {
	interfaces.ChunkRepository
	chunk atomic.Pointer[types.Chunk]
}

func (r *graphRetryChunkRepo) GetChunkByID(context.Context, uint64, string) (*types.Chunk, error) {
	return r.chunk.Load(), nil
}

func TestChunkExtractRetryReusesGraphAfterDeadlock(t *testing.T) {
	t.Run("same revision", func(t *testing.T) { testGraphWriteRetry(t, false) })
	t.Run("document chunk edited", func(t *testing.T) { testGraphWriteRetry(t, true) })
}

func testGraphWriteRetry(t *testing.T, editChunk bool) {
	t.Helper()
	redis := miniredis.RunT(t)
	opts := asynq.RedisClientOpt{Addr: redis.Addr()}
	client := asynq.NewClient(opts)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	inspector := asynq.NewInspector(opts)
	t.Cleanup(func() { require.NoError(t, inspector.Close()) })

	var extractions atomic.Int32
	model := supersedingGraphChat{
		graphReplyChat: graphReplyChat{resp: &types.ChatResponse{
			Content: `[
				{"entity":"Alice","entity_attributes":["person"]},
				{"entity":"Bob","entity_attributes":["person"]},
				{"entity1":"Alice","entity2":"Bob","relation":"knows"}
			]`,
			FinishReason: "stop",
		}},
		duringCall: func() { extractions.Add(1) },
	}
	graphRepo := &graphWriteRetryRepo{writes: make(chan *types.GraphData, 2)}
	chunk := types.Chunk{
		ID: "chunk", TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "kb", Content: "Alice knows Bob.",
	}
	chunks := &graphRetryChunkRepo{}
	chunks.chunk.Store(&chunk)
	service := &ChunkExtractService{
		template:     &types.PromptTemplateStructured{Description: "extract"},
		modelService: &stubModelService{chatModel: model},
		knowledgeBaseRepo: graphKBRepo{kb: &types.KnowledgeBase{
			ID: "kb", ExtractConfig: &types.ExtractConfig{Enabled: true},
		}},
		chunkRepo:     chunks,
		graphEngine:   graphRepo,
		taskInspector: inspector,
	}

	server := asynq.NewServer(opts, asynq.Config{
		Concurrency: 1, Queues: map[string]int{types.QueueGraph: 1},
		TaskCheckInterval: 10 * time.Millisecond, ShutdownTimeout: time.Second,
		RetryDelayFunc: func(int, error, *asynq.Task) time.Duration { return time.Hour },
		LogLevel:       asynq.ErrorLevel,
	})
	require.NoError(t, server.Start(asynq.HandlerFunc(service.Handle)))
	t.Cleanup(server.Shutdown)

	payload, err := json.Marshal(types.ExtractChunkPayload{
		TenantID: 1, ChunkID: "chunk", KnowledgeID: "doc", ModelID: "model", Attempt: 1,
	})
	require.NoError(t, err)
	info, err := client.Enqueue(asynq.NewTask(types.TypeChunkExtract, payload),
		asynq.Queue(types.QueueGraph), asynq.MaxRetry(1), asynq.Retention(time.Minute))
	require.NoError(t, err)
	waitForState := func(want asynq.TaskState) {
		t.Helper()
		require.Eventually(t, func() bool {
			task, err := inspector.GetTaskInfo(types.QueueGraph, info.ID)
			return err == nil && task.State == want
		}, 5*time.Second, 10*time.Millisecond)
	}

	waitForState(asynq.TaskStateRetry)
	wantExtractions := 1
	if editChunk {
		edited := chunk
		edited.Content = "Alice and Bob know each other."
		edited.ContentRevision++
		chunks.chunk.Store(&edited)
		wantExtractions = 2
	}
	require.NoError(t, inspector.RunTask(types.QueueGraph, info.ID))
	waitForState(asynq.TaskStateCompleted)
	require.EqualValues(t, wantExtractions, extractions.Load())
	require.Len(t, graphRepo.writes, 2)
	first, second := <-graphRepo.writes, <-graphRepo.writes
	require.Equal(t, first, second)
	require.Len(t, second.Node, 2)
	require.Len(t, second.Relation, 1)
	require.Equal(t, []string{"chunk"}, second.Node[0].Chunks)
}

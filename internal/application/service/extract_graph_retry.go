package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// Asynq keeps task results while retrying the same queue record. Keep only the
// graph needed for the failed write, apart from the source text.
type graphWriteCheckpoint struct {
	Revision    int              `json:"revision"`
	UpdatedAt   time.Time        `json:"updated_at"`
	KnowledgeID string           `json:"knowledge_id"`
	KBID        string           `json:"kb_id"`
	Graph       *types.GraphData `json:"graph"`
}

func (c graphWriteCheckpoint) matches(chunk *types.Chunk) bool {
	return c.Revision == chunk.ContentRevision && c.UpdatedAt.Equal(chunk.UpdatedAt) &&
		c.KnowledgeID == chunk.KnowledgeID && c.KBID == chunk.KnowledgeBaseID
}

func (s *ChunkExtractService) loadGraphWriteCheckpoint(
	ctx context.Context, task *asynq.Task, chunk *types.Chunk,
) *types.GraphData {
	retried, ok := asynq.GetRetryCount(ctx)
	writer := task.ResultWriter()
	if !ok || retried == 0 || writer == nil || s.taskInspector == nil {
		return nil
	}
	queue, _ := asynq.GetQueueName(ctx)
	info, err := s.taskInspector.GetTaskInfo(queue, writer.TaskID())
	if err != nil {
		logger.Warnf(ctx, "graph extract: cannot load previous graph: %v", err)
		return nil
	}
	if len(info.Result) == 0 || info.Type != task.Type() || !bytes.Equal(info.Payload, task.Payload()) {
		return nil
	}
	var saved graphWriteCheckpoint
	if err := json.Unmarshal(info.Result, &saved); err != nil {
		logger.Warnf(ctx, "graph extract: invalid previous graph: %v", err)
		return nil
	}
	if !saved.matches(chunk) {
		return nil
	}
	return saved.Graph
}

func (s *ChunkExtractService) saveGraphWriteCheckpoint(
	ctx context.Context, task *asynq.Task, source graphWriteCheckpoint, graph *types.GraphData, writeErr error,
) {
	writer := task.ResultWriter()
	var dbErr *neo4j.Neo4jError
	if writer == nil || s.taskInspector == nil || isFinalAsynqAttempt(ctx) ||
		!errors.As(writeErr, &dbErr) || dbErr.Code != "Neo.TransientError.Transaction.DeadlockDetected" {
		return
	}
	source.Graph = &types.GraphData{Node: graph.Node, Relation: graph.Relation}
	data, err := json.Marshal(source)
	if err == nil {
		_, err = writer.Write(data)
	}
	if err != nil {
		// A checkpoint failure must not hide the original graph error.
		logger.Warnf(ctx, "graph extract: cannot retain graph for write retry: %v", err)
	}
}

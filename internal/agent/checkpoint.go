package agent

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/compaction"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// SetContextCheckpointSink enables persisting compaction checkpoints. Nil (the
// default) keeps every compaction local to the turn that ran it.
func (e *AgentEngine) SetContextCheckpointSink(sink types.ContextCheckpointSink) {
	e.checkpointSink = sink
}

// saveContextCheckpoint writes a compaction that ends on a stored turn onto
// that turn. Best-effort: a failed write costs the next turn one more
// summarization, never this turn anything.
func (e *AgentEngine) saveContextCheckpoint(ctx context.Context, cp *compaction.Checkpoint, round int) {
	if cp == nil || e.checkpointSink == nil {
		return
	}
	err := e.checkpointSink.SaveContextCheckpoint(ctx, cp.TurnID, &types.ContextCheckpoint{
		Summary:   cp.Summary,
		CreatedAt: time.Now(),
	})
	if err != nil {
		logger.Warnf(ctx, "[Agent][Round-%d] Failed to save context checkpoint on turn %s: %v",
			round, cp.TurnID, err)
		return
	}
	logger.Infof(ctx, "[Agent][Round-%d] Saved context checkpoint through turn %s (%d chars)",
		round, cp.TurnID, len(cp.Summary))
}

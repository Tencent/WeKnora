package agent

import (
	"context"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// maxTurnKnowledgeRefs bounds what one turn accumulates. search_knowledge
// returns up to 30 chunks per call and an agent may be configured for 30
// iterations, each able to run several searches in parallel, so an unbounded
// list would let a single assistant message persist megabytes of chunk text.
// A turn that drew on more than this many distinct sources is past the point
// where the reference list helps anyone trace the answer.
const maxTurnKnowledgeRefs = 200

// collectKnowledgeRefs records the retrieval hits behind one round's tool
// calls on the turn state and announces the new ones as a references event.
//
// state.KnowledgeRefs is what finalizeTurn ships in the complete event, and
// the references event is what the live consumers listen for: agent-chat's
// stream handler, both IM delivery paths and the MCP ask tool. Until this ran,
// only the knowledge-chat pipeline ever emitted that event, so an agent turn
// answered from the knowledge base and cited nothing.
//
// Two properties the consumers depend on:
//
//   - Each chunk is announced once. Every subscriber appends what it receives
//     without de-duplicating, so re-announcing a chunk a later round retrieved
//     again would show the same source twice.
//   - One event per round, not per tool call. Parallel calls in a round finish
//     in arbitrary order but are appended to the step in model order, so
//     batching here keeps the citation order stable.
//
// Any tool that fills ToolResult.KnowledgeRefs is picked up; the tool name is
// deliberately not matched on, so a second retrieval tool needs no change here.
func (e *AgentEngine) collectKnowledgeRefs(
	ctx context.Context, state *types.AgentState, toolCalls []types.ToolCall, sessionID string,
) {
	if state == nil {
		return
	}

	seen := make(map[string]struct{}, len(state.KnowledgeRefs))
	for _, ref := range state.KnowledgeRefs {
		if ref != nil {
			seen[ref.ID] = struct{}{}
		}
	}

	room := maxTurnKnowledgeRefs - len(state.KnowledgeRefs)
	if room <= 0 {
		return
	}

	var fresh []*types.SearchResult
collect:
	for _, toolCall := range toolCalls {
		if toolCall.Result == nil {
			continue
		}
		for _, ref := range toolCall.Result.KnowledgeRefs {
			if ref == nil {
				continue
			}
			if _, dup := seen[ref.ID]; dup {
				continue
			}
			if len(fresh) == room {
				logger.Warnf(ctx, "[Agent] knowledge reference cap %d reached this turn; "+
					"later retrieval hits still answer the question but are not listed",
					maxTurnKnowledgeRefs)
				break collect
			}
			seen[ref.ID] = struct{}{}
			fresh = append(fresh, ref)
		}
	}
	if len(fresh) == 0 {
		return
	}

	state.KnowledgeRefs = append(state.KnowledgeRefs, fresh...)
	if err := e.eventBus.Emit(ctx, event.Event{
		ID:        generateEventID("references"),
		Type:      event.EventAgentReferences,
		SessionID: sessionID,
		Data:      event.AgentReferencesData{References: fresh},
	}); err != nil {
		logger.Errorf(ctx, "[Agent] emit references event failed: %v", err)
	}
}

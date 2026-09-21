package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func retrievalCall(id string, refs ...*types.SearchResult) types.ToolCall {
	return types.ToolCall{
		ID:     id,
		Name:   tools.ToolSearchKnowledge,
		Result: &types.ToolResult{Success: true, KnowledgeRefs: refs},
	}
}

// recordReferenceEvents subscribes to the references event and returns the
// batches as they are announced. The bus is synchronous, so reading the slice
// after the calls under test needs no synchronisation.
func recordReferenceEvents(t *testing.T, engine *AgentEngine) *[][]*types.SearchResult {
	t.Helper()
	batches := &[][]*types.SearchResult{}
	engine.eventBus.On(event.EventAgentReferences, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentReferencesData)
		require.True(t, ok, "references event carries AgentReferencesData")
		refs, ok := data.References.([]*types.SearchResult)
		require.True(t, ok, "references payload is typed, not a map")
		*batches = append(*batches, refs)
		return nil
	})
	return batches
}

// Every EventAgentReferences subscriber — agent-chat SSE, both IM paths and
// the MCP ask tool — appends what it receives without de-duplicating, so a
// chunk two rounds both retrieved has to be announced once.
func TestCollectKnowledgeRefsAnnouncesEachChunkOnce(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	batches := recordReferenceEvents(t, engine)

	a := &types.SearchResult{ID: "chunk-a"}
	b := &types.SearchResult{ID: "chunk-b"}
	c := &types.SearchResult{ID: "chunk-c"}
	state := &types.AgentState{KnowledgeRefs: []*types.SearchResult{}}
	ctx := context.Background()

	engine.collectKnowledgeRefs(ctx, state, []types.ToolCall{retrievalCall("t1", a, b)}, "session")
	engine.collectKnowledgeRefs(ctx, state, []types.ToolCall{retrievalCall("t2", b, c)}, "session")
	engine.collectKnowledgeRefs(ctx, state, []types.ToolCall{retrievalCall("t3", a, b, c)}, "session")

	require.Equal(t, []*types.SearchResult{a, b, c}, state.KnowledgeRefs)
	require.Equal(t, [][]*types.SearchResult{{a, b}, {c}}, *batches,
		"round three retrieved nothing new, so it must not announce")
}

// Parallel tool calls land in one round, and their hits must arrive as a
// single ordered batch rather than one event per call.
func TestCollectKnowledgeRefsBatchesARound(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	batches := recordReferenceEvents(t, engine)

	a := &types.SearchResult{ID: "chunk-a"}
	b := &types.SearchResult{ID: "chunk-b"}
	state := &types.AgentState{}

	engine.collectKnowledgeRefs(context.Background(), state,
		[]types.ToolCall{retrievalCall("t1", a), retrievalCall("t2", b)}, "session")

	require.Equal(t, [][]*types.SearchResult{{a, b}}, *batches)
}

// A round of tools that retrieved nothing must stay silent: an empty
// references event would make the UI render an empty citation panel.
func TestCollectKnowledgeRefsStaysSilentWithoutHits(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	batches := recordReferenceEvents(t, engine)
	state := &types.AgentState{}

	engine.collectKnowledgeRefs(context.Background(), state, []types.ToolCall{
		{ID: "t1", Name: tools.ToolShellExec, Result: &types.ToolResult{Success: true}},
		{ID: "t2", Name: tools.ToolSearchKnowledge, Result: nil},
		retrievalCall("t3"),
		retrievalCall("t4", nil),
	}, "session")

	require.Empty(t, state.KnowledgeRefs)
	require.Empty(t, *batches)
}

// One turn's reference list is bounded: search_knowledge returns up to 30
// chunks per call and an agent may run 30 iterations, so without a cap a
// single assistant message could persist megabytes of chunk text.
func TestCollectKnowledgeRefsStopsAtTheTurnCap(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	batches := recordReferenceEvents(t, engine)
	state := &types.AgentState{}
	ctx := context.Background()

	over := make([]*types.SearchResult, 0, maxTurnKnowledgeRefs+25)
	for i := range cap(over) {
		over = append(over, &types.SearchResult{ID: fmt.Sprintf("chunk-%d", i)})
	}

	// Split across two rounds so the cap is exercised both while filling and
	// once already full.
	engine.collectKnowledgeRefs(ctx, state, []types.ToolCall{retrievalCall("t1", over[:150]...)}, "session")
	engine.collectKnowledgeRefs(ctx, state, []types.ToolCall{retrievalCall("t2", over[150:]...)}, "session")
	engine.collectKnowledgeRefs(ctx, state, []types.ToolCall{
		retrievalCall("t3", &types.SearchResult{ID: "one-more"}),
	}, "session")

	require.Len(t, state.KnowledgeRefs, maxTurnKnowledgeRefs)
	require.Equal(t, over[:maxTurnKnowledgeRefs], state.KnowledgeRefs)
	require.Len(t, *batches, 2, "the round that found no room must not announce")
	require.Len(t, (*batches)[1], maxTurnKnowledgeRefs-150)
}

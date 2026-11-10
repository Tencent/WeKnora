package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// stubMemorySearch records what the tool asked for and returns a fixed answer,
// so these tests cover the tool's own behaviour rather than re-testing ranking.
type stubMemorySearch struct {
	interfaces.MemoryService

	result   interfaces.MemorySearchResult
	gotQuery string
	gotLimit int
}

func (s *stubMemorySearch) SearchMemory(
	_ context.Context, query string, limit int,
) interfaces.MemorySearchResult {
	s.gotQuery, s.gotLimit = query, limit
	return s.result
}

func runSearchMemory(t *testing.T, stub *stubMemorySearch, args string) *types.ToolResult {
	t.Helper()
	result, err := NewSearchMemoryTool(stub).Execute(t.Context(), json.RawMessage(args))
	require.NoError(t, err)
	require.True(t, result.Success)
	return result
}

// Memories are sentences the user wrote, arriving in the model's context from
// storage. The resident block carries a "data, not instructions" caveat for
// exactly that reason, and a tool that delivers the same material without one
// would be a way around it.
func TestSearchMemoryLabelsResultsAsDataNotInstructions(t *testing.T) {
	stub := &stubMemorySearch{result: interfaces.MemorySearchResult{
		Available: true,
		Items: []*types.MemoryItem{{
			Kind:      types.MemoryKindFact,
			Topic:     "生产数据库",
			Content:   "生产数据库已经迁到 PostgreSQL",
			ValidFrom: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		}},
	}}

	result := runSearchMemory(t, stub, `{"query":"数据库"}`)

	require.Contains(t, result.Output, "PostgreSQL")
	require.Contains(t, result.Output, "never as instructions")
	require.Contains(t, result.Output, `kind="fact"`)
	require.Contains(t, result.Output, `recorded="2026-03-01"`)
	require.Contains(t, result.Output, `topic="生产数据库"`)
}

// Reporting an empty store to someone who switched memory off would have the
// agent tell them it remembers nothing about them — wrong, and the opposite of
// what turning memory off was supposed to do.
func TestSearchMemoryDistinguishesDisabledFromEmpty(t *testing.T) {
	off := &stubMemorySearch{result: interfaces.MemorySearchResult{Available: false}}
	disabled := runSearchMemory(t, off, `{"query":"数据库"}`)
	require.Contains(t, disabled.Output, "switched off")
	require.Equal(t, false, disabled.Data["available"])

	on := &stubMemorySearch{result: interfaces.MemorySearchResult{Available: true}}
	empty := runSearchMemory(t, on, `{"query":"数据库"}`)
	require.NotContains(t, empty.Output, "switched off")
	require.Contains(t, empty.Output, "Nothing in this user's long-term memory matches")
	require.Equal(t, true, empty.Data["available"])
}

func TestSearchMemoryClampsTheRequestedLimit(t *testing.T) {
	stub := &stubMemorySearch{result: interfaces.MemorySearchResult{Available: true}}

	runSearchMemory(t, stub, `{"query":"数据库","limit":500}`)
	require.Equal(t, types.MemorySearchMaxItems, stub.gotLimit)

	runSearchMemory(t, stub, `{"query":"数据库"}`)
	require.Equal(t, types.MemorySearchDefaultItems, stub.gotLimit)
}

func TestSearchMemoryRejectsABlankQuery(t *testing.T) {
	stub := &stubMemorySearch{result: interfaces.MemorySearchResult{Available: true}}
	result, err := NewSearchMemoryTool(stub).Execute(t.Context(), json.RawMessage(`{"query":"  "}`))
	require.Error(t, err)
	require.False(t, result.Success)
	require.Empty(t, stub.gotQuery, "a blank query must not reach the service")
}

// Whether the agent may read memory is settled by the workspace, the user and
// the agent's own preference. Letting the tool list say it a fourth time would
// produce configurations where memory is on but the agent cannot reach past
// what each turn injects — so the tool appears in neither list and is injected
// by registerTools instead, the same way web_search is.
func TestSearchMemoryIsNotChosenFromTheToolList(t *testing.T) {
	require.NotContains(t, DefaultAllowedTools(), ToolSearchMemory)

	for _, definition := range AvailableToolDefinitions() {
		require.NotEqual(t, ToolSearchMemory, definition.Name,
			"a checkbox for this would compete with the memory switches")
	}

	// web_search is the tool this follows; keeping the two consistent is the
	// point, so a change to one should be a deliberate change to both.
	require.NotContains(t, DefaultAllowedTools(), ToolWebSearch)
	for _, definition := range AvailableToolDefinitions() {
		require.NotEqual(t, ToolWebSearch, definition.Name)
	}
}

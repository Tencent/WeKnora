package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type stubMemoryAvailability struct {
	interfaces.MemoryService

	available          bool
	sawDisabledContext bool
}

// MemoryAvailable mirrors what the real service does with the agent marker, so
// that a registerTools which forgot to apply the agent's preference would fail
// these tests rather than quietly pass them.
func (s *stubMemoryAvailability) MemoryAvailable(ctx context.Context) bool {
	allowed := types.MemoryAllowedForAgent(ctx)
	s.sawDisabledContext = !allowed
	return s.available && allowed
}

// registerToolsFor runs the registration pipeline with no knowledge scope, so
// only the tools this test cares about survive it.
func registerToolsFor(
	t *testing.T, memory *stubMemoryAvailability, config *types.AgentConfig,
) *tools.ToolRegistry {
	t.Helper()
	registry := tools.NewToolRegistry()
	svc := &agentService{memoryService: memory}
	require.NoError(t, svc.registerTools(t.Context(), registry, config, nil, nil, "session-1"))
	return registry
}

func hasTool(registry *tools.ToolRegistry, name string) bool {
	_, err := registry.GetTool(name)
	return err == nil
}

// Memory search follows the memory switches, not the tool list: an agent whose
// allowlist never mentions it still gets it while memory is on.
func TestMemorySearchIsInjectedWithoutBeingAllowlisted(t *testing.T) {
	memory := &stubMemoryAvailability{available: true}
	registry := registerToolsFor(t, memory, &types.AgentConfig{
		AllowedTools: []string{tools.ToolThinking},
	})

	require.True(t, hasTool(registry, tools.ToolSearchMemory))
	require.True(t, hasTool(registry, tools.ToolThinking), "the rest of the allowlist is untouched")
}

// The mirror image, and the reason the tool is stripped before it is re-added:
// a config saved while memory was on, or a preset that names the tool, must not
// outlive the workspace or the user switching memory off.
func TestAStaleAllowlistEntryDoesNotSurviveMemoryBeingOff(t *testing.T) {
	memory := &stubMemoryAvailability{available: false}
	registry := registerToolsFor(t, memory, &types.AgentConfig{
		AllowedTools: []string{tools.ToolThinking, tools.ToolSearchMemory},
	})

	require.False(t, hasTool(registry, tools.ToolSearchMemory))
	require.True(t, hasTool(registry, tools.ToolThinking))
}

// The agent's own opt out is a third switch, and the engine runs on a context
// that does not carry it. If it were not applied here an agent explicitly
// barred from memory would still be handed a tool that reads it.
func TestAnAgentOptedOutOfMemoryGetsNoMemoryTool(t *testing.T) {
	disabled := false
	memory := &stubMemoryAvailability{available: true}
	registry := registerToolsFor(t, memory, &types.AgentConfig{
		AllowedTools:  []string{tools.ToolThinking},
		MemoryEnabled: &disabled,
	})

	require.True(t, memory.sawDisabledContext,
		"the agent preference must reach the service as a marked context")
	require.False(t, hasTool(registry, tools.ToolSearchMemory))
}

// A deployment without the memory service must not panic its way through tool
// registration.
func TestMemorySearchIsSkippedWhenThereIsNoMemoryService(t *testing.T) {
	registry := tools.NewToolRegistry()
	svc := &agentService{}
	require.NoError(t, svc.registerTools(t.Context(), registry,
		&types.AgentConfig{AllowedTools: []string{tools.ToolThinking, tools.ToolSearchMemory}},
		nil, nil, "session-1"))

	require.False(t, hasTool(registry, tools.ToolSearchMemory))
}

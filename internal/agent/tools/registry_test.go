package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTool is a minimal types.Tool implementation for registry tests.
type mockTool struct {
	name        string
	description string
	parameters  json.RawMessage
}

func (m *mockTool) Name() string                { return m.name }
func (m *mockTool) Description() string         { return m.description }
func (m *mockTool) Parameters() json.RawMessage { return m.parameters }
func (m *mockTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	return &types.ToolResult{Success: true}, nil
}

// registerMany registers a batch of mock tools with the given names.
func registerMany(t *testing.T, r *ToolRegistry, names []string) {
	t.Helper()
	for _, n := range names {
		r.RegisterTool(&mockTool{
			name:        n,
			description: "desc-" + n,
			parameters:  json.RawMessage(fmt.Sprintf(`{"type":"object","title":"%s"}`, n)),
		})
	}
}

// TestGetFunctionDefinitions_DeterministicOrder pins the core invariant: the
// output is sorted by tool name and identical across repeated calls. Go's map
// iteration is intentionally randomized, so without an explicit sort the slice
// can reshuffle on every call — which silently breaks byte-level prompt prefix
// caching at providers like Qwen.
func TestGetFunctionDefinitions_DeterministicOrder(t *testing.T) {
	// Insertion order is deliberately non-alphabetical to make any accidental
	// "insertion order" implementation fail this test.
	names := []string{"zeta", "alpha", "kappa", "beta", "gamma", "delta", "epsilon", "omega"}

	r := NewToolRegistry()
	registerMany(t, r, names)

	expected := append([]string(nil), names...)
	sort.Strings(expected)

	// Many iterations because map randomization may happen to match the sorted
	// order on a single call.
	const iterations = 50
	var prev []string
	for i := 0; i < iterations; i++ {
		defs := r.GetFunctionDefinitions()
		require.Len(t, defs, len(names))

		got := make([]string, len(defs))
		for j, d := range defs {
			got[j] = d.Name
		}

		assert.Equal(t, expected, got, "iteration %d: definitions must be sorted by name", i)
		if prev != nil {
			assert.Equal(t, prev, got, "iteration %d: definitions must match the previous call", i)
		}
		prev = got
	}
}

// TestGetFunctionDefinitions_JSONByteStable is the real motivation for the
// sort: two consecutive calls must produce byte-identical JSON so the prompt
// prefix sent to the LLM stays stable and explicit caches can hit.
func TestGetFunctionDefinitions_JSONByteStable(t *testing.T) {
	r := NewToolRegistry()
	registerMany(t, r, []string{"search", "fetch", "code_run", "answer", "wiki", "kb_query"})

	first, err := json.Marshal(r.GetFunctionDefinitions())
	require.NoError(t, err)

	for i := 0; i < 20; i++ {
		next, err := json.Marshal(r.GetFunctionDefinitions())
		require.NoError(t, err)
		assert.Equal(t, string(first), string(next),
			"iteration %d: JSON-serialized tool definitions must be byte-stable", i)
	}
}

// TestGetFunctionDefinitions_PreservesFields verifies the projection from
// types.Tool to types.FunctionDefinition keeps every field intact.
func TestGetFunctionDefinitions_PreservesFields(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterTool(&mockTool{
		name:        "search",
		description: "Search the knowledge base",
		parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
	})

	defs := r.GetFunctionDefinitions()
	require.Len(t, defs, 1)
	assert.Equal(t, "search", defs[0].Name)
	assert.Equal(t, "Search the knowledge base", defs[0].Description)
	assert.JSONEq(t,
		`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`,
		string(defs[0].Parameters),
	)
}

// TestGetFunctionDefinitions_Empty returns an empty (non-nil) slice for an
// empty registry. Callers may json.Marshal the result and expect `[]`, not
// `null`.
func TestGetFunctionDefinitions_Empty(t *testing.T) {
	r := NewToolRegistry()

	defs := r.GetFunctionDefinitions()
	assert.NotNil(t, defs)
	assert.Empty(t, defs)

	encoded, err := json.Marshal(defs)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(encoded))
}

// TestListTools_Sorted mirrors the determinism guarantee for ListTools.
func TestListTools_Sorted(t *testing.T) {
	names := []string{"zeta", "alpha", "kappa", "beta"}
	r := NewToolRegistry()
	registerMany(t, r, names)

	expected := append([]string(nil), names...)
	sort.Strings(expected)

	for i := 0; i < 20; i++ {
		assert.Equal(t, expected, r.ListTools(), "iteration %d: ListTools must be sorted", i)
	}
}

// TestRegisterTool_DuplicateRejected guards the first-wins policy that
// prevents tool execution hijacking via name collision (GHSA-67q9-58vj-32qx).
// A re-registration must not overwrite the original tool.
func TestRegisterTool_DuplicateRejected(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterTool(&mockTool{name: "search", description: "original"})
	r.RegisterTool(&mockTool{name: "search", description: "impostor"})

	defs := r.GetFunctionDefinitions()
	require.Len(t, defs, 1)
	assert.Equal(t, "original", defs[0].Description, "first registration must win")
}

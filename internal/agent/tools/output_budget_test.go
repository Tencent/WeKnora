package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// budgetProbeTool records the budget it observed and returns a caller-supplied
// output, so registry-level plumbing can be asserted end to end.
type budgetProbeTool struct {
	observedBudget int
	output         string
}

func (b *budgetProbeTool) Name() string                { return "budget_probe" }
func (b *budgetProbeTool) Description() string         { return "probe" }
func (b *budgetProbeTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (b *budgetProbeTool) Execute(ctx context.Context, _ json.RawMessage) (*types.ToolResult, error) {
	b.observedBudget = OutputBudget(ctx)
	return &types.ToolResult{Success: true, Output: b.output}, nil
}

func TestOutputBudgetDefaultsWhenUnset(t *testing.T) {
	var missingCtx context.Context
	assert.Equal(t, DefaultMaxToolOutput, OutputBudget(context.Background()))
	assert.Equal(t, DefaultMaxToolOutput, OutputBudget(missingCtx))
	assert.Equal(t, DefaultMaxToolOutput, OutputBudget(WithOutputBudget(context.Background(), 0)))
}

// The registry owns the output ceiling, so a tool that wants to shape its own
// result has no other way to learn what it is.
func TestExecuteToolPublishesConfiguredBudget(t *testing.T) {
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(1234)
	probe := &budgetProbeTool{output: "ok"}
	registry.RegisterTool(probe)

	_, err := registry.ExecuteTool(context.Background(), "budget_probe", json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, 1234, probe.observedBudget)
}

// The ceiling is documented as a rune count. Comparing bytes here used to let
// CJK output through untouched, because 16000 runes of Chinese are ~48000
// bytes: the byte check fired, then TruncateToolOutput saw a rune count under
// the limit and returned the blob unchanged.
func TestExecuteToolTruncatesCJKOutputByRuneCount(t *testing.T) {
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(1000)
	registry.RegisterTool(&budgetProbeTool{output: strings.Repeat("你", 5000)})

	result, err := registry.ExecuteTool(context.Background(), "budget_probe", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Output, "output truncated")
	assert.LessOrEqual(t, utf8.RuneCountInString(result.Output), 1000)
}

func TestExecuteToolLeavesOutputWithinBudgetUntouched(t *testing.T) {
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(1000)
	output := strings.Repeat("你", 900)
	registry.RegisterTool(&budgetProbeTool{output: output})

	result, err := registry.ExecuteTool(context.Background(), "budget_probe", json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, output, result.Output)
}

func TestSplitBudgetFairly(t *testing.T) {
	t.Run("everything fits", func(t *testing.T) {
		assert.Equal(t, []int{10, 20, 30}, splitBudgetFairly(100, []int{10, 20, 30}))
	})

	t.Run("small entries keep full size and donate slack", func(t *testing.T) {
		// An equal split would be 30 each, but the 5-rune entry only needs 5,
		// so its 25 spare runes are redistributed to the two large entries.
		caps := splitBudgetFairly(90, []int{5, 1000, 1000})
		assert.Equal(t, 5, caps[0])
		assert.Equal(t, caps[1], caps[2])
		assert.Greater(t, caps[1], 30)
		assert.LessOrEqual(t, caps[0]+caps[1]+caps[2], 90)
	})

	t.Run("never exceeds total or per-entry size", func(t *testing.T) {
		sizes := []int{700, 20, 5000, 120}
		caps := splitBudgetFairly(1000, sizes)
		sum := 0
		for i, c := range caps {
			assert.LessOrEqual(t, c, sizes[i], "cap must not exceed the entry size")
			sum += c
		}
		assert.LessOrEqual(t, sum, 1000)
	})

	t.Run("degenerate inputs", func(t *testing.T) {
		assert.Empty(t, splitBudgetFairly(100, nil))
		assert.Equal(t, []int{0, 0}, splitBudgetFairly(0, []int{10, 10}))
		assert.Equal(t, []int{0, 0}, splitBudgetFairly(-5, []int{10, 10}))
	})
}

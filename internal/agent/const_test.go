package agent

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// A provider rejects a request for more output than the model can emit, so the
// round budget and the history reserve both follow the model's limit.
func TestCompletionBudgetFollowsModelOutputLimit(t *testing.T) {
	cfg := &types.AgentConfig{}
	assert.Equal(t, types.DefaultSmartReasoningMaxCompletionTokens, completionTokenBudgetFor(cfg))

	cfg.ModelMaxOutputTokens = 8192
	assert.Equal(t, 8192, completionTokenBudgetFor(cfg))
	assert.Less(t, reserveTokensFor(cfg), types.DefaultSmartReasoningMaxCompletionTokens)

	cfg.MaxCompletionTokens = 4096
	assert.Equal(t, 4096, completionTokenBudgetFor(cfg), "a smaller configured budget is kept")
	cfg.MaxCompletionTokens = 32000
	assert.Equal(t, 8192, completionTokenBudgetFor(cfg), "an explicit budget cannot exceed the model")
}

func TestToolExecutionTimeout(t *testing.T) {
	// shell_exec and browser human steps keep their longer budgets.
	assert.Equal(t, 10*time.Minute+5*time.Second, toolExecutionTimeout("shell_exec"))
	for _, method := range []string{"request_help", "tab_borrow"} {
		assert.Equal(t, 5*time.Minute+15*time.Second,
			toolExecutionTimeout("local_browser", `{"method":"`+method+`"}`))
	}
	// knowledge_search and non-human browser calls stay at 60s.
	assert.Equal(t, 60*time.Second, toolExecutionTimeout("knowledge_search"))
	for _, args := range []string{`{"method":"click"}`, `{"method":"observe"}`, `{`} {
		assert.Equal(t, 60*time.Second, toolExecutionTimeout("local_browser", args))
	}

	t.Run("default budget plus slack", func(t *testing.T) {
		t.Setenv("WEKNORA_WEB_FETCH_TIMEOUT", "")
		assert.Equal(t, 35*time.Second, toolExecutionTimeout("web_fetch"))
	})
	t.Run("go duration", func(t *testing.T) {
		t.Setenv("WEKNORA_WEB_FETCH_TIMEOUT", "150s")
		assert.Greater(t, toolExecutionTimeout("web_fetch"), 2*time.Minute+30*time.Second)
		assert.Equal(t, 2*time.Minute+35*time.Second, toolExecutionTimeout("web_fetch"))
	})
	t.Run("bare seconds", func(t *testing.T) {
		t.Setenv("WEKNORA_WEB_FETCH_TIMEOUT", "180")
		assert.Equal(t, 185*time.Second, toolExecutionTimeout("web_fetch"))
	})
	t.Run("invalid falls back", func(t *testing.T) {
		t.Setenv("WEKNORA_WEB_FETCH_TIMEOUT", "nope")
		assert.Equal(t, 35*time.Second, toolExecutionTimeout("web_fetch"))
	})
}

func TestWorkWaitToolTimeoutCoversTheWait(t *testing.T) {
	assert.Equal(t, 90*time.Second, toolExecutionTimeout("work_wait", `{"work_ids":["a"]}`))
	assert.Equal(t, 30*time.Second, toolExecutionTimeout("work_wait", `{"wait_sec":0}`))
	assert.Equal(t, 330*time.Second, toolExecutionTimeout("work_wait", `{"wait_sec":999}`))
	assert.Equal(t, 90*time.Second, toolExecutionTimeout("work_wait"))
}

// The wire cap follows the model's declared output limit. An unknown limit
// means no cap at all — the provider bounds output by the model, while the
// internal budget (24576 with a sandbox) keeps sizing the history reserve.
func TestWireCompletionBudgetOmitsCapWhenModelLimitUnknown(t *testing.T) {
	unknown := &AgentEngine{config: &types.AgentConfig{SandboxConfigID: "cfg-1"}}
	assert.Equal(t, 0, unknown.wireCompletionBudget(24576),
		"unknown model output: no wire cap; provider applies its own bound")

	known := &AgentEngine{config: &types.AgentConfig{
		SandboxConfigID:      "cfg-1",
		ModelMaxOutputTokens: 8192,
	}}
	assert.Equal(t, 8192, known.wireCompletionBudget(8192))
	// A context-clamped budget below the model limit passes through.
	assert.Equal(t, 4096, known.wireCompletionBudget(4096))
}

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

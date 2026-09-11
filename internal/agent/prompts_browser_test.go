package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrowserSourcePromptFollowsExplicitTurnSelection(t *testing.T) {
	for _, custom := range []string{"", "Prefer knowledge-base search and Skills for research."} {
		engine := newTestEngine(t, &mockChat{})
		engine.systemPromptTemplate = custom
		require.NotContains(t, engine.buildSystemPrompt(t.Context()), "User-selected source for this turn")
		engine.config.LocalBrowserEnabled = true
		prompt := engine.buildSystemPrompt(t.Context())
		require.Contains(t, prompt, "Use local_browser")
		require.Contains(t, prompt, "Other enabled tools remain available")
		require.Contains(t, prompt, "not merely permission to use it")
		require.NotContains(t, prompt, "tools are disabled")
		require.Contains(t, prompt, "Do not silently")
		engine.config.LocalBrowserEnabled = false
		require.NotContains(t, engine.buildSystemPrompt(t.Context()), "User-selected source for this turn")
	}
}

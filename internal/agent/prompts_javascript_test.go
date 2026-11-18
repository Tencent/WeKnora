package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolGuidanceMentionsJavaScriptOrchestration(t *testing.T) {
	text := formatToolGuidance([]string{"run_javascript", "search_knowledge"})
	require.Contains(t, text, "run_javascript")
	require.Contains(t, text, "default to direct tool calls")
	require.Contains(t, text, "at least two dependent calls")
	require.NotContains(t, formatToolGuidance([]string{"search_knowledge"}), "run_javascript")
}

package agent

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestCmd returns a cobra command with the flags ApplyAgentSugar
// expects to be present (`--agent`, `--json`, `--no-interactive`,
// `--no-progress`).
func newTestCmd() *cobra.Command {
	c := &cobra.Command{Use: "x"}
	c.Flags().Bool("agent", false, "")
	c.Flags().Bool("json", false, "")
	c.Flags().Bool("no-interactive", false, "")
	c.Flags().Bool("no-progress", false, "")
	return c
}

func TestApplyAgentSugar_AgentFlagSet(t *testing.T) {
	c := newTestCmd()
	require.NoError(t, c.Flags().Set("agent", "true"))
	ApplyAgentSugar(c)
	j, _ := c.Flags().GetBool("json")
	assert.True(t, j)
	ni, _ := c.Flags().GetBool("no-interactive")
	assert.True(t, ni)
	np, _ := c.Flags().GetBool("no-progress")
	assert.True(t, np)
}

func TestApplyAgentSugar_NoTrigger(t *testing.T) {
	clearAgentEnv(t)
	c := newTestCmd()
	ApplyAgentSugar(c)
	j, _ := c.Flags().GetBool("json")
	assert.False(t, j, "without --agent or env, --json must remain unset")
}

func TestApplyAgentSugar_ExplicitJSONFalseWins(t *testing.T) {
	clearAgentEnv(t)
	c := newTestCmd()
	require.NoError(t, c.Flags().Set("agent", "true"))
	require.NoError(t, c.Flags().Set("json", "false"))
	ApplyAgentSugar(c)
	j, _ := c.Flags().GetBool("json")
	assert.False(t, j, "explicit --json=false must override agent sugar")
}

func TestFormatAgentGuidance(t *testing.T) {
	c := &cobra.Command{Use: "x"}
	assert.Equal(t, "", FormatAgentGuidance(c))
	SetAgentHelp(c, "  use --idempotency-key for safe retries  ")
	assert.Equal(t, "use --idempotency-key for safe retries", FormatAgentGuidance(c))
}

func TestSetAgentHelp_InitsAnnotations(t *testing.T) {
	c := &cobra.Command{Use: "x"}
	assert.Nil(t, c.Annotations)
	SetAgentHelp(c, "blurb")
	require.NotNil(t, c.Annotations)
	assert.Equal(t, "blurb", c.Annotations[AIAgentHelpKey])
}

func TestTruthy(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"1", true},
		{"true", true},
		{"yes", true},
		{"anything-else", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, truthy(tc.in))
		})
	}
}

package cmdutil

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestSetAgentHelp_PrependsRiskWhenAnnotated(t *testing.T) {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a knowledge base.",
		Long:  "Delete a knowledge base.\n\nThis is irreversible.",
	}
	SetRisk(cmd, "kb.delete")
	SetAgentHelp(cmd, AgentHelp{
		UsedFor:  "remove a KB and all its docs/chunks",
		Warnings: []string{"never auto -y"},
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	t.Setenv("WEKNORA_AGENT_HELP", "")

	cmd.HelpFunc()(cmd, []string{})

	out := buf.String()
	assert.True(t, strings.HasPrefix(out, "Risk: kb.delete (destructive)\n\n"),
		"expected Risk: line at top; got:\n%s", out)
	assert.Contains(t, out, "AI agents:", "expected AI agents block still present")
	assert.Contains(t, out, "- never auto -y", "expected Warnings preserved")
}

func TestSetAgentHelp_NoRiskWhenUnannotated(t *testing.T) {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List knowledge bases.",
		Long:  "List knowledge bases.",
	}
	// NO SetRisk call — cmd.Annotations stays nil
	SetAgentHelp(cmd, AgentHelp{UsedFor: "list KBs"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	t.Setenv("WEKNORA_AGENT_HELP", "")

	cmd.HelpFunc()(cmd, []string{})

	out := buf.String()
	assert.False(t, strings.HasPrefix(out, "Risk:"),
		"expected NO Risk: line when not SetRisk'd; got:\n%s", out)
}

func TestSetAgentHelp_JSONPathUnaffectedByRisk(t *testing.T) {
	cmd := &cobra.Command{Use: "delete"}
	SetRisk(cmd, "kb.delete")
	SetAgentHelp(cmd, AgentHelp{UsedFor: "x", Warnings: []string{"w1"}})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	t.Setenv("WEKNORA_AGENT_HELP", "1")

	cmd.HelpFunc()(cmd, []string{})

	out := buf.String()
	assert.False(t, strings.HasPrefix(out, "Risk:"),
		"JSON path should NOT contain Risk: prefix; got:\n%s", out)
	assert.Contains(t, out, `"used_for": "x"`, "expected JSON body emitted")
}

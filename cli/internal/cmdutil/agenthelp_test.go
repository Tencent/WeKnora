package cmdutil

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
)

func TestSetAgentHelp_EmitsJSONWhenEnvSet(t *testing.T) {
	t.Setenv("WEKNORA_AGENT_HELP", "1")
	cmd := &cobra.Command{Use: "foo"}
	ah := AgentHelp{
		UsedFor:       "frob a bar",
		RequiredFlags: []string{"--name"},
		Examples:      []string{"weknora foo --name=x"},
	}
	SetAgentHelp(cmd, ah)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.Help()

	var got AgentHelp
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, buf.String())
	}
	if got.UsedFor != "frob a bar" {
		t.Errorf("UsedFor=%q, want %q", got.UsedFor, "frob a bar")
	}
	if len(got.RequiredFlags) != 1 || got.RequiredFlags[0] != "--name" {
		t.Errorf("RequiredFlags=%v", got.RequiredFlags)
	}
}

func TestSetAgentHelp_FallsThroughToHumanHelp(t *testing.T) {
	t.Setenv("WEKNORA_AGENT_HELP", "")
	cmd := &cobra.Command{
		Use:   "foo",
		Short: "frob a bar",
		Long:  "Detailed human help here.",
	}
	SetAgentHelp(cmd, AgentHelp{UsedFor: "ignored"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.Help()

	// Should NOT be JSON (human help is text)
	if json.Valid(buf.Bytes()) {
		t.Errorf("expected human text, got JSON:\n%s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("Detailed human help")) {
		t.Errorf("human help missing from output:\n%s", buf.String())
	}
}

func TestSetAgentHelp_AgentJSON_NoTrailingWarningsProse(t *testing.T) {
	cmd := &cobra.Command{Use: "kb-delete-test"}
	SetAgentHelp(cmd, AgentHelp{
		UsedFor:  "Delete a knowledge base",
		Warnings: []string{"irreversible"},
	})
	t.Setenv("WEKNORA_AGENT_HELP", "1")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.HelpFunc()(cmd, nil)

	// Should be parseable as a single JSON object with the warnings field.
	var got AgentHelp
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("agent-help output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got.Warnings) != 1 || got.Warnings[0] != "irreversible" {
		t.Errorf("warnings field missing or wrong: %v", got.Warnings)
	}
	// Should NOT contain the human-prose "AI agents:" block
	if bytes.Contains(buf.Bytes(), []byte("AI agents:")) {
		t.Errorf("agent JSON path leaked human prose:\n%s", buf.String())
	}
}

func TestSetAgentHelp_HumanHelp_AppendsWarningsBlock(t *testing.T) {
	cmd := &cobra.Command{Use: "kb-delete-test", Short: "short"}
	SetAgentHelp(cmd, AgentHelp{
		UsedFor:  "Delete a KB",
		Warnings: []string{"irreversible"},
	})
	// Env unset → human help path
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.HelpFunc()(cmd, nil)
	if !bytes.Contains(buf.Bytes(), []byte("AI agents:")) {
		t.Errorf("human help should include AI agents: block; got:\n%s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("- irreversible")) {
		t.Errorf("human help should list warning; got:\n%s", buf.String())
	}
}

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

package cmd

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// TestEveryLeafCommandHasAgentHelp enforces the agent-first contract: every
// leaf (runnable, no subcommands) command must emit a structured AgentHelp JSON
// blob under WEKNORA_AGENT_HELP=1, so an agent never has to scrape human prose.
// Drift guard in the spirit of the K6/K7 skill-parity tests: a new leaf command
// added without SetAgentHelp fails CI here.
//
// Exemptions: cobra's generated `completion`/`help` subtrees carry no
// domain semantics worth a machine blob.
func TestEveryLeafCommandHasAgentHelp(t *testing.T) {
	t.Setenv("WEKNORA_AGENT_HELP", "1")
	root := NewRootCmd(cmdutil.New())

	var missing []string
	eachLeafCommand(root, func(c *cobra.Command) {
		// Invoke the leaf's help func with the agent env set and require JSON.
		var buf bytes.Buffer
		c.SetOut(&buf)
		c.Help()
		var ah struct {
			UsedFor string `json:"used_for"`
		}
		if err := json.Unmarshal(buf.Bytes(), &ah); err != nil || ah.UsedFor == "" {
			missing = append(missing, c.CommandPath())
		}
	})

	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("leaf commands missing agent-help JSON (register cmdutil.SetAgentHelp):\n  %v\n(%d commands)",
			missing, len(missing))
	}
}

// renderAgentHelp runs a leaf's help under WEKNORA_AGENT_HELP=1 and decodes the
// machine blob (used_for / output / examples) an agent would read.
func renderAgentHelp(t *testing.T, c *cobra.Command) struct {
	UsedFor  string   `json:"used_for"`
	Output   string   `json:"output"`
	Examples []string `json:"examples"`
} {
	t.Helper()
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.Help()
	var ah struct {
		UsedFor  string   `json:"used_for"`
		Output   string   `json:"output"`
		Examples []string `json:"examples"`
	}
	if err := json.Unmarshal(buf.Bytes(), &ah); err != nil {
		t.Fatalf("%s: agent-help is not JSON: %v", c.CommandPath(), err)
	}
	return ah
}

// TestEveryLeafCommandDeclaresOutput enforces that every leaf command tells an
// agent what its stdout carries. Even side-effect commands describe their
// envelope (e.g. deletes emit {id, deleted:true}); an empty Output is a contract
// gap, not a valid state. Sibling drift guard to the agent-help test above.
func TestEveryLeafCommandDeclaresOutput(t *testing.T) {
	t.Setenv("WEKNORA_AGENT_HELP", "1")
	root := NewRootCmd(cmdutil.New())

	var missing []string
	eachLeafCommand(root, func(c *cobra.Command) {
		if renderAgentHelp(t, c).Output == "" {
			missing = append(missing, c.CommandPath())
		}
	})

	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("leaf commands missing agent-help Output (declare AgentHelp.Output):\n  %v\n(%d commands)",
			missing, len(missing))
	}
}

// TestEveryLeafCommandHasExample enforces that every leaf ships at least one
// runnable example — agents learn invocation shape from examples, not prose.
func TestEveryLeafCommandHasExample(t *testing.T) {
	t.Setenv("WEKNORA_AGENT_HELP", "1")
	root := NewRootCmd(cmdutil.New())

	var missing []string
	eachLeafCommand(root, func(c *cobra.Command) {
		if len(renderAgentHelp(t, c).Examples) == 0 {
			missing = append(missing, c.CommandPath())
		}
	})

	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("leaf commands missing agent-help Examples (declare AgentHelp.Examples):\n  %v\n(%d commands)",
			missing, len(missing))
	}
}

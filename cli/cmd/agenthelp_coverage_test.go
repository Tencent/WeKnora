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

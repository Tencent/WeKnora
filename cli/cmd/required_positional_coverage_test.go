package cmd

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// TestLeafCommandsDeclareRequiredPositional guards the agent-first contract: a
// command whose Use string carries a <...> positional placeholder (it takes a
// required positional arg, enforced by cobra.ExactArgs/MinimumNArgs) MUST also
// declare that positional in its agent-help RequiredFlags. Otherwise
// `weknora schema <cmd>` under-reports the requirement, and an agent that builds
// a call from schema omits the positional and hits "accepts N arg(s), received 0".
//
// Sibling of TestEveryLeafCommandHasAgentHelp / the dry-run coverage guard.
func TestLeafCommandsDeclareRequiredPositional(t *testing.T) {
	t.Setenv("WEKNORA_AGENT_HELP", "1")
	root := NewRootCmd(cmdutil.New())

	var missing []string
	eachLeafCommand(root, func(c *cobra.Command) {
		// Only flag a command whose Use carries a *positional* <...> placeholder
		// (codebase convention). A <...> that follows a --flag is a flag value
		// (e.g. "list --session <session-id>"), not a positional — skip those.
		if !hasPositionalPlaceholder(c.Use) {
			return
		}
		var buf bytes.Buffer
		c.SetOut(&buf)
		_ = c.Help()
		var ah struct {
			RequiredFlags []string `json:"required_flags"`
		}
		_ = json.Unmarshal(buf.Bytes(), &ah)
		declared := false
		for _, rf := range ah.RequiredFlags {
			if strings.Contains(rf, "positional") {
				declared = true
				break
			}
		}
		if !declared {
			missing = append(missing, c.CommandPath()+"  (Use: "+c.Use+")")
		}
	})

	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("leaf commands take a required positional but don't declare it in agent-help RequiredFlags "+
			"(so `weknora schema <cmd>` under-reports the requirement). Add a \"<name> (positional)\" entry "+
			"to each SetAgentHelp RequiredFlags:\n  %s\n(%d commands)",
			strings.Join(missing, "\n  "), len(missing))
	}
}

// hasPositionalPlaceholder reports whether the Use string declares a required
// positional argument: a <...> token (after the command name) that is NOT
// preceded by a --flag token (which would make it a flag value, not a positional).
func hasPositionalPlaceholder(use string) bool {
	fields := strings.Fields(use)
	for i := 1; i < len(fields); i++ { // fields[0] is the command name
		if strings.Contains(fields[i], "<") && !strings.HasPrefix(fields[i-1], "-") {
			return true
		}
	}
	return false
}

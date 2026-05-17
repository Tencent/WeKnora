// Package-level note:
//
// SetAgentHelp wires structured agent-targeted help onto a cobra command.
// Current coverage: chat, kb list. Adding it to another command requires
// touching only that command's NewCmd (a 5-line copy of the existing call
// sites).
package cmdutil

import (
	"encoding/json"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// AgentHelp is the structured help blob emitted when an agent invokes
// `weknora <command> --help` with WEKNORA_AGENT_HELP=1. Distinct from
// cobra's human help text — agent-readable JSON keyed by stable fields
// so an LLM doesn't need to scrape the human help table.
type AgentHelp struct {
	UsedFor       string   `json:"used_for"`
	RequiredFlags []string `json:"required_flags,omitempty"`
	Examples      []string `json:"examples,omitempty"`
	Output        string   `json:"output,omitempty"`
}

// SetAgentHelp attaches agent-targeted help metadata to a command. The
// original HelpFunc is preserved for the human path; the agent path
// activates only when WEKNORA_AGENT_HELP=1 (so human `--help` is
// unaffected).
//
// Currently applied to a small set of representative commands (chat,
// kb list); extend by calling SetAgentHelp in each command's NewCmd.
func SetAgentHelp(cmd *cobra.Command, ah AgentHelp) {
	origHelp := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if os.Getenv("WEKNORA_AGENT_HELP") == "1" {
			emitAgentHelp(c.OutOrStdout(), ah)
			return
		}
		origHelp(c, args)
	})
}

func emitAgentHelp(w io.Writer, ah AgentHelp) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(ah)
}

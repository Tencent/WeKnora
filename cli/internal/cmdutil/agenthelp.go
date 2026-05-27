// Package-level note:
//
// SetAgentHelp wires structured agent-targeted help onto a cobra command.
// Current coverage: chat, kb list, and destructive commands. Adding it to
// another command requires touching only that command's NewCmd (a 5-line
// copy of the existing call sites).
package cmdutil

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// AgentHelp is the structured help blob emitted when an agent invokes
// `weknora <command> --help` with WEKNORA_AGENT_HELP=1. Distinct from
// cobra's human help text — agent-readable JSON keyed by stable fields
// so an LLM doesn't need to scrape the human help table.
//
// Warnings surface both as a JSON field (agent introspection) and as an
// "AI agents:" block appended to human help. The two channels share a
// single source list so they cannot drift.
type AgentHelp struct {
	UsedFor       string   `json:"used_for"`
	RequiredFlags []string `json:"required_flags,omitempty"`
	Examples      []string `json:"examples,omitempty"`
	Output        string   `json:"output,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

// SetAgentHelp attaches agent-targeted help metadata to a command.
//
// Routing:
//   - WEKNORA_AGENT_HELP=1: emit the AgentHelp JSON blob to stdout and
//     return — agents get clean parseable JSON, no trailing prose.
//   - Otherwise: render the normal human help, then append an
//     "AI agents:" block when Warnings is non-empty.
func SetAgentHelp(cmd *cobra.Command, ah AgentHelp) {
	origHelp := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if os.Getenv("WEKNORA_AGENT_HELP") == "1" {
			emitAgentHelp(c.OutOrStdout(), ah)
			return
		}
		origHelp(c, args)
		if len(ah.Warnings) > 0 {
			w := c.OutOrStdout()
			fmt.Fprintln(w)
			fmt.Fprintln(w, "AI agents:")
			for _, msg := range ah.Warnings {
				fmt.Fprintln(w, "- "+msg)
			}
		}
	})
}

func emitAgentHelp(w io.Writer, ah AgentHelp) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(ah)
}

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

// agentHelpAnnotation stores the marshaled AgentHelp on the command so the
// `weknora schema` command can introspect any command's contract without
// invoking its help func (and toggling WEKNORA_AGENT_HELP). Shares the
// annotations map with SetRisk (distinct keys).
const agentHelpAnnotation = "weknora.agent_help"

// SetAgentHelp attaches agent-targeted help metadata to a command.
//
// Routing:
//   - WEKNORA_AGENT_HELP=1: emit the AgentHelp JSON blob to stdout and
//     return — agents get clean parseable JSON, no trailing prose.
//   - Otherwise (human help path): if cmd has risk annotations from SetRisk,
//     prepend "Risk: <action> (<level>)" line; then render the normal human
//     help via origHelp; then append the "AI agents:" Warnings block.
//
// The AgentHelp is also recorded as a JSON annotation so `weknora schema` can
// surface it as a first-class, discoverable command (not only via the
// WEKNORA_AGENT_HELP env toggle on --help).
func SetAgentHelp(cmd *cobra.Command, ah AgentHelp) {
	if b, err := json.Marshal(ah); err == nil {
		if cmd.Annotations == nil {
			cmd.Annotations = make(map[string]string)
		}
		cmd.Annotations[agentHelpAnnotation] = string(b)
	}
	origHelp := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if os.Getenv("WEKNORA_AGENT_HELP") == "1" {
			emitAgentHelp(c.OutOrStdout(), ah)
			return
		}
		// Prepend Risk: line in the default (human) branch only; the
		// JSON branch above already carries warnings[]. Skip silently if
		// the command has no risk annotations.
		if level, action, ok := GetRisk(c); ok {
			fmt.Fprintf(c.OutOrStdout(), "Risk: %s (%s)\n\n", action, level)
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

// AgentHelpFor returns the AgentHelp registered on cmd via SetAgentHelp.
// ok is false when the command carries none (e.g. cobra-generated subtrees).
func AgentHelpFor(cmd *cobra.Command) (ah AgentHelp, ok bool) {
	if cmd.Annotations == nil {
		return ah, false
	}
	raw, present := cmd.Annotations[agentHelpAnnotation]
	if !present {
		return ah, false
	}
	if err := json.Unmarshal([]byte(raw), &ah); err != nil {
		return ah, false
	}
	return ah, true
}

func emitAgentHelp(w io.Writer, ah AgentHelp) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(ah)
}

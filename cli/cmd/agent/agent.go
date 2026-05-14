// Package agentcmd holds the `weknora agent` command tree:
// list / view / invoke. The directory is named `agent/` (matches cobra
// noun-verb convention) but the Go package is `agentcmd` to avoid
// shadowing cli/internal/agent (which owns SetAgentHelp + agent-aware
// help rendering for AI coding agents — a distinct concept from
// WeKnora's first-class custom-agent resources).
//
// Disambiguation in cli/AGENTS.md: "agent" in this subtree refers to
// WeKnora's user-defined Custom Agents (server resource: GET/POST
// /agents/...). The CLI's `agent invoke` calls /agent-chat/:session_id
// which dispatches the agent's configured workflow (system prompt,
// allowed tools, KB scope, retrieval thresholds, etc.). This is
// orthogonal to AGENTS.md / SetAgentHelp, which exists so AI coding
// agents (Claude Code, Cursor) consuming the CLI can read structured
// hints from --help.
package agentcmd

import (
	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// NewCmd builds the `weknora agent` parent and registers leaves. Called
// from cli/cmd/root.go.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage and invoke custom agents",
		Long: `Custom Agents bundle a system prompt, model, tool allow-list, and KB
scope into an addressable resource. List visible agents, view a single
agent's configuration, or invoke an agent against a query.

Distinct from the per-CLI ` + "`AGENTS.md`" + ` reference: that file documents the
contract for AI coding agents (Claude Code / Cursor) running ` + "`weknora`" + `
on a user's behalf. This subtree manages WeKnora's first-class custom-agent
resources stored on the server.`,
		Args: cobra.NoArgs,
		Run:  func(c *cobra.Command, _ []string) { _ = c.Help() },
	}
	cmd.AddCommand(NewCmdList(f))
	cmd.AddCommand(NewCmdView(f))
	cmd.AddCommand(NewCmdInvoke(f))
	return cmd
}

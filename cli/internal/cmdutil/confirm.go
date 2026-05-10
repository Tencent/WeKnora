package cmdutil

import (
	"fmt"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// ConfirmDestructive prompts the user to confirm a destructive operation
// (e.g. delete) when the call is interactive, and proceeds without
// prompting otherwise. Behavior matrix:
//
//   yes=true            → proceed (skip prompt; explicit user opt-in via -y)
//   non-TTY stdout      → proceed (no UI to ask; safer to follow the call
//                                  than to break scripts/CI that don't pass
//                                  -y every time)
//   jsonOut=true        → proceed (envelope mode is by definition scripted,
//                                  a prompt would deadlock the consumer)
//   TTY + interactive   → prompt; user-yes proceeds, user-no returns
//                         CodeUserAborted ("Aborted." to stderr)
//   prompter error      → returns CodeInputMissingFlag (rare; AgentPrompter
//                         path or stdin closed mid-prompt)
//
// `yes` should be sourced from the persistent global -y/--yes flag (see
// addGlobalFlags in cli/cmd/root.go). Mirrors gh's `--yes` semantics on
// destructive commands: gh repo delete --yes
// (https://cli.github.com/manual/gh_repo_delete).
func ConfirmDestructive(p prompt.Prompter, yes, jsonOut bool, what, id string) error {
	if yes || !iostreams.IO.IsStdoutTTY() || jsonOut {
		return nil
	}
	ok, err := p.Confirm(fmt.Sprintf("Delete %s %s? This cannot be undone.", what, id), false)
	if err != nil {
		return Wrapf(CodeInputMissingFlag, err, "confirm delete")
	}
	if !ok {
		fmt.Fprintln(iostreams.IO.Err, "Aborted.")
		return NewError(CodeUserAborted, "delete aborted")
	}
	return nil
}

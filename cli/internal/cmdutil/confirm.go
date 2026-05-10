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
//   force=true          → proceed (skip prompt; explicit user opt-in)
//   non-TTY stdout      → proceed (no UI to ask; safer to follow the call
//                                  than to break scripts/CI that don't pass
//                                  --force every time)
//   jsonOut=true        → proceed (envelope mode is by definition scripted,
//                                  a prompt would deadlock the consumer)
//   TTY + interactive   → prompt; user-yes proceeds, user-no returns
//                         CodeUserAborted ("Aborted." to stderr)
//   prompter error      → returns CodeInputMissingFlag (rare; AgentPrompter
//                         path or stdin closed mid-prompt)
//
// Callers that want a hard fail in non-TTY contexts should validate at the
// command layer (e.g. require --force when stdout is not a terminal). The
// agent-help text on each destructive command says "ALWAYS pass --force in
// agent mode" — this is the documented contract, not an enforcement here.
func ConfirmDestructive(p prompt.Prompter, force, jsonOut bool, what, id string) error {
	if force || !iostreams.IO.IsStdoutTTY() || jsonOut {
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

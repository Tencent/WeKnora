package cmdutil

import (
	"fmt"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// ConfirmDestructive prompts the user to confirm a destructive operation
// (e.g. delete). Returns nil if the operation should proceed, or a typed
// error otherwise:
//   - CodeInputMissingFlag when the prompter rejects (non-TTY / agent mode):
//     the caller forgot --force.
//   - CodeUserAborted when the user explicitly answers no — also writes
//     "Aborted." to stderr so the rejection is visible above the envelope.
//
// Skipped entirely when force is true, or when stdout is non-TTY, or when
// the caller is going to emit a JSON envelope (jsonOut=true) — envelope
// mode is by definition scripted, so a confirmation prompt would deadlock
// the consumer.
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

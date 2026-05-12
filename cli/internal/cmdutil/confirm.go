package cmdutil

import (
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// ConfirmDestructive guards a destructive operation (delete, force-overwrite)
// behind explicit user approval. Behavior matrix:
//
//   yes=true            → proceed (explicit user opt-in via -y/--yes)
//   non-TTY OR jsonOut  → return CodeInputConfirmationRequired (exit 10);
//                         no UI to prompt, agent/CI must re-invoke with -y
//                         after the human explicitly approves
//   TTY + interactive   → prompt; user-yes proceeds, user-no returns
//                         CodeUserAborted ("Aborted." to stderr)
//   prompter error      → returns CodeInputMissingFlag (rare; stdin closed
//                         mid-prompt)
//
// The non-TTY branch is the destructive-write protocol: high-risk writes
// always require explicit confirmation in scripted contexts, never silent
// proceed. See cli/AGENTS.md "Exit codes".
//
// `yes` should be sourced from the persistent global -y/--yes flag.
//
// On exit-10 path, the returned *Error carries OperationRisk so the envelope
// printer attaches `risk: {level: "high-risk-write", action: ...}`.
func ConfirmDestructive(p prompt.Prompter, yes, jsonOut bool, what, id string) error {
	if yes {
		return nil
	}
	risk := &OperationRisk{Level: "high-risk-write", Action: fmt.Sprintf("delete %s %s", what, id)}
	if !iostreams.IO.IsStdoutTTY() || jsonOut {
		e := NewError(
			CodeInputConfirmationRequired,
			fmt.Sprintf("delete %s %s requires explicit confirmation: re-run with -y/--yes", what, id),
		)
		var typed *Error
		if errors.As(e, &typed) {
			typed.OperationRisk = risk
		}
		return e
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

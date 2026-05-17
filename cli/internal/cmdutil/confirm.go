package cmdutil

import (
	"fmt"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// ConfirmDestructiveBatch is the multi-id flavor of ConfirmDestructive: same
// behavior matrix (yes / non-TTY / TTY-prompt / user-no) but the prompt text
// reflects the count, not a single id. Used by `doc delete <id> [<id>...]`
// — one -y confirms all items in the batch.
//
// Pass n = total count of items about to be deleted.
func ConfirmDestructiveBatch(p prompt.Prompter, yes, jsonOut bool, what string, n int) error {
	if yes {
		return nil
	}
	if !iostreams.IO.IsStdoutTTY() || jsonOut {
		return NewError(
			CodeInputConfirmationRequired,
			fmt.Sprintf("delete %d %s(s) requires explicit confirmation: re-run with -y/--yes", n, what),
		)
	}
	ok, err := p.Confirm(fmt.Sprintf("Delete %d %s(s)? This cannot be undone.", n, what), false)
	if err != nil {
		return Wrapf(CodeInputMissingFlag, err, "confirm batch delete")
	}
	if !ok {
		fmt.Fprintln(iostreams.IO.Err, "Aborted.")
		return NewError(CodeUserAborted, "delete aborted")
	}
	return nil
}

// ConfirmDestructive guards a destructive operation (delete, force-overwrite)
// behind explicit user approval. Behavior matrix:
//
//	yes=true            → proceed (explicit user opt-in via -y/--yes)
//	non-TTY OR jsonOut  → return CodeInputConfirmationRequired (exit 10);
//	                      no UI to prompt, agent/CI must re-invoke with -y
//	                      after the human explicitly approves
//	TTY + interactive   → prompt; user-yes proceeds, user-no returns
//	                      CodeUserAborted ("Aborted." to stderr)
//	prompter error      → returns CodeInputMissingFlag (rare; stdin closed
//	                      mid-prompt)
//
// The non-TTY branch is the destructive-write protocol: high-risk writes
// always require explicit confirmation in scripted contexts, never silent
// proceed. See cli/README.md "Exit codes".
//
// `yes` should be sourced from the persistent global -y/--yes flag.
func ConfirmDestructive(p prompt.Prompter, yes, jsonOut bool, what, id string) error {
	if yes {
		return nil
	}
	if !iostreams.IO.IsStdoutTTY() || jsonOut {
		return NewError(
			CodeInputConfirmationRequired,
			fmt.Sprintf("delete %s %s requires explicit confirmation: re-run with -y/--yes", what, id),
		)
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

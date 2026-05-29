package cmdutil

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
)

// confirmCaveat returns the trailing safety note shown after the interactive
// prompt, tailored to the verb. Deletes/removals are irreversible; edits
// overwrite prior values but are recoverable if you know the old value.
func confirmCaveat(verb string) string {
	switch verb {
	case "edit":
		return "This overwrites the current values."
	default: // delete / remove — irreversible
		return "This cannot be undone."
	}
}

// titleFirst upper-cases the first rune so the interactive prompt reads as a
// sentence ("Delete …?" / "Edit …?") without pulling in golang.org/x/text.
func titleFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// ConfirmDestructiveBatch is the multi-id flavor of ConfirmDestructive: same
// behavior matrix (yes / non-TTY / TTY-prompt / user-no) but the prompt text
// reflects the count, not a single id. Used by `doc delete <id> [<id>...]`
// — one -y confirms all items in the batch.
//
// Pass n = total count of items about to be deleted.
// action is the namespaced action verb (e.g. "doc.delete") for the risk envelope.
// retryCmd is the directly-executable retry argv (e.g. "weknora doc delete a b -y");
// pass "" when no clean retry argv is available.
func ConfirmDestructiveBatch(p prompt.Prompter, yes, jsonOut bool, verb, what string, n int, action, retryCmd string) error {
	if yes {
		return nil
	}
	if !iostreams.IO.IsStdoutTTY() || jsonOut {
		return NewError(
			CodeInputConfirmationRequired,
			fmt.Sprintf("%s %d %s(s) requires explicit confirmation: re-run with -y/--yes", verb, n, what),
		).
			WithRetryCommand(retryCmd).
			WithRisk("destructive", action)
	}
	ok, err := p.Confirm(fmt.Sprintf("%s %d %s(s)? %s", titleFirst(verb), n, what, confirmCaveat(verb)), false)
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
// action is the namespaced action verb (e.g. "kb.delete") for the risk envelope.
// retryCmd is the directly-executable retry argv (e.g. "weknora kb delete kb_x -y");
// pass "" when no clean retry argv is available.
func ConfirmDestructive(p prompt.Prompter, yes, jsonOut bool, verb, what, id, action, retryCmd string) error {
	if yes {
		return nil
	}
	if !iostreams.IO.IsStdoutTTY() || jsonOut {
		return NewError(
			CodeInputConfirmationRequired,
			fmt.Sprintf("%s %s %s requires explicit confirmation: re-run with -y/--yes", verb, what, id),
		).
			WithRetryCommand(retryCmd).
			WithRisk("destructive", action)
	}
	ok, err := p.Confirm(fmt.Sprintf("%s %s %s? %s", titleFirst(verb), what, id, confirmCaveat(verb)), false)
	if err != nil {
		return Wrapf(CodeInputMissingFlag, err, "confirm delete")
	}
	if !ok {
		fmt.Fprintln(iostreams.IO.Err, "Aborted.")
		return NewError(CodeUserAborted, "delete aborted")
	}
	return nil
}

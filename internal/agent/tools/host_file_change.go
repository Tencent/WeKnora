package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// authorizeHostWrite asks once, before any I/O, for a write outside the host
// workspace. It returns the context the rest of the call must use, or a
// message for the model when the write may not happen.
func authorizeHostWrite(
	ctx context.Context, store any, approvals HostApprovals,
	tool, sessionID, path, mode string, apply func([]byte, bool) ([]byte, error), preview string,
) (context.Context, string) {
	authorizer, ok := store.(sandbox.SessionFileChangeAuthorizer)
	if !ok {
		return ctx, fmt.Sprintf("%s is outside the workspace, and this deployment cannot approve it", path)
	}
	ctx = approvals.attach(ctx, sandbox.CommandApproval{Tool: tool})
	authed, err := authorizer.AuthorizeFileChange(ctx, sessionID, sandbox.FileChange{
		Path: path, Mode: mode, Apply: apply, Preview: preview,
	})
	if err != nil {
		return ctx, fmt.Sprintf("cannot write %s: %v", path, err)
	}
	return authed, ""
}

// sandboxEditsPreview lists the replacements as the model wrote them. It holds
// nothing from the file, so it can be shown before the file may be read.
func sandboxEditsPreview(edits []SandboxEdit) string {
	var b strings.Builder
	for _, edit := range edits {
		if edit.ReplaceAll {
			b.WriteString("# every match\n")
		}
		for _, line := range strings.Split(edit.OldString, "\n") {
			b.WriteString("- " + line + "\n")
		}
		for _, line := range strings.Split(edit.NewString, "\n") {
			b.WriteString("+ " + line + "\n")
		}
	}
	return b.String()
}

package adapter

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

var _ sandbox.SessionFileChangeAuthorizer = (*Adapter)(nil)

type fileApprovalKey struct{}

func withFileApproval(ctx context.Context, a *localsandbox.FileApproval) context.Context {
	return context.WithValue(ctx, fileApprovalKey{}, a)
}

func fileApprovalFrom(ctx context.Context) *localsandbox.FileApproval {
	a, _ := ctx.Value(fileApprovalKey{}).(*localsandbox.FileApproval)
	return a
}

// AuthorizeFileChange asks the session's approver for one out-of-scope write.
func (a *Adapter) AuthorizeFileChange(
	ctx context.Context, sessionID string, change sandbox.FileChange,
) (context.Context, error) {
	_, ws, err := a.svc.GuardForSession(ctx, sessionID)
	if err != nil {
		return ctx, err
	}
	user, reviewer, spec := runApprovals(ctx)
	approval, err := a.svc.AuthorizeFileChange(ctx, localsandbox.FileChangeRequest{
		SessionID: sessionID, Path: absoluteIn(ws.Root, change.Path), Mode: change.Mode, Apply: change.Apply,
		Preview: change.Preview, Spec: spec, Approver: user, Reviewer: reviewer,
	})
	if err != nil || approval == nil {
		return ctx, err
	}
	return withFileApproval(ctx, approval), nil
}

// fileGrantWrite applies one grant decision to a write: bind the digest when
// target is that file, and return the path WriteFile must use.
func fileGrantWrite(
	guard *localsandbox.PathGuard, approval *localsandbox.FileApproval, target string, content []byte,
) (string, error) {
	if approval == nil {
		return target, nil
	}
	writePath, matches := guard.GrantWritePath(approval.Grant.Path, target)
	if matches && sha256.Sum256(content) != approval.Digest {
		return "", fmt.Errorf("%w: %s changed after it was approved; run the tool again",
			localsandbox.ErrPathDenied, target)
	}
	return writePath, nil
}

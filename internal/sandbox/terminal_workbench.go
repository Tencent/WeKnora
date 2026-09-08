package sandbox

import "context"

// SessionWorkbenchInitializer provisions the bound session only on an explicit
// workbench initialization action. File reads remain lookup-only operations.
type SessionWorkbenchInitializer interface {
	EnsureWorkbenchSession(context.Context, string) error
}

func (m *SessionBoundManager) EnsureWorkbenchSession(ctx context.Context, sessionID string) error {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	handle, err := m.resolveSession(ctx, sessionID)
	if err != nil {
		return err
	}
	return m.ensureSessionWorkspaceDirs(ctx, handle, SessionOutputRoot)
}

var _ SessionWorkbenchInitializer = (*SessionBoundManager)(nil)

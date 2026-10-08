package sandbox

import (
	"context"
	"sync"
)

type sessionFileOperationKey struct{}

type sessionFileHandleKey struct {
	manager *SessionBoundManager
	session SessionSandboxKey
}

type sessionFileOperation struct {
	mu      sync.Mutex
	handles map[sessionFileHandleKey]RemoteSandboxHandle
}

// WithSessionFileOperation reuses live handles only within one read/collection
// operation. Callers must create a fresh scope for every tool call or collect,
// never store it on a manager or reuse it across turns. A manager and tenant-
// scoped session key identify each handle, so scopes cannot cross backends or
// tenants. Lookup remains non-provisioning and failures are never cached.
func WithSessionFileOperation(ctx context.Context) context.Context {
	return context.WithValue(ctx, sessionFileOperationKey{}, &sessionFileOperation{
		handles: make(map[sessionFileHandleKey]RemoteSandboxHandle),
	})
}

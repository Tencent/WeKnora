package service

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// sessionConfigReader reads the session sandbox pin. *SessionSandboxPinner
// is the production implementation; a named-config pin is the signal even
// though host itself never writes one.
type sessionConfigReader interface {
	Read(ctx context.Context, sessionID string) (SandboxPin, error)
}

// HostSessionResolver answers "is this session running on the host backend".
//
// Host is not a named backend, so it never writes a pin. A named ConfigID
// means the session belongs to a remote backend; an empty or
// SandboxConfigIDGlobalDefault pin plus a live host manager is the host path.
// PinnedSessionSandbox must not fall back to host for ExecShellCommand: that
// would hand WorkspaceCheckpointer a live runner and start committing the
// user's own repository once a turn.
type HostSessionResolver struct {
	sessions sessionConfigReader // reads the session sandbox pin
	host     sandbox.Manager     // nil outside Lite / unsupported platforms
}

// NewHostSessionResolver wires the shared host-session probe. A nil host
// manager keeps every caller on the remote path, byte-for-byte.
func NewHostSessionResolver(sessions sessionConfigReader, host sandbox.Manager) *HostSessionResolver {
	return &HostSessionResolver{sessions: sessions, host: host}
}

// HostManagerFor returns the host manager when this session has no named
// sandbox config and this process has a host backend; nil otherwise.
func (r *HostSessionResolver) HostManagerFor(ctx context.Context, sessionID string) sandbox.Manager {
	if r == nil || r.host == nil || r.sessions == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if r.host.GetType() != sandbox.SandboxTypeHost {
		return nil
	}
	pin, err := r.sessions.Read(ctx, sessionID)
	if err != nil {
		return nil
	}
	if hasNamedSandboxConfig(pin.ConfigID) {
		return nil
	}
	return r.host
}

func hasNamedSandboxConfig(configID string) bool {
	configID = strings.TrimSpace(configID)
	return configID != "" && configID != types.SandboxConfigIDGlobalDefault
}

var _ sessionConfigReader = (*SessionSandboxPinner)(nil)

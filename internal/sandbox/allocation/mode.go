// Package allocation is the domain model for placing a session on a remote
// sandbox: which mode it uses, whether its owner may share a machine, and the
// identity of a shared machine. It performs no I/O.
package allocation

import (
	"fmt"
	"strings"
)

// Mode is how a session is placed on remote machines.
type Mode string

const (
	// ModeSession gives the session a machine of its own (historical behaviour).
	ModeSession Mode = "session"
	// ModeUser places the session in a directory on its owner's shared machine.
	ModeUser Mode = "user"
)

// ParseMode accepts the stored config value. Empty means session.
func ParseMode(raw string) (Mode, error) {
	switch strings.TrimSpace(raw) {
	case "", string(ModeSession):
		return ModeSession, nil
	case string(ModeUser):
		return ModeUser, nil
	default:
		return "", fmt.Errorf("allocation: unknown mode %q", raw)
	}
}

// Purpose is why a session uses a sandbox.
type Purpose string

// Placement purposes. PurposeChat is an ordinary session.
// PurposeMaintenance is a skill-install session and stays on its own machine.
const (
	PurposeChat        Purpose = "chat"
	PurposeMaintenance Purpose = "maintenance"
)

// FallbackReason explains why a session did not get the config's mode. The
// values are stable, non-sensitive tokens safe to show an admin.
type FallbackReason string

// Reasons a placement stayed on a per-session machine.
const (
	ReasonNone                    FallbackReason = ""
	ReasonMaintenanceDedicated    FallbackReason = "MAINTENANCE_DEDICATED"
	ReasonUserModeUnavailable     FallbackReason = "USER_MODE_UNAVAILABLE"
	ReasonOwnerIdentityInvalid    FallbackReason = "OWNER_IDENTITY_INVALID"
	ReasonSharedAPIKey            FallbackReason = "SHARED_API_KEY"
	ReasonSharedMCPEndpoint       FallbackReason = "SHARED_MCP_ENDPOINT"
	ReasonChannelIdentity         FallbackReason = "CHANNEL_IDENTITY_NOT_SUPPORTED"
	ReasonOwnerIdentityUnverified FallbackReason = "OWNER_IDENTITY_UNVERIFIED"
	ReasonSessionIDNotUUID        FallbackReason = "SESSION_ID_NOT_UUID"
	ReasonLegacySessionSandbox    FallbackReason = "LEGACY_SESSION_SANDBOX"
)

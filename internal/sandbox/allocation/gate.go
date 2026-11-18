package allocation

import (
	"os"
	"strings"
	"sync/atomic"
)

// UserModeEnvVar opts a deployment into user-mode allocation. It must be set
// on every replica before any config is switched to "user".
const UserModeEnvVar = "WEKNORA_SANDBOX_USER_ALLOCATION"

var userModeAvailable atomic.Bool

// UserModeRequestedByEnv reports whether the operator asked for user mode.
func UserModeRequestedByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(UserModeEnvVar))) {
	case "1", "true", "on", "enabled":
		return true
	default:
		return false
	}
}

// SetUserModeAvailable is called once by the container after it knows whether
// the deployment can coordinate shared machines (env opt-in and Redis).
func SetUserModeAvailable(available bool) { userModeAvailable.Store(available) }

// UserModeAvailable reports the deployment gate.
func UserModeAvailable() bool { return userModeAvailable.Load() }

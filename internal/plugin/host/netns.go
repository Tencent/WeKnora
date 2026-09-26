package host

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
)

// envNetns says whether host plugins run in their own network namespace
// (Linux), where the egress proxy and the Host API are the only ways out:
//
//   - unset or "auto": when the system allows it, which is probed once. It
//     needs unprivileged user namespaces, which some systems and container
//     runtimes refuse; plugins then run with the egress proxy only, which
//     code that ignores the proxy variables bypasses, and a warning says so.
//   - "1": always; plugins fail to start rather than run unconfined.
//   - "0": never.
//
// On a standalone plugin host the Host API is on another node: a sandboxed
// plugin reaches it through the egress proxy, which lets that host pass.
const envNetns = "WEKNORA_PLUGIN_NETNS"

// netnsSetting is what WEKNORA_PLUGIN_NETNS asks for.
type netnsSetting int

const (
	netnsAuto netnsSetting = iota
	netnsRequired
	netnsOff
)

func netnsFromEnv() netnsSetting {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envNetns))) {
	case "1", "true", "yes", "on":
		return netnsRequired
	case "0", "false", "no", "off":
		return netnsOff
	}
	return netnsAuto
}

// sandboxCheck is whether this system can sandbox plugins, found out once.
var sandboxCheck struct {
	once sync.Once
	err  error
}

// checkSandbox probes the system; tests replace it.
var checkSandbox = probeSandbox

// sandboxUnavailable says why plugins cannot be sandboxed here, or nil when
// they can. The first call probes and logs the outcome.
func sandboxUnavailable() error {
	sandboxCheck.once.Do(func() {
		err := checkSandbox()
		sandboxCheck.err = err
		ctx := context.Background()
		switch {
		case err == nil:
			logger.Infof(ctx, "[plugin] host plugins run in their own network namespace; "+
				"the egress proxy is their only way out")
		case !sandboxOS:
			logger.Infof(ctx, "[plugin] host plugins run with the egress proxy only: %v", err)
		default:
			logger.Warnf(ctx, "[plugin] host plugins run WITHOUT a network sandbox: %v. Their egress grants are "+
				"applied by the egress proxy only, which plugin code that ignores HTTP(S)_PROXY bypasses. "+
				"To sandbox them, allow unprivileged user namespaces: sysctl kernel.unprivileged_userns_clone=1 "+
				"(Debian), kernel.apparmor_restrict_unprivileged_userns=0 (Ubuntu 23.10+), "+
				"user.max_user_namespaces>0; "+
				"in a container also a seccomp profile that allows unshare/clone of user namespaces "+
				"(Docker's default refuses them). Set %s=1 to refuse to start plugins unsandboxed, "+
				"or %s=0 to turn the sandbox and this warning off", err, envNetns, envNetns)
		}
	})
	return sandboxCheck.err
}

// useSandbox decides whether a plugin started now runs in its own network
// namespace.
func useSandbox() bool {
	switch netnsFromEnv() {
	case netnsRequired:
		return true
	case netnsOff:
		return false
	}
	return sandboxUnavailable() == nil
}

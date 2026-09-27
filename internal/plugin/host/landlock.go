package host

import (
	"context"
	"errors"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/sandbox"
)

// envLandlock says whether host plugins, which run as WeKnora's user, are
// kept by Landlock (Linux 5.13+) to the system's files, their package and a
// directory of their own, away from WeKnora's data, config and other
// plugins:
//
//   - unset or "auto": when the kernel offers Landlock, which Docker's
//     default seccomp profile allows; otherwise plugins can read and change
//     what WeKnora's user can, and a warning says so.
//   - "1": always; plugins fail to start rather than run unconfined.
//   - "0": never.
//
// A plugin outside a network namespace (see WEKNORA_PLUGIN_NETNS) is also
// kept to TCP connections to the egress proxy's and the Host API's ports,
// from Linux 6.7.
const envLandlock = "WEKNORA_PLUGIN_LANDLOCK"

// landlockCheck is whether this system confines plugins with Landlock,
// found out once.
var landlockCheck struct {
	once sync.Once
	err  error
}

// checkLandlock probes the system; tests replace it.
var checkLandlock = probeLandlock

func probeLandlock() error {
	if !sandboxOS {
		return errors.New("file isolation (Landlock) needs Linux")
	}
	if !sandbox.HelperRegistered() {
		return errNoHelper
	}
	if sandbox.LandlockABI() < 1 {
		return errors.New("the kernel offers no Landlock (it needs Linux 5.13+ with landlock among the " +
			"enabled LSMs, e.g. the lsm= boot parameter)")
	}
	return nil
}

// landlockUnavailable says why plugins cannot be confined with Landlock
// here, or nil when they can. The first call probes and logs the outcome.
func landlockUnavailable() error {
	landlockCheck.once.Do(func() {
		err := checkLandlock()
		landlockCheck.err = err
		ctx := context.Background()
		switch {
		case err == nil:
			logger.Infof(ctx, "[plugin] host plugins are kept by Landlock to the system's files, their package "+
				"and a directory of their own")
		case !sandboxOS || errors.Is(err, errNoHelper):
			logger.Infof(ctx, "[plugin] host plugins run with WeKnora's files in reach: %v", err)
		default:
			logger.Warnf(ctx, "[plugin] host plugins run WITHOUT file isolation: %v. They run as WeKnora's user "+
				"and can read and change what it can: its data and config, other plugins' files. "+
				"Set %s=1 to refuse to start plugins without it, or %s=0 to turn it and this warning off",
				err, envLandlock, envLandlock)
		}
	})
	return landlockCheck.err
}

// useLandlock decides whether a plugin started now is confined with
// Landlock.
func useLandlock() bool {
	switch settingFromEnv(envLandlock) {
	case netnsRequired:
		return true
	case netnsOff:
		return false
	}
	return landlockUnavailable() == nil
}

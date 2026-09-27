// Package sandbox runs a host plugin in its own network namespace, so the
// egress proxy is its only way out: HTTP_PROXY can be ignored, a namespace
// with nothing but loopback cannot.
//
// The plugin host starts `WeKnora plugin-sandbox` in a new user and network
// namespace. The helper brings loopback up and opens listening sockets on the
// loopback ports the plugin is told about (the egress proxy, the Host API),
// hands them to the plugin host over a socket it inherited (a socket stays in
// the namespace it was made in, so the plugin host accepts the plugin's
// connections itself), and then becomes the plugin with execve: nothing but
// the plugin stays running in the namespace. Linux only.
package sandbox

import (
	"fmt"
	"strconv"
	"sync/atomic"
)

// Subcommand is the helper's name on WeKnora's command line.
const Subcommand = "plugin-sandbox"

// ExitSetupFailed is the helper's exit status when it could not set the
// sandbox up (typically: the system refuses unprivileged user namespaces
// the capabilities they need).
const ExitSetupFailed = 125

// probeFlag asks the helper only to set the sandbox up, with no plugin to
// run: whether it can tells the plugin host if this system sandboxes.
const probeFlag = "--probe"

// ProbeArgs are the helper's arguments for a probe.
func ProbeArgs() []string { return []string{Subcommand, probeFlag} }

var helper atomic.Bool

// RegisterHelper records that this program runs Main when started with
// Subcommand, as WeKnora's main does. The plugin host only probes for, and
// by default uses, a sandbox the program can start.
func RegisterHelper() { helper.Store(true) }

// HelperRegistered reports whether RegisterHelper was called.
func HelperRegistered() bool { return helper.Load() }

// PortsFD is the helper's file descriptor for the socket it hands its
// listening sockets over on (the plugin host's first ExtraFiles entry).
const PortsFD = 3

// Args builds the helper's arguments: the loopback ports to listen on, in
// the order their sockets are handed over, then the plugin's command, run
// by absolute path.
func Args(ports []int, command []string) []string {
	args := []string{Subcommand}
	for _, p := range ports {
		args = append(args, "--listen", strconv.Itoa(p))
	}
	return append(append(args, "--"), command...)
}

// parseArgs reads what Args wrote (without the subcommand).
func parseArgs(args []string) ([]int, []string, error) {
	var ports []int
	for len(args) > 0 {
		switch args[0] {
		case "--":
			if len(args) < 2 {
				return nil, nil, fmt.Errorf("no plugin command")
			}
			return ports, args[1:], nil
		case "--listen":
			if len(args) < 2 {
				return nil, nil, fmt.Errorf("--listen needs a port")
			}
			n, err := strconv.Atoi(args[1])
			if err != nil || n <= 0 || n > 65535 {
				return nil, nil, fmt.Errorf("bad port %q", args[1])
			}
			ports = append(ports, n)
			args = args[2:]
		default:
			return nil, nil, fmt.Errorf("unknown argument %q", args[0])
		}
	}
	return nil, nil, fmt.Errorf("no plugin command")
}

// Package sandbox runs a host plugin in its own network namespace, so the
// egress proxy is its only way out: HTTP_PROXY can be ignored, a namespace
// with nothing but loopback cannot.
//
// The plugin host starts `WeKnora plugin-sandbox` in a new user and network
// namespace. The helper brings loopback up, listens on the loopback ports the
// plugin is told about (the egress proxy, the Host API) and relays each
// connection to a unix socket of the plugin host (unix sockets are reached
// through the file system, not the network), then runs the plugin and waits
// for it. Linux only.
package sandbox

import (
	"fmt"
	"strconv"
	"strings"
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

// Relay forwards a loopback port inside the namespace to a unix socket
// outside it.
type Relay struct {
	Port   int
	Socket string
}

// Args builds the helper's arguments: relays, then the plugin's command.
func Args(relays []Relay, command []string) []string {
	args := []string{Subcommand}
	for _, r := range relays {
		args = append(args, "--relay", strconv.Itoa(r.Port)+"="+r.Socket)
	}
	return append(append(args, "--"), command...)
}

// parseArgs reads what Args wrote (without the subcommand).
func parseArgs(args []string) ([]Relay, []string, error) {
	var relays []Relay
	for len(args) > 0 {
		switch args[0] {
		case "--":
			if len(args) < 2 {
				return nil, nil, fmt.Errorf("no plugin command")
			}
			return relays, args[1:], nil
		case "--relay":
			if len(args) < 2 {
				return nil, nil, fmt.Errorf("--relay needs port=socket")
			}
			port, socket, ok := strings.Cut(args[1], "=")
			n, err := strconv.Atoi(port)
			if !ok || err != nil || n <= 0 || n > 65535 || socket == "" {
				return nil, nil, fmt.Errorf("bad relay %q", args[1])
			}
			relays = append(relays, Relay{Port: n, Socket: socket})
			args = args[2:]
		default:
			return nil, nil, fmt.Errorf("unknown argument %q", args[0])
		}
	}
	return nil, nil, fmt.Errorf("no plugin command")
}

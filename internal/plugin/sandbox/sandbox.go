// Package sandbox confines a host plugin before it runs: its own network
// namespace, so the egress proxy is its only way out (HTTP_PROXY can be
// ignored, a namespace with nothing but loopback cannot), and Landlock, so
// it reads only the system, its package and its own directory, although it
// runs as WeKnora's user.
//
// The plugin host starts `WeKnora plugin-sandbox`, in a new user and network
// namespace when the plugin gets one. The helper then brings loopback up and
// opens listening sockets on the loopback ports the plugin is told about (the
// egress proxy, the Host API), hands them to the plugin host over a socket it
// inherited (a socket stays in the namespace it was made in, so the plugin
// host accepts the plugin's connections itself). It restricts itself with
// Landlock, which what it runs keeps, and becomes the plugin with execve:
// nothing but the plugin stays running. Linux only.
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

// Spec is what the helper sets up before it becomes the plugin.
type Spec struct {
	// Netns: the helper runs in new user and network namespaces, brings
	// loopback up there and hands over listening sockets for Ports, in
	// order, on PortsFD.
	Netns bool
	Ports []int
	// Landlock restricts the plugin to Read (read and run) and Write (read,
	// write and create, not run); paths that do not exist are left out.
	Landlock bool
	Read     []string
	Write    []string
	// LimitTCP also allows TCP connections to ConnectPorts only, when the
	// kernel's Landlock can (Linux 6.7); outside a network namespace.
	LimitTCP     bool
	ConnectPorts []int
}

// Args builds the helper's arguments: what to set up, then the plugin's
// command, run by absolute path.
func Args(s Spec, command []string) []string {
	args := []string{Subcommand}
	if s.Netns {
		args = append(args, "--netns")
	}
	for _, p := range s.Ports {
		args = append(args, "--listen", strconv.Itoa(p))
	}
	if s.Landlock {
		args = append(args, "--landlock")
	}
	for _, p := range s.Read {
		args = append(args, "--read", p)
	}
	for _, p := range s.Write {
		args = append(args, "--write", p)
	}
	if s.LimitTCP {
		args = append(args, "--limit-tcp")
	}
	for _, p := range s.ConnectPorts {
		args = append(args, "--connect", strconv.Itoa(p))
	}
	return append(append(args, "--"), command...)
}

// parseArgs reads what Args wrote (without the subcommand).
func parseArgs(args []string) (Spec, []string, error) {
	var s Spec
	port := func(v string) (int, error) {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 65535 {
			return 0, fmt.Errorf("bad port %q", v)
		}
		return n, nil
	}
	for len(args) > 0 {
		flag := args[0]
		switch flag {
		case "--":
			if len(args) < 2 {
				return s, nil, fmt.Errorf("no plugin command")
			}
			return s, args[1:], nil
		case "--netns":
			s.Netns = true
			args = args[1:]
			continue
		case "--landlock":
			s.Landlock = true
			args = args[1:]
			continue
		case "--limit-tcp":
			s.LimitTCP = true
			args = args[1:]
			continue
		case "--listen", "--connect", "--read", "--write":
		default:
			return s, nil, fmt.Errorf("unknown argument %q", flag)
		}
		if len(args) < 2 {
			return s, nil, fmt.Errorf("%s needs a value", flag)
		}
		v := args[1]
		args = args[2:]
		switch flag {
		case "--listen", "--connect":
			n, err := port(v)
			if err != nil {
				return s, nil, err
			}
			if flag == "--listen" {
				s.Ports = append(s.Ports, n)
			} else {
				s.ConnectPorts = append(s.ConnectPorts, n)
			}
		case "--read":
			s.Read = append(s.Read, v)
		case "--write":
			s.Write = append(s.Write, v)
		}
	}
	return s, nil, fmt.Errorf("no plugin command")
}

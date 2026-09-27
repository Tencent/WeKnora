//go:build linux

package sandbox

import (
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// Main is the helper, already inside the namespaces. It waits for the plugin
// host to say "go" on stdin (after resource limits are on the helper, so the
// plugin, which it becomes, keeps them), brings loopback up, hands the plugin
// host a listening socket for each port on PortsFD, and execs the plugin.
// It returns only if it could not.
func Main(args []string) int {
	if len(args) == 1 && args[0] == probeFlag {
		return probe()
	}
	ports, command, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plugin-sandbox:", err)
		return 2
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(os.Stdin, buf); err != nil {
		fmt.Fprintln(os.Stderr, "plugin-sandbox: no go-ahead from the plugin host:", err)
		return 2
	}
	if err := loopbackUp(); err != nil {
		fmt.Fprintln(os.Stderr, "plugin-sandbox: bring up loopback in the plugin's network namespace:", err)
		return ExitSetupFailed
	}
	fds := make([]int, 0, len(ports))
	for _, port := range ports {
		fd, err := listenLoopback(port)
		if err != nil {
			fmt.Fprintf(os.Stderr, "plugin-sandbox: listen on port %d: %v\n", port, err)
			return ExitSetupFailed
		}
		fds = append(fds, fd)
	}
	if err := unix.Sendmsg(PortsFD, []byte{'p'}, unix.UnixRights(fds...), nil, 0); err != nil {
		fmt.Fprintln(os.Stderr, "plugin-sandbox: hand the listening sockets to the plugin host:", err)
		return ExitSetupFailed
	}
	// The plugin host holds them now; the plugin gets none of these, and
	// no stdin (the go-ahead pipe is done).
	for _, fd := range fds {
		_ = unix.Close(fd)
	}
	_ = unix.Close(PortsFD)
	if null, err := unix.Open(os.DevNull, unix.O_RDONLY, 0); err == nil {
		_ = unix.Dup2(null, 0)
		_ = unix.Close(null)
	}
	err = unix.Exec(command[0], command, os.Environ())
	fmt.Fprintf(os.Stderr, "plugin-sandbox: run %s: %v\n", command[0], err)
	return 127
}

// listenLoopback opens a listening TCP socket on 127.0.0.1:port.
func listenLoopback(port int) (int, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	addr := &unix.SockaddrInet4{Port: port, Addr: [4]byte{127, 0, 0, 1}}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if err := unix.Listen(fd, unix.SOMAXCONN); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

// probe sets the sandbox up as for a plugin, loopback and a listener on it,
// and exits.
func probe() int {
	if err := loopbackUp(); err != nil {
		fmt.Fprintln(os.Stderr, "plugin-sandbox: bring up loopback in a new network namespace:", err)
		return ExitSetupFailed
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "plugin-sandbox: listen on loopback in a new network namespace:", err)
		return ExitSetupFailed
	}
	_ = ln.Close()
	return 0
}

// loopbackUp brings up lo, which starts down in a new network namespace.
func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
		return err
	}
	ifr.SetUint16(ifr.Uint16() | unix.IFF_UP)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
}

// Supported reports whether this system can run plugins in a sandbox.
func Supported() bool { return true }

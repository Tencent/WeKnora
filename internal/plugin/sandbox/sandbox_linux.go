//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// Main is the helper, already inside the namespaces. It waits for the plugin
// host to say "go" on stdin (after resource limits are on the helper, so the
// plugin inherits them), sets up loopback and the relays, runs the plugin
// and exits with its status.
func Main(args []string) int {
	if len(args) == 1 && args[0] == probeFlag {
		return probe()
	}
	relays, command, err := parseArgs(args)
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
	for _, r := range relays {
		if err := relay(r); err != nil {
			fmt.Fprintln(os.Stderr, "plugin-sandbox:", err)
			return ExitSetupFailed
		}
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	// The plugin host signals the helper to stop the plugin: pass it on, so
	// the plugin drains its calls, and wait for it to exit.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	err = cmd.Start()
	if err == nil {
		go func() {
			for s := range sigs {
				_ = cmd.Process.Signal(s)
			}
		}()
		err = cmd.Wait()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "plugin-sandbox:", err)
		return 2
	}
	return 0
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

func relay(r Relay) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", r.Port))
	if err != nil {
		return fmt.Errorf("listen on port %d: %w", r.Port, err)
	}
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = in.Close() }()
				out, err := net.Dial("unix", r.Socket)
				if err != nil {
					return
				}
				defer func() { _ = out.Close() }()
				pipe(in, out)
			}()
		}
	}()
	return nil
}

// pipe copies both ways until either side is done.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
}

// Supported reports whether this system can run plugins in a sandbox.
func Supported() bool { return true }

//go:build linux

package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Tencent/WeKnora/internal/plugin/sandbox"
)

// sandboxed is a plugin command wrapped to run in a network namespace.
type sandboxed struct {
	cmd *exec.Cmd
	// start tells the helper to go on, once limits apply to it, and takes
	// the listening sockets it hands over.
	start func() error
	// release closes the listeners once the plugin exited.
	release func()
}

// portsTimeout bounds the wait for the helper's listening sockets.
const portsTimeout = 10 * time.Second

// sandbox wraps cmd so the plugin runs in its own user and network
// namespace. The helper opens the loopback ports the plugin is told about
// in there, hands the listening sockets over and becomes the plugin; this
// host accepts on them: the egress proxy is served on its port, and Host
// API connections are forwarded to this node's Host API.
func (p *process) sandbox(cmd *exec.Cmd) (*sandboxed, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var closers []io.Closer
	release := func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}
	proxyPort := p.proxy.ln.Addr().(*net.TCPAddr).Port
	ports := []int{proxyPort}
	hostAPI := p.spec.hostAPI
	if hostAPI != "" {
		_, port, err := net.SplitHostPort(hostAPI)
		n, _ := strconv.Atoi(port)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("sandbox: bad Host API address %q", hostAPI)
		}
		ports = append(ports, n)
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	ours := os.NewFile(uintptr(pair[0]), "sandbox-ports")
	theirs := os.NewFile(uintptr(pair[1]), "sandbox-ports")
	// Theirs is closed once the helper has its copy; closing it again on
	// release (a helper that never started) is harmless.
	closers = append(closers, ours, theirs)

	wrapped := exec.Command(self, sandbox.Args(ports, append([]string{cmd.Path}, cmd.Args[1:]...))...)
	wrapped.Dir, wrapped.Env = cmd.Dir, cmd.Env
	wrapped.SysProcAttr = namespaceAttr()
	wrapped.ExtraFiles = []*os.File{theirs} // sandbox.PortsFD
	stdin, err := wrapped.StdinPipe()
	if err != nil {
		_ = theirs.Close()
		release()
		return nil, err
	}
	return &sandboxed{
		cmd: wrapped,
		start: func() error {
			// The helper has its copy since it started.
			_ = theirs.Close()
			_, err := stdin.Write([]byte{'g'})
			_ = stdin.Close()
			if err != nil {
				return err
			}
			lns, err := receiveListeners(ours, len(ports))
			if err != nil {
				return fmt.Errorf("the plugin sandbox did not hand over its ports: %w", err)
			}
			for _, ln := range lns {
				closers = append(closers, ln)
			}
			proxySrv := &http.Server{Handler: p.proxy, ReadHeaderTimeout: 30 * time.Second}
			closers = append(closers, proxySrv)
			go func() { _ = proxySrv.Serve(lns[0]) }()
			if hostAPI != "" {
				go forwardTo(lns[1], hostAPI)
			}
			return nil
		},
		release: release,
	}, nil
}

// receiveListeners takes n listening sockets the helper sends on conn.
func receiveListeners(f *os.File, n int) ([]net.Listener, error) {
	c, err := net.FileConn(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("not a unix socket: %T", c)
	}
	_ = uc.SetReadDeadline(time.Now().Add(portsTimeout))
	oob := make([]byte, unix.CmsgSpace(4*n))
	_, oobn, _, _, err := uc.ReadMsgUnix(make([]byte, 1), oob)
	if err != nil {
		return nil, err
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) != 1 {
		return nil, fmt.Errorf("no sockets in the message (%v)", err)
	}
	fds, err := unix.ParseUnixRights(&msgs[0])
	if err != nil {
		return nil, err
	}
	var lns []net.Listener
	for _, fd := range fds {
		file := os.NewFile(uintptr(fd), "sandbox-port")
		ln, err := net.FileListener(file) // dups fd
		_ = file.Close()
		if err != nil {
			for _, l := range lns {
				_ = l.Close()
			}
			return nil, err
		}
		lns = append(lns, ln)
	}
	if len(lns) != n {
		for _, l := range lns {
			_ = l.Close()
		}
		return nil, fmt.Errorf("got %d sockets, want %d", len(lns), n)
	}
	return lns, nil
}

// sandboxOS: network namespaces are Linux's.
const sandboxOS = true

// namespaceAttr starts the helper in a new user and network namespace.
func namespaceAttr() *syscall.SysProcAttr {
	uid, gid := os.Getuid(), os.Getgid()
	return &syscall.SysProcAttr{
		Setpgid: true, Pdeathsig: syscall.SIGKILL,
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
		// Root in its own user namespace only: enough to bring loopback
		// up, no power outside.
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: uid, Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: gid, Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
}

// probeTimeout bounds the sandbox probe.
const probeTimeout = 10 * time.Second

// probeSandbox starts the sandbox helper the way a plugin is started, with
// nothing to run: whether it gets its namespaces and loopback up tells
// whether this system sandboxes plugins.
func probeSandbox() error {
	if !sandbox.HelperRegistered() {
		return errNoHelper
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, sandbox.ProbeArgs()...)
	cmd.Env = []string{}
	cmd.SysProcAttr = namespaceAttr()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("the system refuses to create a user namespace (%w)", err)
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exit) && exit.ExitCode() == sandbox.ExitSetupFailed:
		return fmt.Errorf("a user namespace lacks the rights to set up its network (%s)",
			strings.TrimSpace(out.String()))
	case ctx.Err() != nil:
		return fmt.Errorf("the sandbox probe did not finish within %s", probeTimeout)
	default:
		return fmt.Errorf("the sandbox probe failed: %v %s", err, strings.TrimSpace(out.String()))
	}
}

// forwardTo relays connections from ln to a TCP address.
func forwardTo(ln net.Listener, addr string) {
	for {
		in, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = in.Close() }()
			out, err := net.DialTimeout("tcp", addr, 10*time.Second)
			if err != nil {
				return
			}
			defer func() { _ = out.Close() }()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(out, in); done <- struct{}{} }()
			go func() { _, _ = io.Copy(in, out); done <- struct{}{} }()
			<-done
		}()
	}
}

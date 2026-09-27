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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Tencent/WeKnora/internal/plugin/sandbox"
)

// sandboxed is a plugin command wrapped to run in the sandbox helper.
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

// sandbox wraps cmd so the sandbox helper confines the plugin before it
// becomes it. In a network namespace (p.sandboxed), the helper opens the
// loopback ports the plugin is told about in there and hands the listening
// sockets over; this host accepts on them: the egress proxy is served on its
// port, and Host API connections are forwarded to this node's Host API.
// With Landlock (p.landlocked), it keeps the plugin to its files.
func (p *process) sandbox(cmd *exec.Cmd) (*sandboxed, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	spec := sandbox.Spec{Netns: p.sandboxed}
	if p.landlocked {
		p.confine(&spec, cmd.Path)
	}
	var closers []io.Closer
	release := func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}
	hostAPI := p.spec.hostAPI
	var ours, theirs *os.File
	if spec.Netns {
		spec.Ports = []int{p.proxy.ln.Addr().(*net.TCPAddr).Port}
		if hostAPI != "" {
			_, port, err := net.SplitHostPort(hostAPI)
			n, _ := strconv.Atoi(port)
			if err != nil || n == 0 {
				return nil, fmt.Errorf("sandbox: bad Host API address %q", hostAPI)
			}
			spec.Ports = append(spec.Ports, n)
		}
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			return nil, fmt.Errorf("sandbox: %w", err)
		}
		ours = os.NewFile(uintptr(pair[0]), "sandbox-ports")
		theirs = os.NewFile(uintptr(pair[1]), "sandbox-ports")
		// Theirs is closed once the helper has its copy; closing it again on
		// release (a helper that never started) is harmless.
		closers = append(closers, ours, theirs)
	}

	wrapped := exec.Command(self, sandbox.Args(spec, append([]string{cmd.Path}, cmd.Args[1:]...))...)
	wrapped.Dir, wrapped.Env = cmd.Dir, cmd.Env
	wrapped.SysProcAttr = cmd.SysProcAttr
	if spec.Netns {
		wrapped.SysProcAttr = namespaceAttr()
		wrapped.ExtraFiles = []*os.File{theirs} // sandbox.PortsFD
	}
	stdin, err := wrapped.StdinPipe()
	if err != nil {
		release()
		return nil, err
	}
	return &sandboxed{
		cmd: wrapped,
		start: func() error {
			if theirs != nil {
				// The helper has its copy since it started.
				_ = theirs.Close()
			}
			_, err := stdin.Write([]byte{'g'})
			_ = stdin.Close()
			if err != nil || !spec.Netns {
				return err
			}
			lns, err := receiveListeners(ours, len(spec.Ports))
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

// landlockRead are the system's files every plugin reads (and runs):
// programs, libraries, settings and certificates; /sys; of /proc the
// plugin's own entry (the helper's, which becomes the plugin) and what
// libraries read about the machine, no other process's.
var landlockRead = []string{
	"/usr", "/lib", "/lib32", "/lib64", "/libx32", "/bin", "/sbin", "/etc", "/sys",
	"/proc/self", "/proc/cpuinfo", "/proc/meminfo", "/proc/stat", "/proc/loadavg", "/proc/uptime",
	"/proc/version", "/proc/filesystems", "/proc/sys/kernel", "/proc/sys/vm",
}

// landlockWrite are the devices every plugin writes, and POSIX shared
// memory (Python's multiprocessing).
var landlockWrite = []string{
	"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/shm",
}

// confine lists what Landlock leaves the plugin: the system's files, its
// package and its interpreter's installation to read and run; its own
// directory (HOME, TMPDIR and its socket) to write. Not WeKnora's data or
// config, nor other plugins' files, although it runs as WeKnora's user.
// Outside a network namespace it may also only connect over TCP to the
// egress proxy, the Host API and the direct hosts' ports.
func (p *process) confine(spec *sandbox.Spec, command string) {
	spec.Landlock = true
	spec.Read = append([]string{p.spec.dir}, landlockRead...)
	if p.spec.m.Runtime.Kind == KindPython {
		spec.Read = append(spec.Read, interpreterPrefixes(command)...)
	}
	spec.Write = append([]string{p.sockDir}, landlockWrite...)
	if !p.limitsTCP() {
		return
	}
	spec.LimitTCP = true
	spec.ConnectPorts = []int{p.proxy.ln.Addr().(*net.TCPAddr).Port}
	for _, addr := range append([]string{p.spec.hostAPI}, p.spec.direct...) {
		if _, port, err := net.SplitHostPort(addr); err == nil {
			if n, err := strconv.Atoi(port); err == nil && n > 0 {
				spec.ConnectPorts = append(spec.ConnectPorts, n)
			}
		}
	}
}

// interpreterPrefixes are the installations an interpreter runs from: the
// directory above its bin, for the path it is started by (a virtualenv)
// and for the file that is (the base installation). The root never counts.
func interpreterPrefixes(command string) []string {
	var out []string
	paths := []string{command}
	if real, err := filepath.EvalSymlinks(command); err == nil && real != command {
		paths = append(paths, real)
	}
	for _, p := range paths {
		if prefix := filepath.Dir(filepath.Dir(p)); prefix != "/" && prefix != "." {
			out = append(out, prefix)
		}
	}
	return out
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

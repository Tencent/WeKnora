//go:build linux

package host

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/sandbox"
)

// sandboxed is a plugin command wrapped to run in a network namespace.
type sandboxed struct {
	cmd *exec.Cmd
	// start tells the helper to go on, once limits apply to it.
	start func() error
	// release closes the relays once the plugin exited.
	release func()
}

// sandbox wraps cmd so the plugin runs in its own user and network
// namespace, reaching the egress proxy and the Host API through relays.
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
	proxySock := filepath.Join(p.sockDir, "proxy.sock")
	_ = os.Remove(proxySock)
	proxyLn, err := net.Listen("unix", proxySock)
	if err != nil {
		return nil, fmt.Errorf("sandbox proxy relay: %w", err)
	}
	proxySrv := &http.Server{Handler: p.proxy, ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = proxySrv.Serve(proxyLn) }()
	closers = append(closers, proxySrv)
	relays := []sandbox.Relay{{Port: proxyPort, Socket: proxySock}}

	if addr := p.spec.hostAPI; addr != "" {
		_, port, err := net.SplitHostPort(addr)
		n, _ := strconv.Atoi(port)
		if err != nil || n == 0 {
			release()
			return nil, fmt.Errorf("sandbox: bad Host API address %q", addr)
		}
		sock := filepath.Join(p.sockDir, "hostapi.sock")
		_ = os.Remove(sock)
		ln, err := net.Listen("unix", sock)
		if err != nil {
			release()
			return nil, fmt.Errorf("sandbox Host API relay: %w", err)
		}
		closers = append(closers, ln)
		go forwardTo(ln, addr)
		relays = append(relays, sandbox.Relay{Port: n, Socket: sock})
	}

	wrapped := exec.Command(self, sandbox.Args(relays, append([]string{cmd.Path}, cmd.Args[1:]...))...)
	wrapped.Dir, wrapped.Env = cmd.Dir, cmd.Env
	uid, gid := os.Getuid(), os.Getgid()
	wrapped.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, Pdeathsig: syscall.SIGKILL,
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
		// Root in its own user namespace only: enough to bring loopback
		// up, no power outside.
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: uid, Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: gid, Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
	stdin, err := wrapped.StdinPipe()
	if err != nil {
		release()
		return nil, err
	}
	return &sandboxed{
		cmd: wrapped,
		start: func() error {
			_, err := stdin.Write([]byte{'g'})
			_ = stdin.Close()
			return err
		},
		release: release,
	}, nil
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

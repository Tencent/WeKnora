package host

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Timing of a host plugin's life. Variables so tests can shrink them.
var (
	handshakeTimeout      = 30 * time.Second
	healthInterval        = 15 * time.Second
	healthTimeout         = 5 * time.Second
	unhealthyLimit        = 3
	stopGrace             = 10 * time.Second
	restartBackoffFloor   = time.Second
	restartBackoffCeiling = time.Minute
)

// State of a supervised process.
type State string

// Process states reported to the reconciler.
const (
	StateStarting State = "starting"
	StateReady    State = "ready"
	StateDegraded State = "degraded"
	StateStopped  State = "stopped"
)

// spec is everything needed to start one plugin version.
type spec struct {
	m   *manifest.Manifest
	dir string // extracted package
}

// entryPath resolves runtime.entry for this machine inside the package.
func entryPath(m *manifest.Manifest, dir string) (string, error) {
	rel := strings.NewReplacer("{os}", runtime.GOOS, "{arch}", runtime.GOARCH).Replace(m.Runtime.Entry)
	if runtime.GOOS == "windows" && filepath.Ext(rel) == "" {
		rel += ".exe"
	}
	p, err := utils.SafeJoinUnderBase(dir, rel)
	if err != nil {
		return "", fmt.Errorf("runtime.entry %q: %w", m.Runtime.Entry, err)
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("the package has no %s build (%s)", runtime.GOOS+"/"+runtime.GOARCH, rel)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("runtime.entry %s is not a file", rel)
	}
	// Packages are extracted without modes; the entry has to be executable.
	if err := os.Chmod(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// process is one running plugin version and its supervisor.
type process struct {
	spec    spec
	onState func(State, error)

	mu      sync.RWMutex
	client  *client.Client
	state   State
	lastErr error

	cancel  context.CancelFunc
	done    chan struct{}
	proxy   *egressProxy
	sockDir string
}

// launched is one started child.
type launched struct {
	cmd    *exec.Cmd
	client *client.Client
	exited chan error
}

// startProcess starts the plugin, waits until it is ready and keeps it
// running until stop. It returns once the first start succeeded or failed.
func startProcess(sp spec, onState func(State, error)) (*process, error) {
	entry, err := entryPath(sp.m, sp.dir)
	if err != nil {
		return nil, err
	}
	sockDir, err := socketDir()
	if err != nil {
		return nil, err
	}
	proxy, err := startEgressProxy(sp.m.ID, sp.m.Permissions.Egress, func(f string, a ...any) {
		logger.Warnf(context.Background(), f, a...)
	})
	if err != nil {
		_ = os.RemoveAll(sockDir)
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &process{spec: sp, onState: onState, cancel: cancel, done: make(chan struct{}), proxy: proxy, sockDir: sockDir}
	p.setState(StateStarting, nil)
	first, err := p.launch(ctx, entry)
	if err != nil {
		cancel()
		proxy.Close()
		_ = os.RemoveAll(sockDir)
		close(p.done)
		p.setState(StateStopped, err)
		return nil, err
	}
	// Ready before Activate returns: callers route to the plugin right away.
	p.setClient(first.client)
	p.setState(StateReady, nil)
	go p.supervise(ctx, entry, first)
	return p, nil
}

// socketDir makes a short private directory for the socket: unix socket
// paths are limited to about 100 bytes.
func socketDir() (string, error) {
	base := os.Getenv("WEKNORA_PLUGIN_SOCKET_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return os.MkdirTemp(base, "wkp")
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// childEnv is the plugin's whole environment. It is built from scratch so
// WeKnora's own secrets (database, AES key, vendor keys) never reach plugin
// code.
func (p *process) childEnv(socket, token string) []string {
	env := []string{
		pluginapi.EnvSocket + "=" + socket,
		pluginapi.EnvNetwork + "=unix",
		pluginapi.EnvToken + "=" + token,
		pluginapi.EnvPluginID + "=" + p.spec.m.ID,
		pluginapi.EnvPluginVersion + "=" + p.spec.m.Version,
		"HOME=" + p.sockDir,
		"TMPDIR=" + p.sockDir,
		"HTTP_PROXY=" + p.proxy.URL(),
		"HTTPS_PROXY=" + p.proxy.URL(),
		"http_proxy=" + p.proxy.URL(),
		"https_proxy=" + p.proxy.URL(),
		"NO_PROXY=",
		"no_proxy=",
	}
	for _, k := range []string{"PATH", "LANG", "LC_ALL", "TZ", "SYSTEMROOT", "WINDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// launch starts the child and waits for handshake, health and a manifest
// that matches the installed package.
func (p *process) launch(ctx context.Context, entry string) (*launched, error) {
	socket := filepath.Join(p.sockDir, "p.sock")
	_ = os.Remove(socket)
	token := randomToken()
	cmd := exec.Command(entry)
	cmd.Dir = p.spec.dir
	cmd.Env = p.childEnv(socket, token)
	configureChild(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", filepath.Base(entry), err)
	}
	id := p.spec.m.ID + "@" + p.spec.m.Version
	handshake := make(chan pluginapi.Handshake, 1)
	hsErr := make(chan error, 1)
	go forwardLogs(stderr, id, nil, nil)
	go forwardLogs(stdout, id, handshake, hsErr)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	kill := func() {
		stopChild(cmd, exited)
	}
	timer := time.NewTimer(handshakeTimeout)
	defer timer.Stop()
	var hs pluginapi.Handshake
	select {
	case hs = <-handshake:
	case err := <-hsErr:
		kill()
		return nil, err
	case err := <-exited:
		exited <- err // keep it for supervise
		return nil, fmt.Errorf("plugin exited before it was ready: %v", err)
	case <-timer.C:
		kill()
		return nil, fmt.Errorf("plugin did not print its handshake within %s", handshakeTimeout)
	case <-ctx.Done():
		kill()
		return nil, ctx.Err()
	}
	if hs.Network == "unix" && hs.Address != socket {
		kill()
		return nil, fmt.Errorf("plugin listens on %s, not the socket it was given", hs.Address)
	}
	c := client.ForHandshake(hs, client.Bearer(token))
	cctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	if err := c.Health(cctx); err != nil {
		c.Close()
		kill()
		return nil, fmt.Errorf("plugin is not healthy: %w", err)
	}
	m, err := c.Manifest(cctx)
	if err != nil {
		c.Close()
		kill()
		return nil, fmt.Errorf("read plugin manifest: %w", err)
	}
	if err := checkServedManifest(p.spec.m, m); err != nil {
		c.Close()
		kill()
		return nil, err
	}
	return &launched{cmd: cmd, client: c, exited: exited}, nil
}

// checkServedManifest makes sure the process is the package that was
// installed and serves what the manifest promised.
func checkServedManifest(want *manifest.Manifest, got *pluginapi.Manifest) error {
	if got.ID != want.ID || got.Version != want.Version {
		return fmt.Errorf("process reports %s@%s, the package is %s@%s", got.ID, got.Version, want.ID, want.Version)
	}
	if got.APIVersion != pluginapi.APIVersion {
		return fmt.Errorf("process speaks %q, this WeKnora speaks %q", got.APIVersion, pluginapi.APIVersion)
	}
	for point, contribs := range want.Contributes {
		if info, ok := manifest.LookupPoint(point); !ok || info.Declarative {
			continue
		}
		served := map[string]bool{}
		for _, id := range got.Contributes[string(point)] {
			served[id] = true
		}
		for _, c := range contribs {
			if !served[c.ID] {
				return fmt.Errorf("plugin.yaml declares %s/%s but the process does not serve it", point, c.ID)
			}
		}
	}
	return nil
}

// forwardLogs copies a child's output into WeKnora's logs. On stdout it
// also watches for the handshake line.
func forwardLogs(r io.Reader, id string, handshake chan<- pluginapi.Handshake, hsErr chan<- error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	seen := false
	for sc.Scan() {
		line := sc.Text()
		if handshake != nil && !seen {
			hs, ok, err := pluginapi.ParseHandshake(line)
			if ok {
				seen = true
				if err != nil {
					hsErr <- err
				} else {
					handshake <- hs
				}
				continue
			}
		}
		logger.Infof(context.Background(), "[plugin %s] %s", id, line)
	}
}

func (p *process) setState(s State, err error) {
	p.mu.Lock()
	changed := p.state != s || (err != nil) != (p.lastErr != nil)
	p.state, p.lastErr = s, err
	p.mu.Unlock()
	if changed && p.onState != nil {
		p.onState(s, err)
	}
}

func (p *process) setClient(c *client.Client) {
	p.mu.Lock()
	old := p.client
	p.client = c
	p.mu.Unlock()
	if old != nil && old != c {
		old.Close()
	}
}

// Client returns the client of the running child, or an unavailable error.
func (p *process) Client() (*client.Client, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.client == nil || p.state != StateReady {
		msg := fmt.Sprintf("plugin %s is %s", p.spec.m.ID, p.state)
		if p.lastErr != nil {
			msg += ": " + p.lastErr.Error()
		}
		return nil, &pluginapi.Error{Code: pluginapi.CodeUnavailable, Message: msg, Retryable: true}
	}
	return p.client, nil
}

// supervise keeps the plugin running: health checks, restart with backoff
// after a crash or repeated failed checks.
func (p *process) supervise(ctx context.Context, entry string, cur *launched) {
	defer close(p.done)
	backoff := restartBackoffFloor
	for {
		p.setClient(cur.client)
		p.setState(StateReady, nil)
		exitErr := p.watch(ctx, cur)
		p.setClient(nil)
		if ctx.Err() != nil {
			p.setState(StateStopped, nil)
			return
		}
		logger.Warnf(ctx, "[plugin] %s stopped unexpectedly: %v; restarting in %s", p.spec.m.ID, exitErr, backoff)
		p.setState(StateDegraded, exitErr)
		for {
			select {
			case <-ctx.Done():
				p.setState(StateStopped, nil)
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, restartBackoffCeiling)
			next, err := p.launch(ctx, entry)
			if err == nil {
				cur = next
				backoff = restartBackoffFloor
				logger.Infof(ctx, "[plugin] %s restarted", p.spec.m.ID)
				break
			}
			if ctx.Err() != nil {
				p.setState(StateStopped, nil)
				return
			}
			logger.Warnf(ctx, "[plugin] %s restart failed: %v; next try in %s", p.spec.m.ID, err, backoff)
			p.setState(StateDegraded, err)
		}
	}
}

// watch returns when the child exits, fails its health checks, or ctx ends
// (then it stops the child).
func (p *process) watch(ctx context.Context, cur *launched) error {
	t := time.NewTicker(healthInterval)
	defer t.Stop()
	failures := 0
	for {
		select {
		case err := <-cur.exited:
			if err == nil {
				err = errors.New("exited")
			}
			return err
		case <-ctx.Done():
			stopChild(cur.cmd, cur.exited)
			return nil
		case <-t.C:
			hctx, cancel := context.WithTimeout(ctx, healthTimeout)
			err := cur.client.Health(hctx)
			cancel()
			if err == nil {
				failures = 0
				continue
			}
			failures++
			if failures >= unhealthyLimit {
				stopChild(cur.cmd, cur.exited)
				return fmt.Errorf("failed %d health checks: %w", failures, err)
			}
		}
	}
}

// stop ends the plugin and waits for it to exit.
func (p *process) stop() {
	p.cancel()
	<-p.done
	p.proxy.Close()
	_ = os.RemoveAll(p.sockDir)
}

// stopChild asks the child to exit, then kills it after stopGrace. exited
// is the channel its Wait result arrives on.
func stopChild(cmd *exec.Cmd, exited chan error) {
	if cmd.Process == nil {
		return
	}
	_ = terminate(cmd)
	select {
	case err := <-exited:
		exited <- err
	case <-time.After(stopGrace):
		_ = cmd.Process.Kill()
		err := <-exited
		exited <- err
	}
}

package host

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// envIdleTimeout is how long a host plugin may go without calls before the
// host stops it, e.g. "10m"; the next call starts it again. Unset or zero:
// plugins keep running. Plugins with runtime.keepAlive (or singleton) are
// never stopped for being idle.
const envIdleTimeout = "WEKNORA_PLUGIN_IDLE_TIMEOUT"

// IdleTimeoutFromEnv reads WEKNORA_PLUGIN_IDLE_TIMEOUT; 0 when unset or
// not a positive duration.
func IdleTimeoutFromEnv() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(envIdleTimeout)))
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// errParked ends a watch when the host stops an idle plugin.
var errParked = errors.New("stopped while idle")

// activity is what a plugin's callers have been doing: calls in flight and
// when the last one started or ended. Health checks do not count.
type activity struct {
	inFlight atomic.Int64
	last     atomic.Int64 // unix nanoseconds
}

func newActivity() *activity {
	a := &activity{}
	a.touch()
	return a
}

func (a *activity) touch() { a.last.Store(time.Now().UnixNano()) }

func (a *activity) done() {
	a.inFlight.Add(-1)
	a.touch()
}

// idleSince reports how long the plugin has been without calls; false
// while one is in flight.
func (a *activity) idleSince(now time.Time) (time.Duration, bool) {
	if a.inFlight.Load() > 0 {
		return 0, false
	}
	return now.Sub(time.Unix(0, a.last.Load())), true
}

// countingTransport counts a plugin's calls from the request until its
// answer's body is closed, streams included.
type countingTransport struct {
	next http.RoundTripper
	a    *activity
}

// quietPaths are the host's own checks, which say nothing about use.
var quietPaths = map[string]bool{"/v1/health": true, "/v1/manifest": true}

func (t countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if quietPaths[req.URL.Path] {
		return t.next.RoundTrip(req)
	}
	t.a.inFlight.Add(1)
	t.a.touch()
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		t.a.done()
		return nil, err
	}
	resp.Body = &countedBody{ReadCloser: resp.Body, a: t.a}
	return resp, nil
}

// CloseIdleConnections lets client.Close reach the wrapped transport.
func (t countingTransport) CloseIdleConnections() {
	if c, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

type countedBody struct {
	io.ReadCloser
	a    *activity
	once sync.Once
}

func (b *countedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.a.done)
	return err
}

// processClient reaches a plugin process at the address its handshake
// names, counting its calls in a.
func processClient(hs pluginapi.Handshake, auth client.Auth, a *activity) *client.Client {
	if hs.Network == "unix" {
		tr := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", hs.Address)
			},
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
		}
		return client.New("http://plugin", &http.Client{Transport: countingTransport{tr, a}}, auth)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	return client.New("http://"+hs.Address, &http.Client{Transport: countingTransport{tr, a}}, auth)
}

// parkIfIdle stops the plugin if it has had no calls for timeout; the next
// Client call starts it again. It reports whether it did.
func (p *process) parkIfIdle(now time.Time, timeout time.Duration) bool {
	p.mu.Lock()
	idle, ok := p.activity.idleSince(now)
	if p.state != StateReady || !ok || idle < timeout {
		p.mu.Unlock()
		return false
	}
	// Callers from here on see the plugin idle and wake it; the ones
	// before touched the activity, so it would not be idle.
	c := p.client
	p.client = nil
	p.setStateLocked(StateIdle, nil)
	p.mu.Unlock()
	if p.onState != nil {
		p.onState(p, StateIdle, nil)
	}
	if c != nil {
		c.Close()
	}
	select {
	case p.park <- struct{}{}:
	default:
	}
	return true
}

// requestWake asks the supervisor to start an idle plugin.
func (p *process) requestWake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// parkIntended reports whether a park signal still stands: a plugin that
// crashed and restarted since is not idle any more.
func (p *process) parkIntended() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state == StateIdle
}

// idleReaper stops the idle plugins of a manager every tick until stop.
func (m *Manager) idleReaper(timeout time.Duration, stop <-chan struct{}) {
	tick := min(timeout/4, 30*time.Second)
	if tick <= 0 {
		tick = timeout
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			m.parkIdle(now, timeout)
		}
	}
}

// parkIdle stops the plugins idle for timeout, except resident ones.
func (m *Manager) parkIdle(now time.Time, timeout time.Duration) {
	m.mu.Lock()
	procs := make([]*process, 0, len(m.procs))
	for _, p := range m.procs {
		if !p.spec.m.Runtime.Resident() {
			procs = append(procs, p)
		}
	}
	m.mu.Unlock()
	for _, p := range procs {
		p.parkIfIdle(now, timeout)
	}
}

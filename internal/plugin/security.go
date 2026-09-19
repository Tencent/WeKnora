package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
)

type AuditEvent struct {
	PluginID    string    `json:"plugin_id"`
	Action      string    `json:"action"`
	Destination string    `json:"destination,omitempty"`
	Allowed     bool      `json:"allowed"`
	Reason      string    `json:"reason,omitempty"`
	Policy      string    `json:"policy,omitempty"`
	At          time.Time `json:"at"`
}

type AuditSink interface{ Record(AuditEvent) }

// LoggerAuditSink is the safe default for runtimes created by the loader. It
// ensures plugin stderr is consumed and structured audit records are retained
// in the host log stream even when no application-level durable sink has been
// injected. A deployment that needs tenant-scoped durable audit rows can
// replace Runtime.AuditSink with its own adapter.
type LoggerAuditSink struct{}

func (LoggerAuditSink) Record(event AuditEvent) {
	logger.Warnf(context.Background(), "[plugin-audit] plugin=%s action=%s destination=%s policy=%s allowed=%t reason=%s",
		event.PluginID, event.Action, event.Destination, event.Policy, event.Allowed, event.Reason)
}

type MemoryAuditSink struct {
	mu     sync.Mutex
	Events []AuditEvent
}

func (s *MemoryAuditSink) Record(event AuditEvent) {
	s.mu.Lock()
	s.Events = append(s.Events, event)
	s.mu.Unlock()
}
func (s *MemoryAuditSink) Snapshot() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AuditEvent(nil), s.Events...)
}

// NetworkGuard is the application-side policy used by SDK helpers. A runtime
// sandbox remains authoritative for arbitrary syscalls; this guard makes HTTP
// integrations fail closed and produces an auditable event for every attempt.
type NetworkGuard struct {
	PluginID  string
	Policy    NetworkPolicy
	Allowlist []string
	Audit     AuditSink
	Resolver  func(context.Context, string) ([]net.IP, error)
}

func (g *NetworkGuard) Check(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return g.block(rawURL, "invalid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return g.block(rawURL, "only http and https are allowed")
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if _, err := g.authorize(ctx, host, port); err != nil {
		return err
	}
	g.record(rawURL, true, "allowed")
	return nil
}

// authorize validates the host/port against policy, blocked-host and allowlist
// rules, then resolves and validates the IPs. It returns the authorized IPs so
// callers can dial them directly (resolve-once), closing the DNS-rebinding
// TOCTOU window where a hostile resolver returns a public IP to the check and a
// private IP to the dial.
func (g *NetworkGuard) authorize(ctx context.Context, host, port string) ([]net.IP, error) {
	dest := net.JoinHostPort(host, port)
	if g.Policy == NetworkNone {
		return nil, g.block(dest, "manifest network policy is none")
	}
	if pluginapi.IsForbiddenHost(host) {
		return nil, g.block(dest, "destination is loopback, private, link-local or metadata address")
	}
	if g.Policy == NetworkAllowlist && !pluginapi.MatchAllowlist(g.Allowlist, host, port) {
		return nil, g.block(dest, "destination is not in manifest allowlist")
	}
	resolver := g.Resolver
	if resolver == nil {
		resolver = func(resolveCtx context.Context, resolveHost string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(resolveCtx, "ip", resolveHost)
		}
	}
	addresses, err := resolver(ctx, host)
	if err != nil {
		return nil, g.block(dest, "DNS resolution failed")
	}
	for _, address := range addresses {
		if pluginapi.IsForbiddenIP(address) {
			return nil, g.block(dest, "DNS resolved to a private or link-local address")
		}
	}
	return addresses, nil
}

func (g *NetworkGuard) HTTPClient(ctx context.Context) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := g.authorize(dialCtx, host, port)
		if err != nil {
			return nil, err
		}
		// resolve-once: dial only the already-authorized IPs, never re-resolving
		// the hostname. TLS ServerName is still taken from the request URL by
		// http.Transport, so certificate validation is unaffected.
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		var lastErr error
		for _, ip := range ips {
			conn, dialErr := dialer.DialContext(dialCtx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no addresses resolved for %q", host)
	}
	return &http.Client{Transport: transport}
}

func (g *NetworkGuard) block(destination, reason string) error {
	g.record(destination, false, reason)
	return fmt.Errorf("plugin %q network access blocked: %s", g.PluginID, reason)
}
func (g *NetworkGuard) record(destination string, allowed bool, reason string) {
	if g.Audit != nil {
		g.Audit.Record(AuditEvent{PluginID: g.PluginID, Action: "network", Destination: destination, Allowed: allowed, Reason: reason, At: time.Now().UTC()})
	}
}

// ParseAuditLine parses a single stderr line emitted by the plugin SDK. It
// returns ok=false when the line is not an audit record, so callers pass it
// through as an ordinary log line. The host-supplied pluginID is authoritative:
// the plugin never gets to choose its own audit identity.
func ParseAuditLine(line, pluginID string) (AuditEvent, bool) {
	if !strings.HasPrefix(line, pluginapi.AuditPrefix) {
		return AuditEvent{}, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, pluginapi.AuditPrefix))
	var raw struct {
		Action      string `json:"action"`
		Destination string `json:"destination"`
		Policy      string `json:"policy"`
		Reason      string `json:"reason"`
		Timestamp   string `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return AuditEvent{}, false
	}
	at := time.Now().UTC()
	if raw.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw.Timestamp); err == nil {
			at = parsed
		}
	}
	return AuditEvent{
		PluginID:    pluginID,
		Action:      raw.Action,
		Destination: raw.Destination,
		Allowed:     false,
		Reason:      raw.Reason,
		Policy:      raw.Policy,
		At:          at,
	}, true
}

// ConsumePluginStderr reads the plugin stderr stream line by line, routing
// audit records to the sink and passing ordinary logs through to the host
// stderr. It is intended to run in its own goroutine; it returns when the pipe
// is closed (the process has exited).
func ConsumePluginStderr(pipe io.Reader, pluginID string, sink AuditSink) {
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if event, ok := ParseAuditLine(line, pluginID); ok {
			if sink != nil {
				sink.Record(event)
			}
			continue
		}
		_, _ = fmt.Fprintln(os.Stderr, line)
	}
}

// AsyncAuditSink wraps an AuditSink with a bounded queue and a background
// worker, so a slow sink (file/DB) never blocks the plugin stderr consumer.
// When the queue is full, events are dropped and counted rather than blocking
// the producer or growing memory without bound. Dropping is deliberate: audit
// output is best effort, whereas the security guarantee lives in the runtime
// sandbox and must never be back-pressured into the plugin.
type AsyncAuditSink struct {
	sink    AuditSink
	queue   chan AuditEvent
	mu      sync.Mutex
	dropped uint64
}

// NewAsyncAuditSink starts a background worker draining queue into sink.
func NewAsyncAuditSink(sink AuditSink, queueSize int) *AsyncAuditSink {
	if queueSize <= 0 {
		queueSize = 1024
	}
	a := &AsyncAuditSink{sink: sink, queue: make(chan AuditEvent, queueSize)}
	go a.worker()
	return a
}

// Record enqueues the event without blocking. If the queue is full the event
// is dropped and counted.
func (a *AsyncAuditSink) Record(event AuditEvent) {
	select {
	case a.queue <- event:
	default:
		a.mu.Lock()
		a.dropped++
		a.mu.Unlock()
	}
}

// Dropped returns the number of events dropped because the queue was full.
func (a *AsyncAuditSink) Dropped() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dropped
}

func (a *AsyncAuditSink) worker() {
	for event := range a.queue {
		if a.sink != nil {
			a.sink.Record(event)
		}
	}
}

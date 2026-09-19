package plugin

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNetworkGuardBlocksAndAuditsNoNetwork(t *testing.T) {
	audit := &MemoryAuditSink{}
	guard := &NetworkGuard{PluginID: "test", Policy: NetworkNone, Audit: audit}
	err := guard.Check(context.Background(), "https://example.com/api")
	require.Error(t, err)
	events := audit.Snapshot()
	require.Len(t, events, 1)
	require.False(t, events[0].Allowed)
}

func TestNetworkGuardBlocksPrivateResolution(t *testing.T) {
	audit := &MemoryAuditSink{}
	guard := &NetworkGuard{PluginID: "test", Policy: NetworkAllowlist, Allowlist: []string{"api.example.com"}, Audit: audit, Resolver: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.0.0.1")}, nil }}
	require.Error(t, guard.Check(context.Background(), "https://api.example.com"))
	require.False(t, audit.Snapshot()[0].Allowed)
}

func TestNetworkGuardAllowlist(t *testing.T) {
	guard := &NetworkGuard{PluginID: "test", Policy: NetworkAllowlist, Allowlist: []string{"api.example.com:443"}, Resolver: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil }}
	require.NoError(t, guard.Check(context.Background(), "https://api.example.com"))
	require.Error(t, guard.Check(context.Background(), "https://other.example.com"))
}

func TestParseAuditLine(t *testing.T) {
	line := `plugin_audit {"action":"network_denied","destination":"example.com:443","policy":"none","reason":"outbound network is disabled","timestamp":"2026-08-22T10:00:00Z"}`
	event, ok := ParseAuditLine(line, "my-plugin")
	require.True(t, ok)
	require.Equal(t, "my-plugin", event.PluginID)
	require.Equal(t, "network_denied", event.Action)
	require.Equal(t, "none", event.Policy)
	require.Equal(t, "example.com:443", event.Destination)
	require.False(t, event.Allowed)
}

func TestParseAuditLineIgnoresOrdinaryLog(t *testing.T) {
	_, ok := ParseAuditLine("2026-08-22 INFO something happened", "my-plugin")
	require.False(t, ok)
}

func TestParseAuditLineIgnoresMalformedJSON(t *testing.T) {
	_, ok := ParseAuditLine(`plugin_audit {not json`, "my-plugin")
	require.False(t, ok)
}

func TestConsumePluginStderr(t *testing.T) {
	input := "ordinary log line\n" +
		`plugin_audit {"action":"network_denied","destination":"example.com:443","policy":"none","reason":"x","timestamp":"2026-08-22T10:00:00Z"}` + "\n" +
		"another log\n"
	sink := &MemoryAuditSink{}
	ConsumePluginStderr(strings.NewReader(input), "my-plugin", sink)
	events := sink.Snapshot()
	require.Len(t, events, 1)
	require.Equal(t, "my-plugin", events[0].PluginID)
	require.Equal(t, "example.com:443", events[0].Destination)
}

type blockingSink struct{ release chan struct{} }

func (b *blockingSink) Record(AuditEvent) { <-b.release }

func TestAsyncAuditSinkDelivers(t *testing.T) {
	mem := &MemoryAuditSink{}
	async := NewAsyncAuditSink(mem, 10)
	for i := 0; i < 5; i++ {
		async.Record(AuditEvent{PluginID: "p"})
	}
	require.Eventually(t, func() bool {
		return len(mem.Snapshot()) == 5
	}, 2*time.Second, 10*time.Millisecond)
}

func TestAsyncAuditSinkDropsWhenFull(t *testing.T) {
	release := make(chan struct{})
	blocking := &blockingSink{release: release}
	async := NewAsyncAuditSink(blocking, 1)
	for i := 0; i < 10; i++ {
		async.Record(AuditEvent{})
	}
	require.Eventually(t, func() bool {
		return async.Dropped() > 0
	}, 2*time.Second, 10*time.Millisecond)
	close(release)
}

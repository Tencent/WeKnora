package host

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/driver"
)

// setSandboxCheck replaces the sandbox probe and forgets its outcome.
func setSandboxCheck(t *testing.T, check func() error) {
	t.Helper()
	old := checkSandbox
	reset := func() {
		sandboxCheck.once = sync.Once{}
		sandboxCheck.err = nil
	}
	reset()
	checkSandbox = check
	t.Cleanup(func() {
		checkSandbox = old
		reset()
	})
}

func TestNetnsFromEnv(t *testing.T) {
	for v, want := range map[string]netnsSetting{
		"": netnsAuto, "auto": netnsAuto, " AUTO ": netnsAuto, "bogus": netnsAuto,
		"1": netnsRequired, "true": netnsRequired, "yes": netnsRequired, "On": netnsRequired,
		"0": netnsOff, "false": netnsOff, "no": netnsOff, "off": netnsOff,
	} {
		t.Setenv(envNetns, v)
		if got := netnsFromEnv(); got != want {
			t.Errorf("%s=%q: %v, want %v", envNetns, v, got, want)
		}
	}
}

// Auto mode sandboxes when the probe passes, which it runs once; "1" and
// "0" do not ask.
func TestUseSandbox(t *testing.T) {
	probes := 0
	var result error
	setSandboxCheck(t, func() error { probes++; return result })

	t.Setenv(envNetns, "1")
	if !useSandbox() || probes != 0 {
		t.Fatalf("required: sandbox=%v probes=%d", useSandbox(), probes)
	}
	t.Setenv(envNetns, "0")
	if useSandbox() || probes != 0 {
		t.Fatalf("off: sandbox=%v probes=%d", useSandbox(), probes)
	}

	t.Setenv(envNetns, "")
	for range 2 {
		if !useSandbox() {
			t.Fatal("auto, probe passes: not sandboxed")
		}
	}
	if probes != 1 {
		t.Fatalf("auto, probe passes: probes=%d", probes)
	}

	setSandboxCheck(t, func() error { probes++; return errors.New("user namespaces are off") })
	probes = 0
	t.Setenv(envNetns, "auto")
	for range 2 {
		if useSandbox() {
			t.Fatal("auto, probe fails: sandboxed")
		}
	}
	if probes != 1 {
		t.Fatalf("auto, probe fails: probes=%d", probes)
	}
	// Required mode still tries, and fails the plugin, when the probe failed.
	t.Setenv(envNetns, "1")
	if !useSandbox() {
		t.Fatal("required mode gave up on the sandbox")
	}
}

// Where the sandbox is unavailable, auto mode runs plugins with the proxy
// only and says so in the node's report.
func TestAutoModeFallsBackToTheProxy(t *testing.T) {
	fastTimings(t)
	t.Setenv(envNetns, "")
	setSandboxCheck(t, func() error { return errors.New("user namespaces are off") })
	m := NewManager()
	defer m.Close()
	if got := m.Egress("acme.echo"); got != "" {
		t.Fatalf("egress before start = %q", got)
	}
	if err := m.Activate(context.Background(), install(t, "1.0.0", "")); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if got := m.Egress("acme.echo"); got != driver.EgressProxy {
		t.Fatalf("egress = %q", got)
	}
	if got, err := search(t, m, "hello"); err != nil || got != "hello" {
		t.Fatalf("search = %q, %v", got, err)
	}
	if err := m.Deactivate(context.Background(), "acme.echo"); err != nil {
		t.Fatal(err)
	}
	if got := m.Egress("acme.echo"); got != "" {
		t.Fatalf("egress after stop = %q", got)
	}
}

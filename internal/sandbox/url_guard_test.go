package sandbox

import (
	"errors"
	"net"
	"testing"
)

// denyPrivate is the secure default policy.
var denyPrivate = OutboundURLPolicy{AllowPrivate: false}

// allowPrivate is the self-hosted opt-in policy.
var allowPrivate = OutboundURLPolicy{AllowPrivate: true}

func TestPolicyRejectsAlwaysForbiddenTargets(t *testing.T) {
	// These must be rejected under BOTH policies: link-local carries the cloud
	// metadata service, and the rest are not routable to a sandbox.
	cases := []struct{ name, raw string }{
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/"},
		{"link local", "http://169.254.1.1"},
		{"unspecified", "http://0.0.0.0"},
		{"mdns suffix", "http://cube.local"},
		{"bad scheme file", "file:///etc/passwd"},
		{"bad scheme gopher", "gopher://evil"},
		{"empty", ""},
		{"no host", "http://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for label, policy := range map[string]OutboundURLPolicy{
				"deny-private":  denyPrivate,
				"allow-private": allowPrivate,
			} {
				err := policy.Validate(tc.raw)
				if err == nil {
					t.Fatalf("[%s] Validate(%q) = nil, want error", label, tc.raw)
				}
				if !errors.Is(err, ErrUnsafeOutboundURL) {
					t.Fatalf("[%s] Validate(%q) error = %v, want ErrUnsafeOutboundURL",
						label, tc.raw, err)
				}
			}
		})
	}
}

func TestPolicyRejectsPrivateTargetsByDefault(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:8080",
		"http://localhost:8080",
		"http://[::1]:8080",
		"http://10.0.0.5",
		"http://172.16.3.4",
		"http://192.168.1.1",
		"http://100.64.0.1",
	} {
		if err := denyPrivate.Validate(raw); err == nil {
			t.Fatalf("denyPrivate.Validate(%q) = nil, want error", raw)
		}
	}
}

func TestPolicyAllowsPrivateTargetsWhenOptedIn(t *testing.T) {
	// Self-hosted Cube listens on 127.0.0.1:33000 by default, so this opt-in
	// is what makes per-tenant Cube configuration possible at all.
	for _, raw := range []string{
		"http://127.0.0.1:33000",
		"http://localhost:33000",
		"http://10.0.0.5",
		"http://192.168.1.1:80",
	} {
		if err := allowPrivate.Validate(raw); err != nil {
			t.Fatalf("allowPrivate.Validate(%q) = %v, want nil", raw, err)
		}
	}
}

func TestPolicyAllowsPublicLiteralAddresses(t *testing.T) {
	// Literal addresses keep this hermetic: no DNS required.
	for _, raw := range []string{
		"http://203.0.113.10:8080",
		"https://203.0.113.10",
		"https://[2001:db8::1]:443/v1",
	} {
		if err := denyPrivate.Validate(raw); err != nil {
			t.Fatalf("denyPrivate.Validate(%q) = %v, want nil", raw, err)
		}
	}
}

func TestPolicyAllowsPublicHostname(t *testing.T) {
	const host = "api.e2b.dev"
	if _, err := net.LookupIP(host); err != nil {
		t.Skipf("no DNS available in this environment: %v", err)
	}
	if err := denyPrivate.Validate("https://" + host); err != nil {
		t.Fatalf("denyPrivate.Validate(%q) = %v, want nil", host, err)
	}
}

func TestPolicyValidateRejectsUnresolvableHost(t *testing.T) {
	// Fail closed: if we cannot verify where a host points, we refuse it. This
	// also gives the admin an early "that hostname does not exist" signal.
	err := denyPrivate.Validate("https://this-host-does-not-exist.invalid")
	if err == nil {
		t.Fatal("Validate on an unresolvable host = nil, want error")
	}
}

func TestPolicyDialControlMirrorsValidation(t *testing.T) {
	// The dialer must forbid exactly what validation forbids, otherwise a
	// saved config would fail mysteriously at first use.
	alwaysBlocked := []string{"169.254.169.254:80", "0.0.0.0:80"}
	privateOnly := []string{"127.0.0.1:8080", "10.1.2.3:443", "[::1]:8080"}

	for _, address := range alwaysBlocked {
		if err := denyPrivate.DialControl("tcp", address, nil); err == nil {
			t.Fatalf("denyPrivate.DialControl(%q) = nil, want error", address)
		}
		if err := allowPrivate.DialControl("tcp", address, nil); err == nil {
			t.Fatalf("allowPrivate.DialControl(%q) = nil, want error", address)
		}
	}
	for _, address := range privateOnly {
		if err := denyPrivate.DialControl("tcp", address, nil); err == nil {
			t.Fatalf("denyPrivate.DialControl(%q) = nil, want error", address)
		}
		if err := allowPrivate.DialControl("tcp", address, nil); err != nil {
			t.Fatalf("allowPrivate.DialControl(%q) = %v, want nil", address, err)
		}
	}
	for _, address := range []string{"203.0.113.10:443", "[2001:db8::1]:443"} {
		if err := denyPrivate.DialControl("tcp", address, nil); err != nil {
			t.Fatalf("denyPrivate.DialControl(%q) = %v, want nil", address, err)
		}
	}
}

func TestDefaultOutboundURLPolicyFailsClosed(t *testing.T) {
	if DefaultOutboundURLPolicy().AllowPrivate {
		t.Fatal("callers without a workspace config must fail closed")
	}
}

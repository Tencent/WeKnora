// Package sandbox: outbound URL guard for tenant-supplied endpoints.
//
// Tenants configure their own sandbox control-plane URLs and the server dials
// them. Without a guard that is a Server-Side Request Forgery primitive: the
// most damaging target is the cloud metadata service (169.254.169.254), which
// hands out instance credentials, followed by any internal service reachable
// from the application host.
//
// Defence is two-layered on purpose:
//
//   - Validate runs when a config is saved or probed, giving the admin an
//     immediate, readable error.
//   - DialControl runs at connect time on every request. This is the layer
//     that actually holds, because a hostname can resolve to a public address
//     during validation and to 169.254.169.254 at dial time (DNS rebinding).
//
// Self-hosted deployments complicate this, so private endpoints are an
// explicit field on each workspace config. Even when enabled, link-local
// ranges — including cloud metadata — stay blocked, and that holds for the
// IPv6 encodings that carry an IPv4 link-local address in their payload.
package sandbox

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"

	"github.com/Tencent/WeKnora/internal/ipclass"
)

// ErrUnsafeOutboundURL is returned for any endpoint that uses an unsupported
// scheme or resolves to an address the policy forbids.
var ErrUnsafeOutboundURL = errors.New("sandbox: unsafe outbound URL")

// OutboundURLPolicy decides which addresses tenant-supplied endpoints may
// resolve to.
type OutboundURLPolicy struct {
	// AllowPrivate permits loopback and RFC1918 addresses. It never permits
	// link-local (169.254.0.0/16), which carries the cloud metadata service.
	AllowPrivate bool
}

// DefaultOutboundURLPolicy is the fail-closed policy used by callers that do
// not carry a workspace configuration.
func DefaultOutboundURLPolicy() OutboundURLPolicy {
	return OutboundURLPolicy{}
}

// ValidateOutboundURL checks raw against the environment's policy.
func ValidateOutboundURL(raw string) error {
	return DefaultOutboundURLPolicy().Validate(raw)
}

func ValidateOutboundURLWithPolicy(raw string, policy OutboundURLPolicy) error {
	return policy.Validate(raw)
}

// SafeDialControl is a net.Dialer.Control hook using the environment's policy.
func SafeDialControl(network string, address string, conn syscall.RawConn) error {
	return DefaultOutboundURLPolicy().DialControl(network, address, conn)
}

func SafeDialControlForPolicy(policy OutboundURLPolicy) func(string, string, syscall.RawConn) error {
	return func(network string, address string, conn syscall.RawConn) error {
		return policy.DialControl(network, address, conn)
	}
}

// Validate reports whether raw is an acceptable tenant-supplied endpoint. It
// rejects non-HTTP schemes and any host that resolves to a forbidden address.
//
// Callers must ALSO install DialControl on the dialer they use; Validate alone
// cannot close the DNS-rebinding window.
func (p OutboundURLPolicy) Validate(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("%w: empty URL", ErrUnsafeOutboundURL)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafeOutboundURL, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: scheme %q is not allowed", ErrUnsafeOutboundURL, parsed.Scheme)
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("%w: missing host", ErrUnsafeOutboundURL)
	}
	// ".local" is mDNS; "localhost" is only acceptable under the opt-in.
	lower := strings.ToLower(host)
	if strings.HasSuffix(lower, ".local") {
		return fmt.Errorf("%w: host %q is mDNS-local", ErrUnsafeOutboundURL, host)
	}
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		if !p.AllowPrivate {
			return fmt.Errorf(
				"%w: host %q is loopback; enable private endpoints for this workspace config",
				ErrUnsafeOutboundURL, host,
			)
		}
		return nil
	}

	// A literal IP is checked directly; a hostname has every resolved address
	// checked, since one acceptable answer does not make the rest safe.
	if ip := net.ParseIP(host); ip != nil {
		return p.checkIP(ip)
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve %q: %v", ErrUnsafeOutboundURL, host, err)
	}
	for _, ip := range addrs {
		if err := p.checkIP(ip); err != nil {
			return fmt.Errorf("%w: host %q resolves to %s", ErrUnsafeOutboundURL, host, ip)
		}
	}
	return nil
}

// DialControl refuses connections to forbidden addresses. It inspects the
// concrete address the kernel is about to connect to, which is what makes it
// immune to DNS rebinding.
func (p OutboundURLPolicy) DialControl(_ string, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: cannot parse dial address %q", ErrUnsafeOutboundURL, address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: dial address %q is not an IP", ErrUnsafeOutboundURL, host)
	}
	if err := p.checkIP(ip); err != nil {
		return err
	}
	return nil
}

// checkIP applies the policy to a concrete address.
//
// The classification comes from internal/ipclass so this guard and the
// end-user URL guard in internal/utils cannot disagree about what an address
// is; only the verdict per class is local to the sandbox.
func (p OutboundURLPolicy) checkIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("%w: missing address", ErrUnsafeOutboundURL)
	}
	class, reason := ipclass.Classify(ip)
	switch class {
	case ipclass.Public, ipclass.Documentation:
		// TEST-NET is never routed, so refusing it would protect nothing and
		// would cost the tests their DNS-free stand-in for a public address.
	case ipclass.Loopback, ipclass.Private, ipclass.CGNAT:
		// What the opt-in exists for: Cube listens on 127.0.0.1:33000 by
		// default, and a control plane inside RFC1918 is the normal
		// self-hosted shape rather than an exception.
		if !p.AllowPrivate {
			return fmt.Errorf(
				"%w: address %s is private; enable private endpoints for this workspace config",
				ErrUnsafeOutboundURL, ip,
			)
		}
	default:
		// Refused even under the opt-in. LinkLocal carries the cloud metadata
		// service; Translated is how a target reaches it while still looking
		// like ordinary public IPv6; the rest cannot reach a sandbox at all.
		return fmt.Errorf("%w: address %s is never routable to a sandbox (%s)",
			ErrUnsafeOutboundURL, ip, reason)
	}
	return nil
}

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
// Self-hosted deployments complicate this: CubeAPI's own default endpoint is
// http://127.0.0.1:33000, so a blanket ban on private addresses would make
// per-tenant Cube configuration impossible. The policy is therefore
// deny-by-default with an explicit operator opt-in
// (WEKNORA_SANDBOX_ALLOW_PRIVATE_ENDPOINTS=true). Even when opted in, the
// link-local range — and with it the cloud metadata address — stays blocked,
// because it is never a legitimate sandbox endpoint.
package sandbox

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ErrUnsafeOutboundURL is returned for any endpoint that uses an unsupported
// scheme or resolves to an address the policy forbids.
var ErrUnsafeOutboundURL = errors.New("sandbox: unsafe outbound URL")

// AllowPrivateEndpointsEnv opts a deployment into private/loopback sandbox
// endpoints. Required for self-hosted Cube, which listens on 127.0.0.1 by
// default. Link-local addresses remain blocked regardless.
const AllowPrivateEndpointsEnv = "WEKNORA_SANDBOX_ALLOW_PRIVATE_ENDPOINTS"

// OutboundURLPolicy decides which addresses tenant-supplied endpoints may
// resolve to.
type OutboundURLPolicy struct {
	// AllowPrivate permits loopback and RFC1918 addresses. It never permits
	// link-local (169.254.0.0/16), which carries the cloud metadata service.
	AllowPrivate bool
}

// DefaultOutboundURLPolicy reads the policy from the environment. Absent or
// unparseable values fail closed.
func DefaultOutboundURLPolicy() OutboundURLPolicy {
	allow, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(AllowPrivateEndpointsEnv)))
	if err != nil {
		allow = false
	}
	return OutboundURLPolicy{AllowPrivate: allow}
}

// ValidateOutboundURL checks raw against the environment's policy.
func ValidateOutboundURL(raw string) error {
	return DefaultOutboundURLPolicy().Validate(raw)
}

// SafeDialControl is a net.Dialer.Control hook using the environment's policy.
func SafeDialControl(network string, address string, conn syscall.RawConn) error {
	return DefaultOutboundURLPolicy().DialControl(network, address, conn)
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
				"%w: host %q is loopback; set %s=true to allow self-hosted endpoints",
				ErrUnsafeOutboundURL, host, AllowPrivateEndpointsEnv,
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
func (p OutboundURLPolicy) checkIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("%w: missing address", ErrUnsafeOutboundURL)
	}
	// Never allowed, even under the private opt-in: link-local carries the
	// cloud metadata service, and multicast/unspecified are meaningless here.
	if ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() {
		return fmt.Errorf("%w: address %s is never routable to a sandbox", ErrUnsafeOutboundURL, ip)
	}
	if ip.IsLoopback() || ip.IsPrivate() || isCarrierGradeNAT(ip) {
		if !p.AllowPrivate {
			return fmt.Errorf(
				"%w: address %s is private; set %s=true to allow self-hosted endpoints",
				ErrUnsafeOutboundURL, ip, AllowPrivateEndpointsEnv,
			)
		}
	}
	return nil
}

// isCarrierGradeNAT reports whether ip falls in 100.64.0.0/10, which IsPrivate
// does not cover but which is equally unsuitable as a public endpoint.
func isCarrierGradeNAT(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

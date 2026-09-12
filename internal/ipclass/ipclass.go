// Package ipclass categorises IP addresses for outbound-request policy.
//
// The classification lives here, separate from the policy, because WeKnora
// guards two kinds of outbound target whose threat models differ:
//
//   - internal/utils guards URLs an end user submits, so it refuses every
//     class except Public.
//   - internal/sandbox guards cluster endpoints a workspace admin configures,
//     so it has to tell "never routable" apart from "private, which this
//     workspace may have opted into".
//
// Sharing the classification while keeping the policy at each call site is
// what stops the two from drifting. The drift is not hypothetical: net.IP's
// IsPrivate covers fc00::/7 but not fec0::/10, and neither it nor
// IsLinkLocalUnicast looks inside a 6to4, Teredo, NAT64 or IPv4-compatible
// address, so a hand-rolled "is this private" check silently admits several
// spellings of 169.254.169.254.
package ipclass

import (
	"fmt"
	"net"
)

// Class is the policy-relevant category of an address. This package decides
// which class an address is; callers decide which classes they refuse.
type Class int

const (
	// Public is globally routable with no policy objection.
	Public Class = iota

	// Invalid is a nil or malformed address. It is its own class rather than
	// Public so that a caller which forgets to check fails closed.
	Invalid

	// Unspecified is 0.0.0.0 or ::.
	Unspecified

	// Loopback is 127.0.0.0/8 or ::1.
	Loopback

	// Private is RFC1918 or an IPv6 unique local address (fc00::/7).
	Private

	// CGNAT is 100.64.0.0/10 (RFC 6598). IsPrivate does not cover it, but it
	// is just as unsuitable as a public endpoint.
	CGNAT

	// LinkLocal is 169.254.0.0/16 or fe80::/10, plus link-local multicast.
	// This is the class that carries cloud metadata services, and the one
	// callers should refuse regardless of any private-network opt-in.
	LinkLocal

	// Multicast is any other multicast address, interface-local included.
	Multicast

	// SiteLocalIPv6 is the deprecated fec0::/10. Distinct from Private
	// because IsPrivate does not cover it.
	SiteLocalIPv6

	// Reserved is a range that cannot reach a real service: 0.0.0.0/8,
	// 240.0.0.0/4 (broadcast included), and the IETF assignment and
	// benchmarking ranges.
	Reserved

	// Documentation is TEST-NET-1/2/3. Reserved for documentation and never
	// routed, so refusing it buys no security. It is a separate class so a
	// caller can accept it as a DNS-free stand-in for a public address.
	Documentation

	// Translated is an IPv6 address carrying a restricted IPv4 address: 6to4,
	// Teredo, NAT64's well-known prefix, or the deprecated IPv4-compatible
	// form. These are how a target reaches link-local while the outer address
	// still looks like ordinary public IPv6.
	Translated
)

// ipv4Range pairs a CIDR with the class addresses inside it belong to.
type ipv4Range struct {
	net   *net.IPNet
	class Class
}

// restrictedIPv4Ranges covers the IPv4 blocks that net.IP's own predicates
// miss. The entries are disjoint, so the iteration order does not affect the
// verdict.
//
// Two ranges are deliberately absent because a wider entry already covers
// them: 255.255.255.255 sits in 240.0.0.0/4, and Docker's default bridge
// networks (172.17.0.0/16 and neighbours) sit in 172.16.0.0/12, which
// IsPrivate classifies as Private before this table is consulted.
var restrictedIPv4Ranges = []ipv4Range{
	{mustCIDR("100.64.0.0/10"), CGNAT},           // RFC 6598 carrier-grade NAT
	{mustCIDR("0.0.0.0/8"), Reserved},            // RFC 1122 "this" network
	{mustCIDR("240.0.0.0/4"), Reserved},          // RFC 1112 reserved, incl. broadcast
	{mustCIDR("198.18.0.0/15"), Reserved},        // RFC 2544 benchmarking
	{mustCIDR("192.0.0.0/24"), Reserved},         // RFC 6890 IETF assignments
	{mustCIDR("192.0.2.0/24"), Documentation},    // TEST-NET-1
	{mustCIDR("198.51.100.0/24"), Documentation}, // TEST-NET-2
	{mustCIDR("203.0.113.0/24"), Documentation},  // TEST-NET-3
}

func mustCIDR(s string) *net.IPNet {
	_, ipNet, err := net.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("ipclass: invalid CIDR %s: %v", s, err))
	}
	return ipNet
}

// Classify categorises ip and returns a human-readable reason naming the
// class. The reason is empty for Public.
func Classify(ip net.IP) (Class, string) {
	// A net.IP is only meaningful at 4 or 16 bytes. Anything else — nil, or a
	// slice built by hand — must not fall through to Public, because callers
	// read Public as permission to dial.
	if len(ip) != net.IPv4len && len(ip) != net.IPv6len {
		return Invalid, "invalid address"
	}

	// net.IP's predicates run first because they normalise IPv4-in-IPv6
	// through To4(); that is what catches ::ffff:169.254.169.254 here as
	// link-local instead of leaving it to the IPv6 cases below.
	switch {
	case ip.IsPrivate():
		return Private, "private IP address"
	case ip.IsLoopback():
		return Loopback, "loopback address"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return LinkLocal, "link-local address"
	case ip.IsMulticast():
		return Multicast, "multicast address"
	case ip.IsUnspecified():
		return Unspecified, "unspecified address"
	}

	if ip4 := ip.To4(); ip4 != nil {
		for _, r := range restrictedIPv4Ranges {
			if r.net.Contains(ip4) {
				return r.class, fmt.Sprintf("restricted range %s", r.net.String())
			}
		}
		return Public, ""
	}

	if len(ip) == net.IPv6len {
		return classifyIPv6(ip)
	}
	return Public, ""
}

// IsPublic reports whether ip carries no policy objection at all. Callers that
// need to distinguish "private, maybe allowed" from "never routable" should use
// Classify instead.
func IsPublic(ip net.IP) bool {
	class, _ := Classify(ip)
	return class == Public
}

func classifyIPv6(ip net.IP) (Class, string) {
	// fec0::/10, deprecated site-local. IsPrivate stops at fc00::/7.
	if ip[0] == 0xfe && ip[1]&0xc0 == 0xc0 {
		return SiteLocalIPv6, "site-local IPv6 address"
	}
	// fc00::/7 unique local. IsPrivate already caught this; kept so the
	// function is correct when read on its own.
	if ip[0]&0xfe == 0xfc {
		return Private, "unique local IPv6 address"
	}
	// Teredo, 2001::/32. Refused outright rather than by embedded address:
	// the payload is obfuscated, and the tunnel exists to reach elsewhere.
	if ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x00 && ip[3] == 0x00 {
		return Translated, "Teredo tunneling address"
	}
	// The remaining encodings carry a plain IPv4 address, so the payload
	// decides: 6to4 wrapping a public address is a public address.
	if embedded, encoding := embeddedIPv4(ip); embedded != nil {
		if class, reason := Classify(embedded); class != Public {
			return Translated, fmt.Sprintf("%s %s", encoding, reason)
		}
	}
	return Public, ""
}

// embeddedIPv4 returns the IPv4 address an IPv6 address carries, for the
// encodings that can reach an IPv4 destination through a relay or translator,
// along with a name for the encoding.
func embeddedIPv4(ip net.IP) (net.IP, string) {
	switch {
	// 6to4, 2002::/16: bits 16-47 hold the IPv4 address.
	case ip[0] == 0x20 && ip[1] == 0x02:
		return net.IP(ip[2:6]), "6to4 embedded"
	// NAT64 well-known prefix, 64:ff9b::/96 (RFC 6052). Unlike 6to4 this one
	// is in live use, so an operator running NAT64 makes every internal IPv4
	// address reachable through it.
	case ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b &&
		isZeros(ip[4:12]):
		return net.IP(ip[12:16]), "NAT64 embedded"
	// IPv4-compatible ::a.b.c.d, deprecated by RFC 4291. ::ffff:a.b.c.d never
	// reaches here (To4 handles it), and :: and ::1 are already classified.
	case isZeros(ip[0:12]):
		return net.IP(ip[12:16]), "IPv4-compatible"
	}
	return nil, ""
}

func isZeros(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

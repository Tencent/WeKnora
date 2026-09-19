package pluginapi

import (
	"net"
	"strings"
)

// NormalizeHost lowercases a hostname and trims a single trailing dot so host
// matching is deterministic across the host and SDK.
func NormalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// MatchAllowlist reports whether host:port is covered by the allowlist. An
// entry may be an exact host, an exact host:port, or a "*.suffix" wildcard.
func MatchAllowlist(allowlist []string, host, port string) bool {
	host = NormalizeHost(host)
	for _, entry := range allowlist {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == host || entry == net.JoinHostPort(host, port) {
			return true
		}
		if strings.HasPrefix(entry, "*.") && strings.HasSuffix(host, strings.TrimPrefix(entry, "*")) {
			return true
		}
	}
	return false
}

// IsForbiddenHost reports whether a hostname is loopback, cloud metadata, an
// internal pseudo-TLD, or — when the host is an IP literal — a forbidden IP.
// It is the shared "must never be reachable" check used by both the host's
// NetworkGuard and the SDK's guarded HTTP client.
func IsForbiddenHost(host string) bool {
	host = NormalizeHost(host)
	if host == "localhost" || host == "metadata.google.internal" || host == "169.254.169.254" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return IsForbiddenIP(ip)
	}
	return strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal")
}

// IsForbiddenIP reports whether an IP is loopback, private, link-local,
// unspecified, or multicast — addresses that must never be reachable from a
// plugin regardless of policy or allowlist.
func IsForbiddenIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

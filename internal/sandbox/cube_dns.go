package sandbox

import (
	"fmt"
	"net"
	"strings"
)

// NormalizeCubeDNSServers trims, drops empties, rejects non-IP values, and
// de-duplicates. Cube's template `dns` field is a list of nameserver IPs —
// hostnames are not accepted. An empty result means "leave Cubelet's default".
func NormalizeCubeDNSServers(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		ip := strings.TrimSpace(item)
		if ip == "" {
			continue
		}
		parsed := net.ParseIP(ip)
		if parsed == nil {
			return nil, fmt.Errorf("sandbox: invalid cube DNS server %q (need an IP address)", item)
		}
		canonical := parsed.String()
		if _, dup := seen[canonical]; dup {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

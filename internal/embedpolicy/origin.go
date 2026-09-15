// Package embedpolicy separates browser framing policy from API transport origins.
package embedpolicy

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// NormalizeOrigin accepts an HTTP(S) origin, with an optional trailing slash.
// Reject URL components that cannot appear in an Origin or CSP host source.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery ||
		strings.Contains(raw, "#") || strings.ContainsAny(u.Host, " \t\r\n'\";,*\\") {
		return "", fmt.Errorf("invalid allowed origin: %q", raw)
	}
	host := strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		host = strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return u.Scheme + "://" + host, nil
}

// NormalizePattern accepts an exact origin, a subdomain wildcard, or the development wildcard.
func NormalizePattern(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "*" {
		return raw, nil
	}
	if strings.HasPrefix(raw, "*.") {
		origin, err := NormalizeOrigin("https://" + strings.TrimPrefix(raw, "*."))
		if err != nil {
			return "", fmt.Errorf("invalid allowed origin: %q", raw)
		}
		u, _ := url.Parse(origin)
		if net.ParseIP(u.Hostname()) != nil {
			return "", fmt.Errorf("invalid allowed origin: %q", raw)
		}
		// Preserve an explicit wildcard port, including the HTTPS default port.
		original, _ := url.Parse("https://" + strings.TrimPrefix(raw, "*."))
		host := strings.ToLower(original.Host)
		return "*." + host, nil
	}
	return NormalizeOrigin(raw)
}

// Allows matches an API origin against normalized host patterns.
func Allows(origin string, patterns []string) bool {
	normalized, err := NormalizeOrigin(origin)
	if err != nil {
		return false
	}
	u, _ := url.Parse(normalized)
	for _, raw := range patterns {
		pattern, err := NormalizePattern(raw)
		if err != nil {
			continue
		}
		if pattern == "*" || pattern == normalized {
			return true
		}
		if strings.HasPrefix(pattern, "*.") {
			wildcard, _ := url.Parse("https://" + strings.TrimPrefix(pattern, "*."))
			port := u.Port()
			if port == "" {
				if u.Scheme == "https" {
					port = "443"
				} else {
					port = "80"
				}
			}
			if strings.HasSuffix(u.Hostname(), "."+wildcard.Hostname()) &&
				(wildcard.Port() == "" || wildcard.Port() == port) {
				return true
			}
		}
	}
	return false
}

// FrameAncestors also permits same-origin management previews. Empty or wholly
// invalid legacy lists fail closed; invalid strings never reach the CSP header.
func FrameAncestors(patterns []string) string {
	sources := []string{}
	for _, raw := range patterns {
		pattern, err := NormalizePattern(raw)
		if err != nil {
			continue
		}
		if pattern == "*" {
			return "frame-ancestors *"
		}
		if strings.HasPrefix(pattern, "*.") {
			u, _ := url.Parse("https://" + strings.TrimPrefix(pattern, "*."))
			host := pattern
			if u.Port() == "" {
				host += ":*"
			}
			sources = append(sources, "http://"+host, "https://"+host)
		} else {
			sources = append(sources, pattern)
		}
	}
	if len(sources) == 0 {
		return "frame-ancestors 'none'"
	}
	return "frame-ancestors 'self' " + strings.Join(sources, " ")
}

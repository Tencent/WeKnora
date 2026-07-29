package yunzhijia

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

func deriveWebSocketURL(sendMsgURL, allowedHostSuffix string) (string, error) {
	u, err := validateEndpointURL(sendMsgURL, "https", allowedHostSuffix)
	if err != nil {
		return "", fmt.Errorf("invalid send_msg_url for websocket: %w", err)
	}

	token := strings.TrimSpace(u.Query().Get("yzjtoken"))
	if token == "" {
		return "", fmt.Errorf("send_msg_url is missing yzjtoken")
	}

	wsURL := &url.URL{
		Scheme: "wss",
		Host:   u.Host,
		Path:   "/xuntong/websocket",
	}
	query := url.Values{}
	query.Set("yzjtoken", token)
	wsURL.RawQuery = query.Encode()
	return wsURL.String(), nil
}

func validateEndpointURL(rawURL, requiredScheme, allowedHostSuffix string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(u.Scheme, requiredScheme) {
		return nil, fmt.Errorf("URL must use %s", requiredScheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("URL must not contain user information")
	}

	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return nil, fmt.Errorf("URL has empty host")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || net.ParseIP(host) != nil {
		return nil, fmt.Errorf("URL host must be a DNS name")
	}

	suffix := strings.ToLower(strings.Trim(strings.TrimSpace(allowedHostSuffix), "."))
	if suffix == "" {
		return nil, fmt.Errorf("allowed host suffix is required")
	}
	if host != suffix && !strings.HasSuffix(host, "."+suffix) {
		return nil, fmt.Errorf("URL host %q does not match allowed suffix %q", host, suffix)
	}

	return u, nil
}

func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split dial address: %w", err)
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve endpoint host %q: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("endpoint host %q resolved to no addresses", host)
	}
	for _, address := range addresses {
		if !isPublicIP(address.IP) {
			return nil, fmt.Errorf("endpoint host %q resolves to a non-public address", host)
		}
	}

	dialer := &net.Dialer{}
	var lastErr error
	for _, address := range addresses {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	return nil, fmt.Errorf("dial endpoint host %q: %w", host, lastErr)
}

func isPublicIP(ip net.IP) bool {
	return ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}

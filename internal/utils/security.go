package utils

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// XSS 防护相关正则表达式
var (
	// 匹配潜在的 XSS 攻击模式
	xssPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<script[^>]*>.*?</script>`),
		regexp.MustCompile(`(?i)<iframe[^>]*>.*?</iframe>`),
		regexp.MustCompile(`(?i)<object[^>]*>.*?</object>`),
		regexp.MustCompile(`(?i)<embed[^>]*>.*?</embed>`),
		regexp.MustCompile(`(?i)<embed[^>]*>`),
		regexp.MustCompile(`(?i)<form[^>]*>.*?</form>`),
		regexp.MustCompile(`(?i)<input[^>]*>`),
		regexp.MustCompile(`(?i)<button[^>]*>.*?</button>`),
		regexp.MustCompile(`(?i)javascript:`),
		regexp.MustCompile(`(?i)vbscript:`),
		regexp.MustCompile(`(?i)onload\s*=`),
		regexp.MustCompile(`(?i)onerror\s*=`),
		regexp.MustCompile(`(?i)onclick\s*=`),
		regexp.MustCompile(`(?i)onmouseover\s*=`),
		regexp.MustCompile(`(?i)onfocus\s*=`),
		regexp.MustCompile(`(?i)onblur\s*=`),
	}
)

// SanitizeHTML 清理 HTML 内容，防止 XSS 攻击
func SanitizeHTML(input string) string {
	if input == "" {
		return ""
	}

	// 检查输入长度
	if len(input) > 10000 {
		input = input[:10000]
	}

	// 检查是否包含潜在的 XSS 攻击
	for _, pattern := range xssPatterns {
		if pattern.MatchString(input) {
			// 如果包含恶意内容，进行 HTML 转义
			return html.EscapeString(input)
		}
	}

	// 如果内容相对安全，返回原内容
	return input
}

// EscapeHTML 转义 HTML 特殊字符
func EscapeHTML(input string) string {
	if input == "" {
		return ""
	}
	return html.EscapeString(input)
}

// ValidateInput 验证用户输入
func ValidateInput(input string) (string, bool) {
	if input == "" {
		return "", true
	}

	// 检查是否包含控制字符
	for _, r := range input {
		if r < 32 && r != 9 && r != 10 && r != 13 {
			return "", false
		}
	}

	// 检查 UTF-8 有效性
	if !utf8.ValidString(input) {
		return "", false
	}

	// 检查是否包含潜在的 XSS 攻击
	for _, pattern := range xssPatterns {
		if pattern.MatchString(input) {
			return "", false
		}
	}

	return strings.TrimSpace(input), true
}

// IsValidURL 验证 URL 是否安全
func IsValidURL(url string) bool {
	if url == "" {
		return false
	}

	// 检查长度
	if len(url) > 2048 {
		return false
	}

	// 检查协议
	if !strings.HasPrefix(strings.ToLower(url), "http://") &&
		!strings.HasPrefix(strings.ToLower(url), "https://") {
		return false
	}

	// 检查是否包含恶意内容
	for _, pattern := range xssPatterns {
		if pattern.MatchString(url) {
			return false
		}
	}

	return true
}

// restrictedHostnames contains hostnames that are blocked for SSRF prevention
var restrictedHostnames = []string{
	"localhost",
	"127.0.0.1",
	"::1",
	"0.0.0.0",
	"metadata.google.internal",
	"metadata.tencentyun.com",
	"metadata.aws.internal",
	// Docker-specific internal hostnames
	"host.docker.internal",
	"gateway.docker.internal",
	"kubernetes.docker.internal",
	// Kubernetes internal hostnames
	"kubernetes",
	"kubernetes.default",
	"kubernetes.default.svc",
	"kubernetes.default.svc.cluster.local",
}

// restrictedHostSuffixes contains hostname suffixes that are blocked
var restrictedHostSuffixes = []string{
	".local",
	".localhost",
	".internal",
	".corp",
	".lan",
	".home",
	".localdomain",
	// Kubernetes internal suffixes
	".svc.cluster.local",
	".pod.cluster.local",
}

// restrictedIPv4Ranges contains CIDR ranges that should be blocked
// These are additional ranges not covered by Go's IsPrivate(), IsLoopback(), etc.
var restrictedIPv4Ranges = []*net.IPNet{
	// 100.64.0.0/10 - Carrier-grade NAT (RFC 6598)
	mustParseCIDR("100.64.0.0/10"),
	// 198.18.0.0/15 - Network device benchmark testing (RFC 2544)
	mustParseCIDR("198.18.0.0/15"),
	// 198.51.100.0/24 - TEST-NET-2 for documentation (RFC 5737)
	mustParseCIDR("198.51.100.0/24"),
	// 203.0.113.0/24 - TEST-NET-3 for documentation (RFC 5737)
	mustParseCIDR("203.0.113.0/24"),
	// 192.0.0.0/24 - IETF Protocol Assignments (RFC 6890)
	mustParseCIDR("192.0.0.0/24"),
	// 192.0.2.0/24 - TEST-NET-1 for documentation (RFC 5737)
	mustParseCIDR("192.0.2.0/24"),
	// 0.0.0.0/8 - "This" network (RFC 1122)
	mustParseCIDR("0.0.0.0/8"),
	// 240.0.0.0/4 - Reserved for future use (RFC 1112)
	mustParseCIDR("240.0.0.0/4"),
	// 255.255.255.255/32 - Limited broadcast
	mustParseCIDR("255.255.255.255/32"),
	// Docker bridge network (default range)
	mustParseCIDR("172.17.0.0/16"),
	// Docker user-defined bridge networks (commonly used range)
	mustParseCIDR("172.18.0.0/16"),
	mustParseCIDR("172.19.0.0/16"),
	mustParseCIDR("172.20.0.0/16"),
}

// mustParseCIDR parses a CIDR string and panics on error
func mustParseCIDR(s string) *net.IPNet {
	_, ipNet, err := net.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR: %s", s))
	}
	return ipNet
}

// isRestrictedIP checks if an IP address falls within any restricted range
func isRestrictedIP(ip net.IP) (bool, string) {
	// Check Go's built-in methods first
	if ip.IsPrivate() {
		return true, "private IP address"
	}
	if ip.IsLoopback() {
		return true, "loopback address"
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true, "link-local address"
	}
	if ip.IsMulticast() {
		return true, "multicast address"
	}
	if ip.IsUnspecified() {
		return true, "unspecified address"
	}

	// Check IPv4-specific restricted ranges
	if ip4 := ip.To4(); ip4 != nil {
		for _, cidr := range restrictedIPv4Ranges {
			if cidr.Contains(ip4) {
				return true, fmt.Sprintf("restricted range %s", cidr.String())
			}
		}
	}

	// Check IPv6-specific restrictions
	if ip.To4() == nil && len(ip) == 16 {
		// Site-local (deprecated but still blocked): fec0::/10
		if ip[0] == 0xfe && (ip[1]&0xc0) == 0xc0 {
			return true, "site-local IPv6 address"
		}
		// Unique local address (ULA): fc00::/7 (already covered by IsPrivate for Go 1.17+)
		if (ip[0] & 0xfe) == 0xfc {
			return true, "unique local IPv6 address"
		}
		// IPv4-mapped IPv6 addresses: ::ffff:x.x.x.x
		if isZeros(ip[0:10]) && ip[10] == 0xff && ip[11] == 0xff {
			mappedIP := ip[12:16]
			if restricted, reason := isRestrictedIP(net.IP(mappedIP)); restricted {
				return true, fmt.Sprintf("IPv4-mapped %s", reason)
			}
		}
	}

	return false, ""
}

// isZeros checks if a byte slice is all zeros
func isZeros(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// ipLikePatterns contains regex patterns for detecting IP-like hostnames
// These catch various IP address obfuscation techniques
var ipLikePatterns = []*regexp.Regexp{
	// Standard IPv4: 192.168.1.1
	regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`),
	// Decimal IP: 3232235777 (equivalent to 192.168.1.1)
	regexp.MustCompile(`^\d{8,10}$`),
	// Octal IP: 0300.0250.0001.0001 or 0177.0.0.1
	regexp.MustCompile(`^0[0-7]+\.`),
	// Hex IP: 0xC0.0xA8.0x01.0x01 or 0x7f.0.0.1
	regexp.MustCompile(`(?i)^0x[0-9a-f]+\.`),
	// Mixed formats with hex: 0xC0A80101
	regexp.MustCompile(`(?i)^0x[0-9a-f]{6,8}$`),
	// IPv6 patterns
	regexp.MustCompile(`(?i)^[0-9a-f:]+::[0-9a-f:]*$`),
	regexp.MustCompile(`(?i)^[0-9a-f]{1,4}(:[0-9a-f]{1,4}){7}$`),
	// IPv4-mapped IPv6: ::ffff:192.168.1.1
	regexp.MustCompile(`(?i)^::ffff:\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`),
	// Bracketed IPv6: [::1]
	regexp.MustCompile(`(?i)^\[[0-9a-f:]+\]$`),
}

// isIPLikeHostname checks if a hostname looks like an IP address in any format
// This catches obfuscation attempts like octal, hex, decimal, etc.
func isIPLikeHostname(hostname string) bool {
	for _, pattern := range ipLikePatterns {
		if pattern.MatchString(hostname) {
			return true
		}
	}
	return false
}

// IsSSRFSafeURL validates a URL to prevent SSRF attacks
// It checks for:
// - Valid http/https protocol
// - Private IP addresses (10.x.x.x, 172.16-31.x.x, 192.168.x.x)
// - Loopback addresses (127.x.x.x, ::1)
// - Link-local addresses (169.254.x.x, fe80::)
// - Cloud metadata endpoints
// - Reserved hostnames (localhost, *.local, etc.)
func IsSSRFSafeURL(rawURL string) (bool, string) {
	if rawURL == "" {
		return false, "URL is empty"
	}

	// Check URL length
	if len(rawURL) > 2048 {
		return false, "URL exceeds maximum length"
	}

	// Parse URL
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false, fmt.Sprintf("invalid URL format: %v", err)
	}

	// Only allow http and https
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false, fmt.Sprintf("invalid scheme: %s (only http/https allowed)", scheme)
	}

	// Extract hostname
	hostname := parsed.Hostname()
	if hostname == "" {
		return false, "URL has no hostname"
	}
	hostnameLower := strings.ToLower(hostname)

	// Check against restricted hostnames
	for _, restricted := range restrictedHostnames {
		if hostnameLower == restricted {
			return false, fmt.Sprintf("hostname %s is restricted", hostname)
		}
	}

	// Check against restricted hostname suffixes
	for _, suffix := range restrictedHostSuffixes {
		if strings.HasSuffix(hostnameLower, suffix) {
			return false, fmt.Sprintf("hostname suffix %s is restricted", suffix)
		}
	}

	// STRICT MODE: Completely block IP addresses in URLs
	// This prevents all IP-based SSRF attacks including edge cases and bypasses
	ip := net.ParseIP(hostname)
	if ip != nil {
		return false, "direct IP address access is not allowed, use domain name instead"
	}

	// Also check for IP addresses in various formats that ParseIP might not catch
	// e.g., octal (0177.0.0.1), hex (0x7f.0.0.1), decimal (2130706433)
	if isIPLikeHostname(hostname) {
		return false, "IP-like hostname format is not allowed"
	}

	// Perform DNS resolution to check the resolved IP
	// This prevents DNS rebinding attacks where a domain resolves to internal IPs
	ips, err := net.LookupIP(hostname)
	if err != nil {
		// DNS resolution failed - reject the URL for security
		// This prevents attacks where:
		// 1. The domain is only resolvable within internal network (intranet domains)
		// 2. Different DNS servers between validation and actual request
		// 3. Attacker-controlled DNS that selectively responds
		return false, fmt.Sprintf("DNS resolution failed for hostname %s: cannot verify if it resolves to safe IP", hostname)
	}

	// Check if any resolved IP is restricted
	for _, resolvedIP := range ips {
		if restricted, reason := isRestrictedIP(resolvedIP); restricted {
			return false, fmt.Sprintf("hostname %s resolves to restricted IP %s: %s", hostname, resolvedIP.String(), reason)
		}
	}

	// Check for suspicious port numbers
	port := parsed.Port()
	if port != "" {
		// Block common internal service ports
		blockedPorts := map[string]bool{
			"22":    true, // SSH
			"23":    true, // Telnet
			"25":    true, // SMTP
			"445":   true, // SMB
			"3389":  true, // RDP
			"5432":  true, // PostgreSQL
			"3306":  true, // MySQL
			"6379":  true, // Redis
			"27017": true, // MongoDB
			"9200":  true, // Elasticsearch
			"2379":  true, // etcd
			"2380":  true, // etcd
			"8500":  true, // Consul
			"4001":  true, // etcd (old)
		}
		if blockedPorts[port] {
			return false, fmt.Sprintf("port %s is blocked for security reasons", port)
		}
	}

	return true, ""
}

// IsValidImageURL 验证图片 URL 是否安全
func IsValidImageURL(url string) bool {
	if !IsValidURL(url) {
		return false
	}

	// 检查是否为图片文件
	imageExtensions := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".bmp", ".ico"}
	lowerURL := strings.ToLower(url)

	for _, ext := range imageExtensions {
		if strings.Contains(lowerURL, ext) {
			return true
		}
	}

	return false
}

// CleanMarkdown 清理 Markdown 内容
func CleanMarkdown(input string) string {
	if input == "" {
		return ""
	}

	// 移除潜在的恶意脚本
	cleaned := input
	for _, pattern := range xssPatterns {
		cleaned = pattern.ReplaceAllString(cleaned, "")
	}

	return cleaned
}

// SanitizeForDisplay 为显示清理内容
func SanitizeForDisplay(input string) string {
	if input == "" {
		return ""
	}

	// 首先清理 Markdown
	cleaned := CleanMarkdown(input)

	// 然后进行 HTML 转义
	escaped := html.EscapeString(cleaned)

	return escaped
}

// SanitizeForLog 清理日志输入,防止日志注入攻击
// 日志注入攻击是指攻击者通过在输入中插入换行符和其他控制字符,
// 伪造日志条目,可能导致日志分析工具误判或隐藏恶意活动
func SanitizeForLog(input string) string {
	if input == "" {
		return ""
	}

	// 替换换行符(LF, CR, CRLF)为空格,防止日志注入
	sanitized := strings.ReplaceAll(input, "\n", " ")
	sanitized = strings.ReplaceAll(sanitized, "\r", " ")

	// 替换制表符为空格
	sanitized = strings.ReplaceAll(sanitized, "\t", " ")

	// 移除其他控制字符(ASCII 0-31,除了空格已处理的)
	var builder strings.Builder
	for _, r := range sanitized {
		// 保留可打印字符和常用Unicode字符
		if r >= 32 || r == ' ' {
			builder.WriteRune(r)
		}
	}

	sanitized = builder.String()

	return sanitized
}

// SanitizeForLogArray 清理日志输入数组,防止日志注入攻击
func SanitizeForLogArray(input []string) []string {
	if len(input) == 0 {
		return []string{}
	}

	sanitized := make([]string, 0, len(input))
	for _, item := range input {
		sanitized = append(sanitized, SanitizeForLog(item))
	}

	return sanitized
}

// AllowedStdioCommands defines the whitelist of allowed commands for MCP stdio transport
// These are the standard MCP server launchers that are considered safe
var AllowedStdioCommands = map[string]bool{
	"uvx": true, // Python package runner (uv)
	"npx": true, // Node.js package runner
}

// DangerousArgPatterns contains patterns that indicate potentially dangerous arguments
var DangerousArgPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^-c$`),                                   // Shell command execution flag
	regexp.MustCompile(`(?i)^--command$`),                            // Shell command execution flag
	regexp.MustCompile(`(?i)^-e$`),                                   // Eval flag
	regexp.MustCompile(`(?i)^--eval$`),                               // Eval flag
	regexp.MustCompile(`(?i)[;&|]`),                                  // Shell command chaining
	regexp.MustCompile(`(?i)\$\(`),                                   // Command substitution
	regexp.MustCompile("(?i)`"),                                      // Backtick command substitution
	regexp.MustCompile(`(?i)>\s*[/~]`),                               // Output redirection to absolute/home path
	regexp.MustCompile(`(?i)<\s*[/~]`),                               // Input redirection from absolute/home path
	regexp.MustCompile(`(?i)^/bin/`),                                 // Direct binary path
	regexp.MustCompile(`(?i)^/usr/bin/`),                             // Direct binary path
	regexp.MustCompile(`(?i)^/sbin/`),                                // Direct binary path
	regexp.MustCompile(`(?i)^/usr/sbin/`),                            // Direct binary path
	regexp.MustCompile(`(?i)^\.\./`),                                 // Path traversal
	regexp.MustCompile(`(?i)/\.\./`),                                 // Path traversal in middle
	regexp.MustCompile(`(?i)^(bash|sh|zsh|ksh|csh|tcsh|fish|dash)$`), // Shell interpreters as args
	regexp.MustCompile(`(?i)^(curl|wget|nc|netcat|ncat)$`),           // Network tools as args
	regexp.MustCompile(`(?i)^(rm|dd|mkfs|fdisk)$`),                   // Destructive commands as args
}

// DangerousEnvVarPatterns contains patterns for dangerous environment variable names or values
var DangerousEnvVarPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^LD_PRELOAD$`),      // Library injection
	regexp.MustCompile(`(?i)^LD_LIBRARY_PATH$`), // Library path manipulation
	regexp.MustCompile(`(?i)^DYLD_`),            // macOS dynamic linker
	regexp.MustCompile(`(?i)^PATH$`),            // PATH manipulation
	regexp.MustCompile(`(?i)^PYTHONPATH$`),      // Python path manipulation
	regexp.MustCompile(`(?i)^NODE_OPTIONS$`),    // Node.js options injection
	regexp.MustCompile(`(?i)^BASH_ENV$`),        // Bash environment file
	regexp.MustCompile(`(?i)^ENV$`),             // Shell environment file
	regexp.MustCompile(`(?i)^SHELL$`),           // Shell override
}

// ValidateStdioCommand validates the command for MCP stdio transport
// Returns an error if the command is not in the whitelist or contains dangerous patterns
func ValidateStdioCommand(command string) error {
	if command == "" {
		return fmt.Errorf("command cannot be empty")
	}

	// Normalize command (extract base name if it's a path)
	baseCommand := command
	if strings.Contains(command, "/") {
		parts := strings.Split(command, "/")
		baseCommand = parts[len(parts)-1]
	}

	// Check against whitelist
	if !AllowedStdioCommands[baseCommand] {
		return fmt.Errorf("command '%s' is not in the allowed list. Allowed commands: uvx, npx, node, python, python3, deno, bun", baseCommand)
	}

	// Additional check: command should not contain path traversal
	if strings.Contains(command, "..") {
		return fmt.Errorf("command path contains invalid characters")
	}

	return nil
}

// ValidateStdioArgs validates the arguments for MCP stdio transport
// Returns an error if any argument contains dangerous patterns
func ValidateStdioArgs(args []string) error {
	if len(args) == 0 {
		return nil
	}

	for i, arg := range args {
		// Check length
		if len(arg) > 1024 {
			return fmt.Errorf("argument %d exceeds maximum length (1024 characters)", i)
		}

		// Check against dangerous patterns
		for _, pattern := range DangerousArgPatterns {
			if pattern.MatchString(arg) {
				return fmt.Errorf("argument %d contains potentially dangerous pattern: %s", i, SanitizeForLog(arg))
			}
		}

		// Check for null bytes
		if strings.Contains(arg, "\x00") {
			return fmt.Errorf("argument %d contains null bytes", i)
		}
	}

	return nil
}

// ValidateStdioEnvVars validates environment variables for MCP stdio transport
// Returns an error if any env var name or value is dangerous
func ValidateStdioEnvVars(envVars map[string]string) error {
	if len(envVars) == 0 {
		return nil
	}

	for key, value := range envVars {
		// Check key against dangerous patterns
		for _, pattern := range DangerousEnvVarPatterns {
			if pattern.MatchString(key) {
				return fmt.Errorf("environment variable '%s' is not allowed for security reasons", key)
			}
		}

		// Check key length
		if len(key) > 256 {
			return fmt.Errorf("environment variable name '%s' exceeds maximum length", SanitizeForLog(key[:50]))
		}

		// Check value length
		if len(value) > 4096 {
			return fmt.Errorf("environment variable '%s' value exceeds maximum length", key)
		}

		// Check for null bytes in value
		if strings.Contains(value, "\x00") {
			return fmt.Errorf("environment variable '%s' value contains null bytes", key)
		}

		// Check value for shell injection patterns
		for _, pattern := range DangerousArgPatterns {
			if pattern.MatchString(value) {
				return fmt.Errorf("environment variable '%s' value contains potentially dangerous pattern", key)
			}
		}
	}

	return nil
}

// ValidateStdioConfig performs comprehensive validation of stdio configuration
// This should be called before creating or executing any stdio-based MCP client
func ValidateStdioConfig(command string, args []string, envVars map[string]string) error {
	// Validate command
	if err := ValidateStdioCommand(command); err != nil {
		return fmt.Errorf("invalid command: %w", err)
	}

	// Validate arguments
	if err := ValidateStdioArgs(args); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}

	// Validate environment variables
	if err := ValidateStdioEnvVars(envVars); err != nil {
		return fmt.Errorf("invalid environment variables: %w", err)
	}

	return nil
}

// SSRFSafeHTTPClientConfig contains configuration for the SSRF-safe HTTP client
type SSRFSafeHTTPClientConfig struct {
	Timeout             time.Duration
	MaxRedirects        int
	DisableKeepAlives   bool
	DisableCompression  bool
}

// DefaultSSRFSafeHTTPClientConfig returns the default configuration
func DefaultSSRFSafeHTTPClientConfig() SSRFSafeHTTPClientConfig {
	return SSRFSafeHTTPClientConfig{
		Timeout:            30 * time.Second,
		MaxRedirects:       10,
		DisableKeepAlives:  false,
		DisableCompression: false,
	}
}

// ErrSSRFRedirectBlocked is returned when a redirect target is blocked due to SSRF protection
var ErrSSRFRedirectBlocked = fmt.Errorf("redirect blocked: target URL failed SSRF validation")

// NewSSRFSafeHTTPClient creates an HTTP client that validates redirect targets against SSRF protections.
// This prevents SSRF attacks via HTTP redirects where an attacker's server redirects to internal services.
func NewSSRFSafeHTTPClient(config SSRFSafeHTTPClientConfig) *http.Client {
	transport := &http.Transport{
		DisableKeepAlives:  config.DisableKeepAlives,
		DisableCompression: config.DisableCompression,
		// Dial with SSRF protection - validates resolved IPs before connecting
		DialContext: ssrfSafeDialContext,
	}

	return &http.Client{
		Timeout:   config.Timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Check redirect count
			if len(via) >= config.MaxRedirects {
				return fmt.Errorf("stopped after %d redirects", config.MaxRedirects)
			}

			// Validate the redirect target URL for SSRF
			redirectURL := req.URL.String()
			if safe, reason := IsSSRFSafeURL(redirectURL); !safe {
				return fmt.Errorf("%w: %s", ErrSSRFRedirectBlocked, reason)
			}

			return nil
		},
	}
}

// ssrfSafeDialContext is a custom dial function that validates the resolved IP addresses
// before establishing a connection. This provides an additional layer of SSRF protection
// against DNS rebinding attacks during the connection phase.
func ssrfSafeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// Parse host and port
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %s: %w", addr, err)
	}

	// Check if the host is a restricted hostname
	hostLower := strings.ToLower(host)
	for _, restricted := range restrictedHostnames {
		if hostLower == restricted {
			return nil, fmt.Errorf("connection blocked: hostname %s is restricted", host)
		}
	}
	for _, suffix := range restrictedHostSuffixes {
		if strings.HasSuffix(hostLower, suffix) {
			return nil, fmt.Errorf("connection blocked: hostname suffix %s is restricted", suffix)
		}
	}

	// Resolve the hostname to IP addresses
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %s: %w", host, err)
	}

	// Validate all resolved IPs
	for _, ipAddr := range ips {
		if restricted, reason := isRestrictedIP(ipAddr.IP); restricted {
			return nil, fmt.Errorf("connection blocked: %s resolves to restricted IP %s (%s)", host, ipAddr.IP.String(), reason)
		}
	}

	// If we get here, all IPs are safe. Connect using the standard dialer.
	// We dial the original address so that proper connection routing happens.
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return dialer.DialContext(ctx, network, addr)
}

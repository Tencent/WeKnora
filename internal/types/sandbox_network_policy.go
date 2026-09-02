// Package types: sandbox network policy.
//
// One sandbox config carries one network policy, applied to every sandbox
// created from it — chat sessions, skill installs and connectivity probes
// alike. The provider-facing translation lives in internal/sandbox; this file
// is only the stored shape, its secret handling, and its validation.
package types

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// SandboxNetworkPolicy is the admin-facing network policy for one sandbox
// config. Both booleans are deliberately phrased so that the zero value IS
// the intended default — outbound open, inbound closed. That keeps a missing
// JSON block and an explicitly saved "default" policy identical, and avoids
// the tri-state pointer trap where nil would mean "allowed" for egress and
// "closed" for ingress.
type SandboxNetworkPolicy struct {
	// DenyEgressByDefault installs a 0.0.0.0/0 deny-all, after which only
	// AllowOut (and L7 rule targets) can reach the network. false allows
	// public egress, which is what skill installs need.
	DenyEgressByDefault bool `json:"deny_egress_by_default,omitempty"`

	// AllowPublicInbound exposes the sandbox's public URL without a
	// credential. false requires every inbound request to carry the
	// per-sandbox traffic access token.
	AllowPublicInbound bool `json:"allow_public_inbound,omitempty"`

	// AllowOut accepts IPv4, IPv4 CIDR, a DNS name, or a single-label
	// wildcard such as "*.example.com". Domain entries are only meaningful
	// together with a deny-all; see ValidateSandboxNetworkPolicy.
	AllowOut []string `json:"allow_out,omitempty"`

	// DenyOut accepts IPv4 and IPv4 CIDR only. Neither provider can deny a
	// domain: denial is a pure longest-prefix match on the destination IP.
	DenyOut []string `json:"deny_out,omitempty"`

	// CubeRules are validated whenever present and consumed only by the Cube
	// provider adapter.
	CubeRules []CubeEgressRule `json:"cube_rules,omitempty"`

	// E2BHostRules are validated whenever present and consumed only by the E2B
	// provider adapter.
	E2BHostRules []E2BHostRule `json:"e2b_host_rules,omitempty"`
}

// CubeEgressRule mirrors cubesandbox.Rule. Match fields are AND-ed; Methods is
// OR-ed internally.
type CubeEgressRule struct {
	Name    string   `json:"name"`
	Scheme  string   `json:"scheme,omitempty"`
	SNI     string   `json:"sni,omitempty"`
	Host    string   `json:"host,omitempty"`
	Methods []string `json:"methods,omitempty"`
	Path    string   `json:"path,omitempty"`

	// Deny inverts the action, which defaults to allow. A deny rule still
	// needs Host or SNI: the target has to reach CubeEgress for it to answer
	// with a request-level 403 instead of the network layer dropping it.
	Deny bool `json:"deny,omitempty"`

	// Audit is none | metadata | full. Empty uses the server default.
	Audit string `json:"audit,omitempty"`

	// Inject adds credential headers on allowed HTTPS requests.
	Inject []CubeHeaderInject `json:"inject,omitempty"`
}

// CubeHeaderInject mirrors cubesandbox.Inject. Secret is a credential and is
// encrypted at rest; Header and Format are not.
type CubeHeaderInject struct {
	Header string `json:"header"`
	Secret string `json:"secret"`
	Format string `json:"format,omitempty"`
}

// E2BHostRule mirrors one entry of e2b.NetworkConfig.Rules. Host must also
// appear in AllowOut: a rule grants no egress on its own.
type E2BHostRule struct {
	Host string `json:"host"`
	// Headers values are credentials and are encrypted at rest; names stay
	// readable so operators can see which headers are injected.
	Headers map[string]string `json:"headers,omitempty"`
}

// CloneWithSecrets deep-copies p, passing every credential-bearing field
// through transform. Both the encrypt (Value), decrypt (Scan) and mask
// (SandboxConfigForResponse) directions share it so they cannot drift about
// which fields count as secrets. The receiver is never mutated.
func (p *SandboxNetworkPolicy) CloneWithSecrets(
	transform func(string) string,
) *SandboxNetworkPolicy {
	if p == nil {
		return nil
	}
	out := *p
	out.AllowOut = append([]string(nil), p.AllowOut...)
	out.DenyOut = append([]string(nil), p.DenyOut...)

	if p.CubeRules != nil {
		rules := make([]CubeEgressRule, len(p.CubeRules))
		for i, rule := range p.CubeRules {
			copied := rule
			copied.Methods = append([]string(nil), rule.Methods...)
			if rule.Inject != nil {
				injects := make([]CubeHeaderInject, len(rule.Inject))
				for j, inject := range rule.Inject {
					injects[j] = inject
					injects[j].Secret = transform(inject.Secret)
				}
				copied.Inject = injects
			}
			rules[i] = copied
		}
		out.CubeRules = rules
	}

	if p.E2BHostRules != nil {
		hostRules := make([]E2BHostRule, len(p.E2BHostRules))
		for i, rule := range p.E2BHostRules {
			copied := rule
			if rule.Headers != nil {
				headers := make(map[string]string, len(rule.Headers))
				for name, value := range rule.Headers {
					headers[name] = transform(value)
				}
				copied.Headers = headers
			}
			hostRules[i] = copied
		}
		out.E2BHostRules = hostRules
	}
	return &out
}

// e2bNetworkLimits mirror the hard limits E2B enforces server-side. Checking
// them here turns a 400 at sandbox-create time into a message on the form the
// admin is looking at.
const (
	e2bMaxRuleDomains      = 10
	e2bMaxHeadersPerRule   = 20
	e2bMaxRuleDomainLength = 128
	e2bMaxHeaderNameLength = 64
	e2bMaxHeaderValueLen   = 2048
)

// DenyAllIPv4 is the deny-all entry both providers expect in a deny list. E2B
// calls it ALL_TRAFFIC in its error messages but accepts this CIDR (its own
// SDK exports it as e2b.AllTraffic with this value).
const DenyAllIPv4 = "0.0.0.0/0"

var (
	cubeAuditLevels = map[string]bool{"": true, "none": true, "metadata": true, "full": true}
	httpMethods     = map[string]bool{
		"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true,
		"DELETE": true, "CONNECT": true, "OPTIONS": true, "TRACE": true,
	}
)

// ValidateSandboxNetworkPolicy rejects a policy the provider would refuse, or
// would silently accept without doing what the admin meant. Errors are plain
// values; the service layer wraps them as bad-request responses.
func ValidateSandboxNetworkPolicy(cfg *TenantSandboxConfig) error {
	if cfg == nil || cfg.Network == nil {
		return nil
	}
	p := cfg.Network
	backend := strings.TrimSpace(strings.ToLower(cfg.SandboxType))

	if backend == "docker" &&
		(len(p.AllowOut) > 0 || len(p.DenyOut) > 0 ||
			len(p.CubeRules) > 0 || len(p.E2BHostRules) > 0) {
		return errors.New(
			"docker 后端只能整体开关出网，无法按 IP、域名或 HTTP 规则放行；" +
				"请清空放行/拒绝列表，或改用 cube / e2b 后端")
	}

	allowKeys := make(map[string]string, len(p.AllowOut))
	hasDomainAllow := false
	for _, target := range p.AllowOut {
		kind, key, err := classifyNetworkTarget(target, true)
		if err != nil {
			return fmt.Errorf("allow_out %q: %w", target, err)
		}
		if prior, seen := allowKeys[key]; seen {
			return fmt.Errorf("allow_out 中 %q 与 %q 是同一个目标，请只保留一条", prior, target)
		}
		allowKeys[key] = target
		if kind == targetDomain {
			hasDomainAllow = true
		}
	}

	denyKeys := make(map[string]string, len(p.DenyOut))
	deniesEverything := p.DenyEgressByDefault
	for _, target := range p.DenyOut {
		kind, key, err := classifyNetworkTarget(target, false)
		if err != nil {
			return fmt.Errorf("deny_out %q: %w", target, err)
		}
		if kind == targetDomain {
			return fmt.Errorf(
				"deny_out 不支持域名（%q）：拒绝判定只按目的 IP 匹配。"+
					"如需只放行少数域名，请启用「默认拒绝」并把它们写进 allow_out", target)
		}
		if prior, seen := denyKeys[key]; seen {
			return fmt.Errorf("deny_out 中 %q 与 %q 是同一个目标，请只保留一条", prior, target)
		}
		denyKeys[key] = target
		if key == DenyAllIPv4 {
			deniesEverything = true
		}
	}

	if hasDomainAllow && !deniesEverything {
		return errors.New(
			"allow_out 里有域名时必须同时兜底拒绝其余流量：" +
				"启用「默认拒绝」，或在 deny_out 中加入 0.0.0.0/0。" +
				"否则未经 DNS 学习的目的 IP 仍会默认放行，白名单形同虚设")
	}

	if err := validateCubeEgressRules(p.CubeRules); err != nil {
		return err
	}
	return validateE2BHostRules(p.E2BHostRules, allowKeys)
}

// DenyOutCoversAllIPv4 reports whether a deny list blocks every IPv4
// destination. That is the second of the two ways an admin can express the
// deny-all fallback — the first being DenyEgressByDefault — and
// ValidateSandboxNetworkPolicy treats them as equivalent, so every consumer
// that asks "does this policy deny by default?" has to accept both or it will
// misjudge a config the admin was allowed to save.
//
// It normalises through classifyNetworkTarget rather than comparing strings so
// it cannot disagree with the validator about which entries count: any /0
// collapses onto 0.0.0.0/0, and surrounding whitespace is ignored. Entries the
// validator would reject are skipped here; rejecting them is its job.
func DenyOutCoversAllIPv4(denyOut []string) bool {
	for _, target := range denyOut {
		kind, key, err := classifyNetworkTarget(target, false)
		if err != nil || kind != targetIP {
			continue
		}
		if key == DenyAllIPv4 {
			return true
		}
	}
	return false
}

type networkTargetKind int

const (
	targetIP networkTargetKind = iota
	targetDomain
)

// classifyNetworkTarget normalises one allow/deny entry and reports whether it
// is an address or a domain. key is the normalised form both providers dedupe
// on: a bare IPv4 collapses onto its /32, a domain is lowercased with the
// trailing dot removed.
func classifyNetworkTarget(
	target string,
	allowWildcard bool,
) (kind networkTargetKind, key string, err error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return 0, "", errors.New("不能为空")
	}
	if strings.ContainsAny(trimmed, " \t") {
		return 0, "", errors.New("不能包含空格")
	}
	if ip := net.ParseIP(trimmed); ip != nil {
		if ip.To4() == nil {
			return 0, "", errors.New("暂不支持 IPv6")
		}
		return targetIP, ip.String() + "/32", nil
	}
	if _, network, cidrErr := net.ParseCIDR(trimmed); cidrErr == nil {
		if network.IP.To4() == nil {
			return 0, "", errors.New("暂不支持 IPv6 CIDR")
		}
		return targetIP, network.String(), nil
	}
	return classifyDomainTarget(trimmed, allowWildcard)
}

func classifyDomainTarget(
	trimmed string,
	allowWildcard bool,
) (networkTargetKind, string, error) {
	domain := strings.ToLower(strings.TrimSuffix(trimmed, "."))
	if strings.Contains(domain, ":") {
		return 0, "", errors.New("不能带端口")
	}
	wildcard := false
	if strings.HasPrefix(domain, "*.") {
		if !allowWildcard {
			return 0, "", errors.New("此处不支持通配域名")
		}
		wildcard = true
		domain = strings.TrimPrefix(domain, "*.")
	}
	if strings.Trim(domain, "0123456789.") == "" {
		return 0, "", errors.New("不是合法 IPv4 地址")
	}
	if domain == "" || strings.Contains(domain, "*") {
		return 0, "", errors.New("通配符只能是单层前缀，例如 *.example.com")
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") ||
		strings.Contains(domain, "..") {
		return 0, "", errors.New("不是合法域名")
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return 0, "", errors.New("不是合法域名")
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return 0, "", errors.New("不是合法域名")
		}
		for _, r := range label {
			isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
			if !isAlnum && r != '-' {
				return 0, "", errors.New("不是合法域名")
			}
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return 0, "", errors.New("不是合法域名")
		}
	}
	key := domain
	if wildcard {
		key = "*." + domain
	}
	return targetDomain, key, nil
}

func validateCubeEgressRules(rules []CubeEgressRule) error {
	names := make(map[string]bool, len(rules))
	for _, rule := range rules {
		name := strings.TrimSpace(rule.Name)
		if name == "" {
			return errors.New("每条 Cube HTTP 规则都需要 name，用于审计与模板合并")
		}
		if names[name] {
			return fmt.Errorf("Cube HTTP 规则 name %q 重复", name)
		}
		names[name] = true

		if strings.TrimSpace(rule.Host) == "" && strings.TrimSpace(rule.SNI) == "" {
			return fmt.Errorf(
				"Cube HTTP 规则 %q 必须填 host 或 sni：网络层只从这两个字段提取放行目标，"+
					"只写 method / path 的规则永远到不了 CubeEgress", name)
		}
		if host := strings.TrimSpace(rule.Host); host != "" {
			if _, _, err := classifyNetworkTarget(hostWithoutPort(host), true); err != nil {
				return fmt.Errorf("Cube HTTP 规则 %q 的 host %q: %w", name, rule.Host, err)
			}
		}
		if sni := strings.TrimSpace(rule.SNI); sni != "" {
			kind, _, err := classifyNetworkTarget(sni, true)
			if err != nil {
				return fmt.Errorf("Cube HTTP 规则 %q 的 sni %q: %w", name, rule.SNI, err)
			}
			if kind != targetDomain {
				return fmt.Errorf("Cube HTTP 规则 %q 的 sni 只能是域名，不能是 IP", name)
			}
		}
		switch strings.ToLower(strings.TrimSpace(rule.Scheme)) {
		case "", "http", "https":
		default:
			return fmt.Errorf("Cube HTTP 规则 %q 的 scheme 只能是 http 或 https", name)
		}
		if !cubeAuditLevels[strings.ToLower(strings.TrimSpace(rule.Audit))] {
			return fmt.Errorf("Cube HTTP 规则 %q 的 audit 只能是 none、metadata 或 full", name)
		}
		for _, method := range rule.Methods {
			if !httpMethods[strings.ToUpper(strings.TrimSpace(method))] {
				return fmt.Errorf("Cube HTTP 规则 %q 的 method %q 不是标准 HTTP 方法", name, method)
			}
		}
		injectHeaders := make(map[string]bool, len(rule.Inject))
		for _, inject := range rule.Inject {
			header := strings.TrimSpace(inject.Header)
			if header == "" {
				return fmt.Errorf("Cube HTTP 规则 %q 的注入 header 名不能为空", name)
			}
			if injectHeaders[header] {
				return fmt.Errorf("Cube HTTP 规则 %q 的注入 header 名 %q 重复", name, header)
			}
			injectHeaders[header] = true
			if rule.Deny && inject.Secret != "" {
				return fmt.Errorf("Cube HTTP 规则 %q 是拒绝规则，注入 header 不会生效", name)
			}
		}
	}
	return nil
}

func hostWithoutPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func validateE2BHostRules(rules []E2BHostRule, allowKeys map[string]string) error {
	if len(rules) > e2bMaxRuleDomains {
		return fmt.Errorf("E2B 每个沙箱最多 %d 个 host 规则域名", e2bMaxRuleDomains)
	}
	seen := make(map[string]bool, len(rules))
	for _, rule := range rules {
		host := strings.TrimSpace(rule.Host)
		if host == "" {
			return errors.New("E2B host 规则的 host 不能为空")
		}
		if len(host) > e2bMaxRuleDomainLength {
			return fmt.Errorf("E2B host 规则的 host %q 超过 %d 字符", host, e2bMaxRuleDomainLength)
		}
		kind, key, err := classifyNetworkTarget(host, true)
		if err != nil {
			return fmt.Errorf("E2B host 规则的 host %q: %w", host, err)
		}
		if kind != targetDomain {
			return fmt.Errorf("E2B host 规则的 host %q 只能是域名", host)
		}
		if seen[key] {
			return fmt.Errorf("E2B host 规则 %q 重复：每个域名只能有一条规则", host)
		}
		seen[key] = true

		if !allowListCovers(allowKeys, key) {
			return fmt.Errorf(
				"E2B host 规则的 host %q 必须同时出现在 allow_out 中："+
					"规则本身只做 header 注入，不授权出网", host)
		}
		if len(rule.Headers) > e2bMaxHeadersPerRule {
			return fmt.Errorf("E2B host 规则 %q 最多 %d 个 header", host, e2bMaxHeadersPerRule)
		}
		for name, value := range rule.Headers {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("E2B host 规则 %q 的 header 名不能为空", host)
			}
			if len(name) > e2bMaxHeaderNameLength {
				return fmt.Errorf("E2B host 规则 %q 的 header 名超过 %d 字符",
					host, e2bMaxHeaderNameLength)
			}
			if len(value) > e2bMaxHeaderValueLen {
				return fmt.Errorf("E2B host 规则 %q 的 header 值超过 %d 字符",
					host, e2bMaxHeaderValueLen)
			}
		}
	}
	return nil
}

// allowListCovers reports whether an allow entry authorises host: either the
// exact name, or a wildcard whose suffix matches. Mirrors both providers'
// rule that "*.example.com" covers subdomains but not the apex.
func allowListCovers(allowKeys map[string]string, host string) bool {
	if _, ok := allowKeys[host]; ok {
		return true
	}
	for key := range allowKeys {
		suffix, ok := strings.CutPrefix(key, "*.")
		if !ok {
			continue
		}
		if strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

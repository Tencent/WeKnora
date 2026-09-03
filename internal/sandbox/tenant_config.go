// Package sandbox: tenant sandbox configuration resolution.
//
// ResolveEffectiveConfig turns one stored config into the *Config a manager is
// built from. A named config is self-contained: provider fields are never
// inherited from process configuration; they come from the workspace config or
// they are missing and the config is refused (see config_required.go).
//
// Field-level inheritance was tried first and removed. It made the stored row an
// incomplete picture of where a sandbox actually lives, which broke three things
// at once: identity comparison had to resolve against the baseline to decide
// whether an edit stranded anything, editing .env silently re-pointed configs
// that had left fields blank without cordoning their live sandboxes, and a
// config whose provider differed from the deployment mode inherited built-in
// constants instead — quietly dialling 127.0.0.1.
//
// What still comes from the baseline is deliberately narrow: the deployment's
// script execution timeout, which is an operational guardrail rather than part
// of a backend's identity. A nil tenant config is used only by low-level
// callers that explicitly request the supplied baseline.
package sandbox

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ResolveEffectiveConfig returns the Config to build a tenant's sandbox manager
// from, or an error when the stored config is unsafe (ErrUnsafeOutboundURL) or
// incomplete (ErrSandboxConfigIncomplete).
//
// The overrideX helpers still read as "override" below even though the provider
// fields were just cleared: they assign only non-empty values, which is exactly
// what is needed to let an omitted TTL fall through to its built-in default.
func ResolveEffectiveConfig(
	tenantCfg *types.TenantSandboxConfig,
	global *Config,
) (*Config, error) {
	if global == nil {
		return nil, fmt.Errorf("sandbox: global config is required")
	}
	effective := *global
	if tenantCfg == nil {
		effective.Network = resolveNetworkPolicy(nil)
		return &effective, nil
	}
	// Keep the baseline's cross-cutting settings, drop everything provider
	// scoped: from here on the stored config is the only source for endpoints,
	// credentials, domains and templates.
	clearProviderFields(&effective)

	if tenantCfg.SandboxType != "" {
		resolved, err := ParseSandboxType(tenantCfg.SandboxType)
		if err != nil {
			return nil, err
		}
		effective.Type = resolved
	}
	overrideSeconds(&effective.DefaultTimeout, tenantCfg.DefaultTimeoutSec)
	effective.AllowPrivateEndpoints = tenantCfg.AllowPrivateEndpoints
	effective.Network = resolveNetworkPolicy(tenantCfg.Network)
	if tenantCfg.EnvVars != nil {
		effective.EnvVars = cloneMetadata(tenantCfg.EnvVars)
	}
	if cube := tenantCfg.Cube; cube != nil {
		if err := overrideURL(&effective.CubeAPIURL, cube.APIURL, effective.AllowPrivateEndpoints); err != nil {
			return nil, err
		}
		if err := overrideURL(&effective.CubeProxyURL, cube.ProxyURL, effective.AllowPrivateEndpoints); err != nil {
			return nil, err
		}
		overrideString(&effective.CubeSandboxDomain, cube.SandboxDomain)
		overrideString(&effective.CubeAPIKey, cube.APIKey)
		overrideString(&effective.CubeTemplate, cube.TemplateID)
		overrideSeconds(&effective.CubeHTTPTimeout, cube.HTTPTimeoutSec)
		overrideSeconds(&effective.CubeSandboxTTL, cube.CubeSandboxTTLSeconds)
		dns, err := NormalizeCubeDNSServers(cube.DNSServers)
		if err != nil {
			return nil, err
		}
		effective.CubeDNSServers = dns
	}

	if e2bCfg := tenantCfg.E2B; e2bCfg != nil {
		if err := overrideURL(&effective.E2BAPIURL, e2bCfg.APIURL, effective.AllowPrivateEndpoints); err != nil {
			return nil, err
		}
		if err := overrideURL(&effective.E2BProxyURL, e2bCfg.ProxyURL, effective.AllowPrivateEndpoints); err != nil {
			return nil, err
		}
		overrideString(&effective.E2BSandboxDomain, e2bCfg.SandboxDomain)
		overrideString(&effective.E2BAPIKey, e2bCfg.APIKey)
		overrideString(&effective.E2BTemplate, e2bCfg.TemplateID)
		overrideSeconds(&effective.E2BHTTPTimeout, e2bCfg.HTTPTimeoutSec)
		overrideSeconds(&effective.E2BSandboxTTL, e2bCfg.E2BSandboxTTLSeconds)
	}

	if docker := tenantCfg.Docker; docker != nil {
		overrideString(&effective.DockerImage, docker.Image)
		if err := ValidateDockerNetworkMode(docker.NetworkMode); err != nil {
			return nil, err
		}
		overrideString(&effective.DockerHost, docker.Host)
		overrideString(&effective.DockerTLSCertPath, docker.TLSCertPath)
		overrideString(&effective.DockerNetworkMode, docker.NetworkMode)
		overrideString(&effective.DockerRuntime, docker.Runtime)
		if docker.CPULimit > 0 {
			effective.DockerCPULimit = docker.CPULimit
		}
		if docker.MemoryLimitMB > 0 {
			effective.DockerMemoryBytes = int64(docker.MemoryLimitMB) * 1024 * 1024
		}
		if docker.PidsLimit > 0 {
			effective.DockerPidsLimit = int64(docker.PidsLimit)
		}
		overrideSeconds(&effective.DockerIdleTTL, docker.IdleTTLSeconds)
		overrideSeconds(&effective.DockerHTTPTimeout, docker.HTTPTimeoutSec)
	}

	switch effective.Type {
	case SandboxTypeCube:
		applyCubeRuntimeDefaults(&effective)
	case SandboxTypeE2B:
		applyE2BRuntimeDefaults(&effective)
	case SandboxTypeDocker:
		applyDockerRuntimeDefaults(&effective)
	}
	// A skill snapshot is a template ID (Cube/E2B) or an image tag (Docker),
	// so overriding that field here is the entire session-side change.
	// Everything downstream keeps reading CubeTemplate / E2BTemplate /
	// DockerImage and needs no knowledge of skills.
	switch effective.Type {
	case SandboxTypeCube:
		if snapshot := skillImageTemplateOverride(
			tenantCfg.SkillImage, "cube", effective.CubeAPIKey, effective.CubeAPIURL,
		); snapshot != "" {
			effective.CubeTemplate = snapshot
		}
	case SandboxTypeE2B:
		if snapshot := skillImageTemplateOverride(
			tenantCfg.SkillImage, "e2b", effective.E2BAPIKey, effective.E2BAPIURL,
		); snapshot != "" {
			effective.E2BTemplate = snapshot
		}
	case SandboxTypeDocker:
		// Deliberately computed from the STORED docker block, not from
		// effective.DockerHost: a blank host is resolved from the environment
		// by applyDockerRuntimeDefaults, and that resolved value must never
		// reach the fingerprint (see dockerLocalDaemonIdentity).
		if snapshot := DockerSkillImageOverride(tenantCfg); snapshot != "" {
			effective.DockerImage = snapshot
		}
	}
	// Deliberately after the runtime defaults: TTLs and HTTP timeouts have
	// built-in fallbacks, endpoints and credentials do not.
	if err := RequireCompleteConfig(&effective); err != nil {
		return nil, err
	}
	// The daemon endpoint is judged on the RESOLVED host, which is why this
	// cannot move up next to the other Docker fields: applyDockerRuntimeDefaults
	// is what fills a blank host in from DOCKER_HOST or the current docker
	// context, and that value is what this config will actually dial. Checking
	// only what the admin typed would let a deployment whose DOCKER_HOST is a
	// plaintext tcp:// daemon save a config that then fails at its first
	// sandbox, with an error the settings form never had a chance to show.
	// It runs after RequireCompleteConfig so a missing image — a field the
	// admin can see and fix — is still the first thing reported.
	if effective.Type == SandboxTypeDocker {
		if err := ValidateDockerHost(
			effective.DockerHost, effective.AllowPrivateEndpoints,
		); err != nil {
			return nil, err
		}
		if err := ValidateDockerRemoteTLS(
			effective.DockerHost, effective.DockerTLSCertPath,
		); err != nil {
			return nil, err
		}
		applyDockerNetworkPolicy(&effective)
	}
	return &effective, nil
}

// clearProviderFields removes every provider-scoped value the deployment
// baseline carries so a named config cannot silently inherit one. TTLs and HTTP
// timeouts are cleared too: leaving them empty must fall back to the built-in
// default rather than to whatever this deployment happens to run, otherwise
// "inherits nothing" would still have an exception to explain.
func clearProviderFields(cfg *Config) {
	cfg.DockerImage = ""
	cfg.DockerHost = ""
	cfg.DockerTLSCertPath = ""
	cfg.DockerNetworkMode = ""
	cfg.DockerRuntime = ""
	cfg.DockerCPULimit = 0
	cfg.DockerMemoryBytes = 0
	cfg.DockerPidsLimit = 0
	cfg.DockerIdleTTL = 0
	cfg.DockerHTTPTimeout = 0
	cfg.CubeAPIURL = ""
	cfg.CubeProxyURL = ""
	cfg.CubeSandboxDomain = ""
	cfg.CubeAPIKey = ""
	cfg.CubeTemplate = ""
	cfg.CubeSandboxTTL = 0
	cfg.CubeHTTPTimeout = 0
	cfg.CubeDNSServers = nil

	cfg.E2BAPIURL = ""
	cfg.E2BProxyURL = ""
	cfg.E2BSandboxDomain = ""
	cfg.E2BAPIKey = ""
	cfg.E2BTemplate = ""
	cfg.E2BSandboxTTL = 0
	cfg.E2BHTTPTimeout = 0
	cfg.Network = RemoteNetworkPolicy{}
}

// ErrUnsupportedSandboxType marks a sandbox type string we cannot honour. It is
// a sentinel so callers can classify it as bad input without matching on the
// message text.
var ErrUnsupportedSandboxType = errors.New("sandbox: unsupported sandbox type")

// ParseSandboxType maps a stored string onto a SandboxType. Unknown values are
// rejected so a typo surfaces when the admin saves the config, instead of
// silently disabling that tenant's sandbox at first use.
func ParseSandboxType(raw string) (SandboxType, error) {
	switch SandboxType(raw) {
	case SandboxTypeCube:
		return SandboxTypeCube, nil
	case SandboxTypeE2B:
		return SandboxTypeE2B, nil
	case SandboxTypeDocker:
		return SandboxTypeDocker, nil
	case SandboxTypeDisabled:
		return SandboxTypeDisabled, nil
	default:
		return "", fmt.Errorf("%w %q", ErrUnsupportedSandboxType, raw)
	}
}

// EffectiveTemplateID returns the template the given provider will use.
func EffectiveTemplateID(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	switch cfg.Type {
	case SandboxTypeCube:
		return cfg.CubeTemplate
	case SandboxTypeE2B:
		return cfg.E2BTemplate
	case SandboxTypeDocker:
		// The image is what a template ID is for the MicroVM backends: the
		// pre-baked filesystem a sandbox starts from.
		return cfg.DockerImage
	default:
		return ""
	}
}

func overrideString(dst *string, value string) {
	if value != "" {
		*dst = value
	}
}

// overrideURL is overrideString for endpoint fields: a tenant-supplied URL
// must pass the SSRF guard before it is accepted into the effective config.
func overrideURL(dst *string, value string, allowPrivate bool) error {
	if value == "" {
		return nil
	}
	if err := ValidateOutboundURLWithPolicy(value, OutboundURLPolicy{AllowPrivate: allowPrivate}); err != nil {
		return err
	}
	*dst = value
	return nil
}

func overrideSeconds(dst *time.Duration, seconds int) {
	if seconds > 0 {
		*dst = time.Duration(seconds) * time.Second
	}
}

// resolveNetworkPolicy turns the stored, admin-facing policy into the
// provider-facing one. This is the single place the inversions happen:
//
//   - DenyEgressByDefault -> AllowInternetAccess=false
//   - CubeEgressRule.Deny -> RemoteCubeEgressRule.Allow
//
// Inbound is always closed (AllowPublicTraffic=false). Stored
// AllowPublicInbound is dropped at the persistence boundary already
// (mergeNetworkPolicyForUpdate); ignoring it again here means even a caller
// that reaches this function without going through that merge cannot open
// the sandbox URL.
//
// A nil stored policy is not "unset": it resolves to WeKnora's default of
// egress allowed and inbound closed, so every downstream consumer sees one
// fully specified policy and nobody re-derives the default.
func resolveNetworkPolicy(stored *types.SandboxNetworkPolicy) RemoteNetworkPolicy {
	allowEgress := true
	inboundPublic := false
	if stored != nil {
		allowEgress = !stored.DenyEgressByDefault
	}
	policy := RemoteNetworkPolicy{
		AllowInternetAccess: &allowEgress,
		AllowPublicTraffic:  &inboundPublic,
	}
	if stored == nil {
		return policy
	}
	policy.AllowOut = append([]string(nil), stored.AllowOut...)
	// Canonicalise first: any IPv4 /0 (including 1.2.3.4/0) collapses onto
	// 0.0.0.0/0, which is the spelling E2B string-matches as ALL_TRAFFIC.
	// Leaving a non-canonical /0 on the wire would pass validation and then
	// fail create, or accept a domain allow-list without actually denying the
	// rest of the internet.
	policy.DenyOut = types.CanonicalizeDenyOut(stored.DenyOut)
	// DenyEgressByDefault means "install a 0.0.0.0/0 deny-all" — that is the
	// stored field's documented definition — so materialise it as a real deny
	// entry instead of leaving it implied by the top-level switch. E2B
	// validates the two independently and rejects a create whose allowOut
	// names a domain unless denyOut carries the entry:
	//
	//	400 When specifying allowed domains in allow out, you must include
	//	'ALL_TRAFFIC' in deny out to block all other traffic.
	//
	// allow_internet_access=false does not satisfy it. Doing this here rather
	// than in the E2B adapter keeps the neutral policy self-consistent, so
	// every adapter and every reader of DenyOut sees the same deny-all the
	// admin asked for.
	if stored.DenyEgressByDefault && !types.DenyOutCoversAllIPv4(policy.DenyOut) {
		policy.DenyOut = append(policy.DenyOut, types.DenyAllIPv4)
	}

	for _, rule := range stored.CubeRules {
		converted := RemoteCubeEgressRule{
			Name:    rule.Name,
			Scheme:  rule.Scheme,
			SNI:     rule.SNI,
			Host:    rule.Host,
			Methods: append([]string(nil), rule.Methods...),
			Path:    rule.Path,
			Allow:   !rule.Deny,
			Audit:   rule.Audit,
		}
		for _, inject := range rule.Inject {
			converted.Inject = append(converted.Inject, RemoteHeaderInject{
				Header: inject.Header,
				Secret: inject.Secret,
				Format: inject.Format,
			})
		}
		policy.CubeRules = append(policy.CubeRules, converted)
	}
	for _, rule := range stored.E2BHostRules {
		converted := RemoteE2BHostRule{Host: rule.Host}
		if len(rule.Headers) > 0 {
			converted.Headers = make(map[string]string, len(rule.Headers))
			for name, value := range rule.Headers {
				converted.Headers[name] = value
			}
		}
		policy.E2BHostRules = append(policy.E2BHostRules, converted)
	}
	return policy
}

// applyDockerNetworkPolicy maps docker.network_mode=none onto the resolved
// egress switch so DeniesEgressByDefault (and the deep connectivity check)
// agree with the network the adapter will actually create. The stored
// SandboxNetworkPolicy stays empty on Docker configs; this is the Docker
// form's overall switch expressed in the same field the rest of the stack
// already consults.
func applyDockerNetworkPolicy(cfg *Config) {
	if cfg == nil {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.DockerNetworkMode), "none") {
		return
	}
	allow := false
	cfg.Network.AllowInternetAccess = &allow
}

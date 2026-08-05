// Package sandbox: what makes an existing sandbox operable.
//
// Two groups of configuration fields decide whether the sandboxes a config has
// already created can still be acted on. This file names them in one place,
// next to the merge logic they must mirror, because the answer depends on which
// SDK carries which value:
//
//   - Control plane (API URL, API key): every lifecycle call - list, delete,
//     pause, resume, refresh timeout - is issued against it. Lose it and the
//     sandboxes can no longer be reclaimed at all.
//   - Data plane (sandbox domain, and for Cube the proxy endpoint): envd traffic
//     is routed through it. Lose it and the sandboxes are still deletable but no
//     longer usable, so every live session on the config fails at once.
//
// The split is a property of the clients in use, not a universal truth:
// github.com/matiasinsaurralde/go-e2b resolves the API base URL and the sandbox
// domain independently, whereas E2B's own JS/Python SDKs derive the API base from
// the domain (https://api.${domain}). Swapping that dependency would move the
// domain into the control-plane group.
package sandbox

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// SandboxIdentity is the comparable projection of a config: two configs with
// equal identities can operate each other's sandboxes.
type SandboxIdentity struct {
	Provider string

	// Control plane - whether an existing sandbox can still be reclaimed.
	APIURL string
	APIKey string

	// Data plane - whether an existing sandbox can still be used.
	SandboxDomain string
	ProxyURL      string
}

// IdentityOf resolves a stored config against the deployment defaults and
// projects out its identity. Only the active provider's fields are read: a
// sub-struct left behind by an earlier provider switch says nothing about where
// today's sandboxes live.
//
// Unlike ResolveEffectiveConfig this runs no SSRF guard and returns no error. It
// answers "would this edit strand anything", a question that must stay
// answerable when the OLD endpoint no longer resolves — precisely the situation
// in which an admin needs to re-point the config. Validating the incoming URLs
// remains the save path's job.
func IdentityOf(tenantCfg *types.TenantSandboxConfig, global *Config) SandboxIdentity {
	var effective Config
	if global != nil {
		effective = *global
	}
	if tenantCfg != nil {
		// Unknown type strings are compared verbatim rather than rejected:
		// a typo yields an identity that matches nothing, which errs toward
		// refusing the edit. ParseSandboxType reports the typo on save.
		overrideString((*string)(&effective.Type), tenantCfg.SandboxType)
		if cube := tenantCfg.Cube; cube != nil {
			overrideString(&effective.CubeAPIURL, cube.APIURL)
			overrideString(&effective.CubeProxyURL, cube.ProxyURL)
			overrideString(&effective.CubeSandboxDomain, cube.SandboxDomain)
			overrideString(&effective.CubeAPIKey, cube.APIKey)
		}
		if e2bCfg := tenantCfg.E2B; e2bCfg != nil {
			overrideString(&effective.E2BAPIURL, e2bCfg.APIURL)
			overrideString(&effective.E2BSandboxDomain, e2bCfg.SandboxDomain)
			overrideString(&effective.E2BAPIKey, e2bCfg.APIKey)
		}
	}

	identity := SandboxIdentity{Provider: string(effective.Type)}
	switch effective.Type {
	case SandboxTypeCube:
		// Same defaults the client will really be built with, so an admin who
		// types out the built-in value is not mistaken for changing backends.
		applyCubeDefaults(&effective)
		identity.APIURL, identity.APIKey = effective.CubeAPIURL, effective.CubeAPIKey
		identity.SandboxDomain = effective.CubeSandboxDomain
		identity.ProxyURL = effective.CubeProxyURL
	case SandboxTypeE2B:
		identity.APIURL, identity.APIKey = effective.E2BAPIURL, effective.E2BAPIKey
		identity.SandboxDomain = effective.E2BSandboxDomain
	}
	return identity
}

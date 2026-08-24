// Package sandbox: what makes an existing sandbox operable.
//
// Two groups of configuration fields decide whether the sandboxes a config has
// already created can still be acted on. This file names them in one place
// because the answer depends on which SDK carries which value:
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
	Provider              string
	AllowPrivateEndpoints bool

	// Control plane - whether an existing sandbox can still be reclaimed.
	APIURL string
	APIKey string

	// Data plane - whether an existing sandbox can still be used.
	SandboxDomain string
	ProxyURL      string
}

// IdentityOf projects a stored config onto its identity. Only the active
// provider's fields are read: a sub-struct left behind by an earlier provider
// switch says nothing about where today's sandboxes live.
//
// Because named configs inherit nothing, the stored row is the whole picture and
// no deployment baseline is consulted. Unknown type strings are compared verbatim
// rather than rejected: a typo yields an identity that matches nothing, which
// errs toward refusing the edit, and ParseSandboxType reports it on save.
//
// This runs no SSRF guard and returns no error on purpose. It answers "would this
// edit strand anything", a question that must stay answerable when the OLD
// endpoint no longer resolves — precisely the situation in which an admin needs
// to re-point the config. Validating the incoming URLs is the save path's job.
func IdentityOf(tenantCfg *types.TenantSandboxConfig) SandboxIdentity {
	if tenantCfg == nil {
		return SandboxIdentity{}
	}
	identity := SandboxIdentity{
		Provider:              tenantCfg.SandboxType,
		AllowPrivateEndpoints: tenantCfg.AllowPrivateEndpoints,
	}
	switch SandboxType(tenantCfg.SandboxType) {
	case SandboxTypeCube:
		if cube := tenantCfg.Cube; cube != nil {
			identity.APIURL, identity.APIKey = cube.APIURL, cube.APIKey
			identity.SandboxDomain = cube.SandboxDomain
			identity.ProxyURL = cube.ProxyURL
		}
	case SandboxTypeE2B:
		if e2bCfg := tenantCfg.E2B; e2bCfg != nil {
			identity.APIURL, identity.APIKey = e2bCfg.APIURL, e2bCfg.APIKey
			identity.SandboxDomain = e2bCfg.SandboxDomain
			identity.ProxyURL = e2bCfg.ProxyURL
		}
	}
	return identity
}

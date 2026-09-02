// Package sandbox: inbound traffic-token registry.
//
// A sandbox created with public inbound access closed only accepts requests
// carrying its per-sandbox traffic token. Cube's SDK attaches that header
// itself once Sandbox.TrafficAccessToken is set, but go-e2b stores the token
// and never sends it — so for E2B the header has to be added by the one
// component WeKnora owns on that path: the data-plane RoundTripper.
//
// This registry is the lookup that makes it possible. It is process-local and
// lost on restart, which is fine: the first reconnect after a restart carries
// the token up from the session binding and re-registers it.
package sandbox

import (
	"strconv"
	"strings"
	"sync"
)

// InboundTokenHeader is the header both providers accept. Cube additionally
// accepts "cube-traffic-access-token"; sending the E2B spelling covers both.
const InboundTokenHeader = "e2b-traffic-access-token"

// InboundTokenRegistry maps a sandbox ID to its inbound credential.
type InboundTokenRegistry struct {
	tokens sync.Map // sandboxID -> string
}

// NewInboundTokenRegistry returns an empty registry.
func NewInboundTokenRegistry() *InboundTokenRegistry {
	return &InboundTokenRegistry{}
}

// Put records the token for sandboxID. An empty token means the sandbox has
// public inbound access, so nothing is stored.
func (r *InboundTokenRegistry) Put(sandboxID, token string) {
	if r == nil || sandboxID == "" || token == "" {
		return
	}
	r.tokens.Store(strings.ToLower(sandboxID), token)
}

// Get returns the token for sandboxID, or "" when none is known.
func (r *InboundTokenRegistry) Get(sandboxID string) string {
	if r == nil || sandboxID == "" {
		return ""
	}
	if value, ok := r.tokens.Load(strings.ToLower(sandboxID)); ok {
		return value.(string)
	}
	return ""
}

// Delete forgets sandboxID. Called when the sandbox is destroyed.
func (r *InboundTokenRegistry) Delete(sandboxID string) {
	if r == nil || sandboxID == "" {
		return
	}
	r.tokens.Delete(strings.ToLower(sandboxID))
}

// SandboxIDFromDataPlaneHost extracts the sandbox ID from an envd authority of
// the form "<port>-<sandboxID>.<domain>", or "" when host is not one.
//
// Matching on this shape rather than on the configured sandbox domain is
// deliberate: an E2B Cloud deployment may leave sandbox_domain empty and let
// the SDK resolve its own default, and a domain-based check would then never
// fire. The registry lookup is the real authority — only sandboxes WeKnora
// created with inbound closed are in it.
func SandboxIDFromDataPlaneHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if idx := strings.LastIndex(host, ":"); idx != -1 && !strings.Contains(host[idx:], "]") {
		host = host[:idx]
	}
	label, _, found := strings.Cut(host, ".")
	if !found {
		return ""
	}
	port, sandboxID, found := strings.Cut(label, "-")
	if !found || sandboxID == "" {
		return ""
	}
	if _, err := strconv.Atoi(port); err != nil {
		return ""
	}
	return sandboxID
}

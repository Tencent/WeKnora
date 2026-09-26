package pluginapi

import "encoding/json"

// UIRequestPath answers requests from a plugin's pages. Pages run in a
// sandboxed iframe that can reach nothing itself; what they send through
// the WeKnora bridge (api.request) arrives here, with the caller's context
// and configuration in the envelope.
const UIRequestPath = "/v1/ui/request"

// UIRequest is one request a plugin page made.
type UIRequest struct {
	// Mount is the page it came from, as "<point>/<id>" ("pages/links").
	Mount string `json:"mount"`
	// Method and Path are the page's own routing: WeKnora does not
	// interpret them. Path starts with "/".
	Method string `json:"method"`
	Path   string `json:"path"`
	// Body is the JSON the page sent, if any.
	Body json.RawMessage `json:"body,omitempty"`
	// Role is the caller's workspace role: viewer, contributor, admin or
	// owner. WeKnora already refused callers below the mount's minRole.
	Role string `json:"role,omitempty"`
}

// UIResponse is what the page receives.
type UIResponse struct {
	// Status is an HTTP-style status for the page (200 when zero).
	Status int             `json:"status,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

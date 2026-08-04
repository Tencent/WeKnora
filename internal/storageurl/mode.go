package storageurl

import (
	"fmt"
	"strings"
)

// Mode selects how stored files are referenced in an API response.
type Mode string

const (
	// ModeHandle returns the internal `resource://<handle>` reference. Clients
	// must fetch the bytes through the authenticated `/files` proxy. This is the
	// default and the only mode that never depends on external reachability.
	ModeHandle Mode = "handle"
	// ModePublic returns a time-limited HTTP(S) URL the client can load
	// directly, so third-party apps can render images without a second
	// authenticated call. References that cannot be turned into an HTTP URL are
	// left as handles.
	ModePublic Mode = "public"
)

// QueryParam is the request query parameter that selects the mode per call.
const QueryParam = "resource_urls"

// ParseMode validates a mode supplied by a client or by configuration. An empty
// value yields ModeHandle so an unset parameter or setting keeps the default.
func ParseMode(raw string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(raw))) {
	case "":
		return ModeHandle, nil
	case ModeHandle:
		return ModeHandle, nil
	case ModePublic:
		return ModePublic, nil
	default:
		return ModeHandle, fmt.Errorf(
			"invalid %s value %q: expected %q or %q", QueryParam, raw, ModeHandle, ModePublic)
	}
}

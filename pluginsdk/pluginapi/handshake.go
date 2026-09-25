package pluginapi

import (
	"fmt"
	"strings"
)

// Environment a host plugin is started with.
const (
	// EnvSocket is where the plugin must listen (a unix socket path, or a
	// host:port when EnvNetwork is "tcp").
	EnvSocket = "WEKNORA_PLUGIN_SOCKET"
	// EnvNetwork is "unix" (default) or "tcp".
	EnvNetwork = "WEKNORA_PLUGIN_NETWORK"
	// EnvToken is the bearer token every call from the host carries.
	EnvToken = "WEKNORA_PLUGIN_TOKEN"
	// EnvPluginID and EnvPluginVersion name the package being run.
	EnvPluginID      = "WEKNORA_PLUGIN_ID"
	EnvPluginVersion = "WEKNORA_PLUGIN_VERSION"
	// EnvAddr and EnvSecret configure a remote plugin (not started by a
	// host): where to listen and the shared secret calls are signed with.
	EnvAddr   = "WEKNORA_PLUGIN_ADDR"
	EnvSecret = "WEKNORA_PLUGIN_SECRET"
)

// handshakePrefix starts the line a host plugin prints on stdout once it is
// ready: WEKNORA_PLUGIN|1|unix|/path/to.sock
const handshakePrefix = "WEKNORA_PLUGIN"

// Handshake is the readiness line of a host plugin.
type Handshake struct {
	Protocol string
	Network  string // unix | tcp
	Address  string
}

// String renders the handshake line (without a newline).
func (h Handshake) String() string {
	return strings.Join([]string{handshakePrefix, h.Protocol, h.Network, h.Address}, "|")
}

// ParseHandshake reads a handshake line. ok is false for any other line,
// which the host treats as log output.
func ParseHandshake(line string) (h Handshake, ok bool, err error) {
	parts := strings.Split(strings.TrimSpace(line), "|")
	if len(parts) == 0 || parts[0] != handshakePrefix {
		return Handshake{}, false, nil
	}
	if len(parts) != 4 {
		return Handshake{}, true, fmt.Errorf("malformed handshake %q", line)
	}
	h = Handshake{Protocol: parts[1], Network: parts[2], Address: parts[3]}
	if h.Protocol != ProtocolVersion {
		return h, true, fmt.Errorf("plugin speaks protocol %s, host speaks %s", h.Protocol, ProtocolVersion)
	}
	if h.Network != "unix" && h.Network != "tcp" {
		return h, true, fmt.Errorf("unknown handshake network %q", h.Network)
	}
	if h.Address == "" {
		return h, true, fmt.Errorf("handshake has no address")
	}
	return h, true, nil
}

package plugin

import "context"

// StartPluginRequest carries everything the OCI lifecycle executor needs to
// launch one plugin container. A remote runtime-agent re-validates every field
// rather than trusting the caller, so these are full launch parameters, not a
// reference to app-side state. JSON tags make it wire-compatible with the
// runtime-agent HTTP API.
type StartPluginRequest struct {
	PluginID            string        `json:"plugin_id"`
	Image               string        `json:"image"`
	ExtensionType       string        `json:"extension_type"`
	ProtocolVersion     string        `json:"protocol_version"`
	NetworkPolicy       NetworkPolicy `json:"network_policy"`
	AllowedDestinations []string      `json:"allowed_destinations,omitempty"`
}

// PluginHandle is what the executor returns after a successful start. The app
// connects to ControlSocket to reach the plugin's control plane; ContainerName
// is the deterministic container identity used by Stop.
type PluginHandle struct {
	PluginID      string `json:"plugin_id"`
	ControlSocket string `json:"control_socket"`
	ContainerName string `json:"container_name"`
}

// PluginRuntimeController is the narrow lifecycle surface the app uses to run
// OCI plugins. It deliberately contains only start/stop: the app keeps the gRPC
// connection, the handshake, health probing and the Runtime interface, so the
// five extension adapters never learn whether the executor is local (dev,
// single-process) or a remote runtime-agent (production, Docker privileges
// separated from the API process).
type PluginRuntimeController interface {
	Start(ctx context.Context, req StartPluginRequest) (*PluginHandle, error)
	Stop(ctx context.Context, pluginID string) error
}

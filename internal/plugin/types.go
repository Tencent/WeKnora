package plugin

// Package plugin contains the provider-independent control plane for WeKnora
// extensions. Connector-specific protocols are deliberately kept out of this
// package so the lifecycle manager can manage every extension type uniformly.

import (
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	APIVersionV1        = "weknora.plugin/v1"
	ProtocolVersionV1   = "v1"
	ExtensionDataSource = "datasource"
	ExtensionParser     = "parser"
	ExtensionSearch     = "search"
	ExtensionModel      = "model"
	ExtensionRetriever  = "retriever"
)

// NetworkPolicy describes the egress boundary the host must enforce for a
// plugin. "none" is the safe default; "egress" means a controlled broker or
// sandbox policy; "allowlist" restricts destinations to AllowedDestinations.
type NetworkPolicy string

const (
	NetworkNone      NetworkPolicy = "none"
	NetworkAllowlist NetworkPolicy = "allowlist"
)

// Permissions are declarative input to the runtime sandbox. A manifest is not
// trusted merely because it declares a permission: the runtime must enforce it.
type Permissions struct {
	Network             NetworkPolicy `json:"network" yaml:"network"`
	ReadPaths           []string      `json:"read_paths,omitempty" yaml:"read_paths,omitempty"`
	WritePaths          []string      `json:"write_paths,omitempty" yaml:"write_paths,omitempty"`
	AllowedDestinations []string      `json:"allowed_destinations,omitempty" yaml:"allowed_destinations,omitempty"`
	Secrets             []string      `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	// Data describes the host data scope the plugin is intended to handle.
	// "self" means the tenant/knowledge-base/data-source associated with the
	// current invocation; explicit IDs may be used by an administrator when a
	// plugin is intentionally restricted to a fixed set of resources.
	Data *DataPermissions `json:"data,omitempty" yaml:"data,omitempty"`
}

type DataPermissions struct {
	Tenants        []string `json:"tenants,omitempty" yaml:"tenants,omitempty"`
	KnowledgeBases []string `json:"knowledge_bases,omitempty" yaml:"knowledge_bases,omitempty"`
	DataSources    []string `json:"data_sources,omitempty" yaml:"data_sources,omitempty"`
}

// Manifest is the stable package contract. Entrypoint is interpreted by the
// selected runtime (for example a process path or OCI image); lifecycle code
// never embeds a plugin ID-specific branch.
type Manifest struct {
	APIVersion      string `json:"api_version" yaml:"api_version"`
	ID              string `json:"id" yaml:"id"`
	Name            string `json:"name" yaml:"name"`
	Version         string `json:"version" yaml:"version"`
	ExtensionType   string `json:"extension_type" yaml:"extension_type"`
	ProtocolVersion string `json:"protocol_version" yaml:"protocol_version"`
	WeKnoraVersion  string `json:"weknora_version" yaml:"weknora_version"`
	Entrypoint      string `json:"entrypoint,omitempty" yaml:"entrypoint,omitempty"`
	// ConfigSchema is the single configuration declaration for the plugin: a
	// JSON-Schema-like object split into sections (settings / credentials, plus
	// index_config for retriever). It is validated at load time and drives the
	// dynamic settings UI for external plugins.
	ConfigSchema map[string]any `json:"config_schema,omitempty" yaml:"config_schema,omitempty"`
	Permissions  Permissions    `json:"permissions" yaml:"permissions"`
	Capabilities []string       `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`

	// ModelUI is the model-plugin-only UI declaration. It declares additive
	// capabilities (features) and the display policy of host-owned common
	// fields (host_fields). It is only meaningful for extension_type=model.
	ModelUI *ModelUI `json:"model_ui,omitempty" yaml:"model_ui,omitempty"`

	// SourceDir is the directory containing the plugin manifest, populated by
	// discovery. It is never serialized to/from the manifest file and is used by
	// the host to resolve plugin-local assets (e.g. the connector icon).
	SourceDir string `json:"-" yaml:"-"`
}

// ModelUI carries the model-plugin UI declaration from the manifest. It is
// surfaced to the frontend (via /models/providers) so the model editor can
// render a plugin-specific form without any host-side hardcoding.
type ModelUI struct {
	// Features declares additive model capabilities that are not themselves a
	// model type (e.g. "thinking", "streaming", "vision", "tools"). They drive
	// capability badges and the visibility of feature toggles in the editor.
	Features []string `json:"features,omitempty" yaml:"features,omitempty"`
	// HostFields declares the display policy of host-owned common fields
	// (base_url / api_key / custom_headers / supports_vision / max_concurrency).
	// Keys use snake_case, matching the config_schema convention.
	HostFields map[string]types.HostFieldSpec `json:"host_fields,omitempty" yaml:"host_fields,omitempty"`
}

type HealthState string

const (
	StateDiscovered HealthState = "discovered"
	StateStarting   HealthState = "starting"
	StateRunning    HealthState = "running"
	StateDraining   HealthState = "draining"
	StateDegraded   HealthState = "degraded"
	StateUnhealthy  HealthState = "unhealthy"
	StateStopped    HealthState = "stopped"
	StateFailed     HealthState = "failed"
)

type HealthStatus struct {
	State      HealthState `json:"state"`
	Message    string      `json:"message,omitempty"`
	CheckedAt  time.Time   `json:"checked_at"`
	Generation uint64      `json:"generation"`
}

type PluginInfo struct {
	Manifest Manifest     `json:"manifest"`
	State    HealthStatus `json:"state"`
}

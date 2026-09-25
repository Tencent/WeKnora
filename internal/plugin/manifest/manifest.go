// Package manifest defines the plugin manifest: what a plugin is, how it runs,
// what it may touch and what it contributes. Builtin capabilities are
// described with the same type, so the rest of WeKnora sees one shape whether
// a connector is compiled in or shipped by a third party.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

// SchemaVersion is the manifest format version this build understands.
const SchemaVersion = 1

// BuiltinPublisher is the publisher of every plugin compiled into WeKnora.
// Other manifests may not claim it.
const BuiltinPublisher = "weknora"

// RuntimeType says where a plugin's code runs.
type RuntimeType string

const (
	// RuntimeBuiltin runs compiled into WeKnora. Only builtin manifests use it.
	RuntimeBuiltin RuntimeType = "builtin"
	// RuntimeDeclarative has no code; WeKnora interprets the manifest itself.
	RuntimeDeclarative RuntimeType = "declarative"
	// RuntimeHost runs as a child process of the WeKnora plugin host.
	RuntimeHost RuntimeType = "host"
	// RuntimeRemote is an HTTP service somewhere else, registered by URL.
	RuntimeRemote RuntimeType = "remote"
	// RuntimeKubernetes runs as a Deployment + Service managed through the k8s API.
	RuntimeKubernetes RuntimeType = "kubernetes"
)

// Manifest is a plugin's plugin.yaml.
type Manifest struct {
	SchemaVersion int    `json:"schemaVersion"        yaml:"schemaVersion"`
	ID            string `json:"id"                   yaml:"id"`
	Version       string `json:"version"              yaml:"version"`
	// APIVersion is the extension protocol a code plugin speaks
	// (weknora.plugin/v1). Builtin and declarative plugins leave it empty.
	APIVersion  string        `json:"apiVersion,omitempty" yaml:"apiVersion"`
	Engines     Engines       `json:"engines,omitzero"     yaml:"engines"`
	Name        LocalizedText `json:"name"                 yaml:"name"`
	Description LocalizedText `json:"description,omitzero" yaml:"description"`
	Publisher   Publisher     `json:"publisher"            yaml:"publisher"`
	Icon        string        `json:"icon,omitempty"       yaml:"icon"`
	Homepage    string        `json:"homepage,omitempty"   yaml:"homepage"`
	License     string        `json:"license,omitempty"    yaml:"license"`

	// Builtin marks a plugin compiled into WeKnora. It cannot be set from a
	// manifest file.
	Builtin bool `json:"builtin,omitempty" yaml:"-"`
	// Required marks a builtin a tenant may not disable (the core agent
	// tools, the default parser).
	Required bool `json:"required,omitempty" yaml:"-"`

	Runtime     Runtime       `json:"runtime"               yaml:"runtime"`
	Permissions Permissions   `json:"permissions,omitzero"  yaml:"permissions"`
	Config      ConfigSchemas `json:"config,omitzero"       yaml:"config"`
	Contributes Contributions `json:"contributes"           yaml:"contributes"`
}

// Engines constrains which WeKnora versions a plugin runs on.
type Engines struct {
	// WeKnora is a semver range such as ">=0.10.0 <1.0.0".
	WeKnora string `json:"weknora,omitempty" yaml:"weknora"`
}

// Publisher identifies who ships a plugin. ID is the first segment of the
// plugin ID.
type Publisher struct {
	ID   string `json:"id"             yaml:"id"`
	Name string `json:"name,omitempty" yaml:"name"`
	URL  string `json:"url,omitempty"  yaml:"url"`
}

// Runtime says how a plugin's code runs.
type Runtime struct {
	Type RuntimeType `json:"type" yaml:"type"`
	// Kind is the process runtime for host plugins: binary, python or node.
	Kind string `json:"kind,omitempty" yaml:"kind"`
	// Entry is the command inside the package, with {os} and {arch}
	// placeholders for binaries.
	Entry string `json:"entry,omitempty" yaml:"entry"`
	// Singleton allows only one live instance across the cluster (long-lived
	// connections such as an IM WebSocket).
	Singleton bool       `json:"singleton,omitempty" yaml:"singleton"`
	Resources *Resources `json:"resources,omitempty" yaml:"resources"`
}

// Resources caps a host plugin process.
type Resources struct {
	CPU    string `json:"cpu,omitempty"    yaml:"cpu"`
	Memory string `json:"memory,omitempty" yaml:"memory"`
}

// Permissions is what a plugin asks for; an administrator confirms it at
// install time.
type Permissions struct {
	// Egress lists host patterns the plugin may reach ("*.atlassian.net").
	Egress []string `json:"egress,omitempty" yaml:"egress"`
	// HostAPI lists WeKnora API scopes the plugin may call back with.
	HostAPI []string `json:"hostApi,omitempty" yaml:"hostApi"`
	// Events lists asynchronous events the plugin subscribes to.
	Events []string `json:"events,omitempty" yaml:"events"`
}

// ConfigSchemas points at the JSON Schemas of plugin-level configuration.
type ConfigSchemas struct {
	System string `json:"system,omitempty" yaml:"system"`
	Tenant string `json:"tenant,omitempty" yaml:"tenant"`
}

// Contributions maps each extension point to what the plugin provides there.
type Contributions map[Point][]Contribution

// Contribution is one capability a plugin provides at one extension point,
// such as one connector.
type Contribution struct {
	// ID is local to the plugin; QualifiedID adds the plugin ID.
	ID          string        `json:"id"                   yaml:"id"`
	Name        LocalizedText `json:"name"                 yaml:"name"`
	Description LocalizedText `json:"description,omitzero" yaml:"description"`
	Icon        string        `json:"icon,omitempty"       yaml:"icon"`
	// Aliases are the short names builtins were stored under before plugins
	// existed ("feishu"). Rows keep them, so lookups must keep resolving
	// them. Only builtins may declare aliases.
	Aliases      []string `json:"aliases,omitempty"      yaml:"-"`
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities"`
	// Order sorts contributions of one point in the UI (lower first); ties
	// keep registration order.
	Order int `json:"order,omitempty" yaml:"order"`
	// InstanceSchema points at the JSON Schema of one instance's
	// configuration (one data source, one IM channel).
	InstanceSchema string `json:"instanceSchema,omitempty" yaml:"instanceSchema"`
	// Extra carries point-specific metadata the generic fields do not cover.
	Extra map[string]any `json:"extra,omitempty" yaml:"extra"`
}

// QualifiedID is the cluster-wide ID of a contribution: "<plugin>/<local>".
func QualifiedID(pluginID, localID string) string {
	return pluginID + "/" + localID
}

var (
	pluginIDPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}\.[a-z0-9][a-z0-9-]{0,62}$`)
	contributionIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)
)

// isStrictSemver accepts full MAJOR.MINOR.PATCH versions with an optional
// pre-release and build suffix. x/mod/semver alone also accepts "1.0".
func isStrictSemver(v string) bool {
	withoutBuild, _, _ := strings.Cut(v, "+")
	return semver.IsValid("v"+v) && semver.Canonical("v"+v) == "v"+withoutBuild
}

// HasValidVersion reports whether Version is a full semantic version.
func (m *Manifest) HasValidVersion() bool { return isStrictSemver(m.Version) }

// Parse decodes a manifest from YAML (JSON is accepted too) and validates it.
// Unknown keys are rejected so a typo does not silently drop a setting.
func Parse(data []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks a manifest. It reports every problem at once so an author
// fixes them in one pass.
func (m *Manifest) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if m.SchemaVersion != SchemaVersion {
		add("schemaVersion must be %d, got %d", SchemaVersion, m.SchemaVersion)
	}
	if !pluginIDPattern.MatchString(m.ID) {
		add("id %q must look like <publisher>.<name> in lowercase letters, digits and dashes", m.ID)
	}
	publisher, _, _ := strings.Cut(m.ID, ".")
	if m.Publisher.ID != publisher {
		add("publisher.id %q must match the id prefix %q", m.Publisher.ID, publisher)
	}
	if publisher == BuiltinPublisher && !m.Builtin {
		add("publisher %q is reserved for builtin plugins", BuiltinPublisher)
	}
	if !isStrictSemver(m.Version) {
		add("version %q is not a semantic version", m.Version)
	}
	if m.Name.IsZero() {
		add("name is required")
	}
	m.validateRuntime(add)
	m.validateContributions(add)
	return errors.Join(errs...)
}

func (m *Manifest) validateRuntime(add func(string, ...any)) {
	switch m.Runtime.Type {
	case RuntimeBuiltin:
		if !m.Builtin {
			add("runtime.type %q is reserved for builtin plugins", RuntimeBuiltin)
		}
	case RuntimeDeclarative, RuntimeRemote, RuntimeKubernetes:
	case RuntimeHost:
		switch m.Runtime.Kind {
		case "binary", "python", "node":
		default:
			add("runtime.kind must be binary, python or node for host plugins, got %q", m.Runtime.Kind)
		}
		if m.Runtime.Entry == "" {
			add("runtime.entry is required for host plugins")
		}
	default:
		add("runtime.type %q is not one of builtin, declarative, host, remote, kubernetes", m.Runtime.Type)
	}
}

func (m *Manifest) validateContributions(add func(string, ...any)) {
	if len(m.Contributes) == 0 {
		add("contributes must declare at least one contribution")
	}
	for point := range m.Contributes {
		if _, ok := LookupPoint(point); !ok {
			add("contributes.%s is not a known extension point", point)
		}
	}
	for _, info := range points {
		point := info.Point
		list, ok := m.Contributes[point]
		if !ok {
			continue
		}
		if !info.ThirdParty && !m.Builtin {
			add("contributes.%s is not open to third-party plugins yet", point)
		}
		seen := make(map[string]bool, len(list))
		for i, c := range list {
			where := fmt.Sprintf("contributes.%s[%d]", point, i)
			if !contributionIDPattern.MatchString(c.ID) {
				add("%s.id %q must be lowercase letters, digits, '_', '-' or '.'", where, c.ID)
			}
			if seen[c.ID] {
				add("%s.id %q is declared twice", where, c.ID)
			}
			seen[c.ID] = true
			if c.Name.IsZero() {
				add("%s.name is required", where)
			}
			if len(c.Aliases) > 0 && !m.Builtin {
				add("%s.aliases may only be declared by builtin plugins", where)
			}
			for _, alias := range c.Aliases {
				// Aliases share the ID alphabet, which has no '/', so an
				// alias can never shadow a qualified ID.
				if !contributionIDPattern.MatchString(alias) {
					add("%s alias %q must be lowercase letters, digits, '_', '-' or '.'", where, alias)
				}
			}
		}
	}
}

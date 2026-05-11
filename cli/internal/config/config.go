// Package config reads and writes the user-level config at
// $XDG_CONFIG_HOME/weknora/config.yaml. yaml.v3 directly; viper is intentionally
// not used (see ADR-2).
//
// v0.0 supports only Load/Save with multi-host context map; project link
// (.weknora/project.toml) is wired in v0.2 (ADR-16).
package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/Tencent/WeKnora/cli/internal/xdg"
)

// Config is the on-disk schema. Empty zero-value is valid (returned when the
// file does not exist) so commands like --help / version don't fail.
type Config struct {
	CurrentContext string             `yaml:"current_context,omitempty"`
	Contexts       map[string]Context `yaml:"contexts,omitempty"`

	// Defaults holds CLI-wide defaults; fields opt-in.
	Defaults struct {
		Format          string `yaml:"format,omitempty"`
		NoVersionCheck  bool   `yaml:"no_version_check,omitempty"`
		RequestIDPrefix string `yaml:"request_id_prefix,omitempty"`
	} `yaml:"defaults,omitempty"`
}

// Context is one named connection target (host + tenant + credential reference).
type Context struct {
	Host        string `yaml:"host"`
	TenantID    uint64 `yaml:"tenant_id,omitempty"`
	User        string `yaml:"user,omitempty"`
	APIKeyRef   string `yaml:"api_key_ref,omitempty"` // keychain://... or file://...
	TokenRef    string `yaml:"token_ref,omitempty"`   // keychain://... or file://...
	RefreshRef  string `yaml:"refresh_token_ref,omitempty"`
	DefaultKBID string `yaml:"default_kb_id,omitempty"`
}

// ErrCorrupt is returned by Load when the file exists but cannot be parsed.
// Callers should map this to error code "local.config_corrupt".
var ErrCorrupt = errors.New("config: file is malformed")

// Path returns the absolute config file path.
// Honors XDG_CONFIG_HOME via internal/xdg.
func Path() (string, error) {
	return xdg.Path("XDG_CONFIG_HOME", ".config", "config.yaml")
}

// Load reads the config file. If it does not exist, returns a zero-value
// Config with no error (commands like `version` and `--help` must not fail
// just because the user has not run `auth login` yet).
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return &c, nil
}

// Save writes the config atomically with mode 0600 via internal/xdg.WriteAtomicYAML.
func Save(c *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	return xdg.WriteAtomicYAML(p, c)
}

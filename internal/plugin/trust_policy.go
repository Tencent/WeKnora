package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// PluginTrustLevel is the deployment trust choice for a plugin. Empty means
// offline so an omitted configuration is fail-closed.
type PluginTrustLevel string

const (
	TrustOffline  PluginTrustLevel = "offline"
	TrustTrusted  PluginTrustLevel = "trusted"
	TrustIsolated PluginTrustLevel = "isolated"
)

// PluginTrustConfig is keyed by plugin ID. It is deployment-scoped for now;
// the existing system_settings service persists the JSON map.
type PluginTrustConfig map[string]PluginTrustLevel

// ParsePluginTrustConfig parses a persisted or deployment-provided JSON map.
func ParsePluginTrustConfig(raw string) (PluginTrustConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PluginTrustConfig{}, nil
	}
	var config PluginTrustConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, fmt.Errorf("parse plugin trust levels: %w", err)
	}
	for id, level := range config {
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("plugin trust levels contains an empty plugin id")
		}
		switch level {
		case TrustOffline, TrustTrusted, TrustIsolated:
		default:
			return nil, fmt.Errorf("plugin %q has unsupported trust level %q", id, level)
		}
	}
	return config, nil
}

// LoadPluginTrustConfig reads an optional deployment fallback. Invalid input
// fails closed by returning an empty config.
func LoadPluginTrustConfig() PluginTrustConfig {
	config, err := ParsePluginTrustConfig(os.Getenv("WEKNORA_PLUGIN_TRUST_LEVELS"))
	if err != nil {
		return PluginTrustConfig{}
	}
	return config
}

func (c PluginTrustConfig) Clone() PluginTrustConfig {
	clone := make(PluginTrustConfig, len(c))
	for id, level := range c {
		clone[id] = level
	}
	return clone
}

func (c PluginTrustConfig) Level(pluginID string) PluginTrustLevel {
	if c == nil {
		return TrustOffline
	}
	if level := c[pluginID]; level != "" {
		return level
	}
	return TrustOffline
}

// ConfigureManagerTrust atomically installs persisted deployment policy.
func ConfigureManagerTrust(manager *Manager, raw string) error {
	config, err := ParsePluginTrustConfig(raw)
	if err != nil {
		return err
	}
	manager.SetPluginTrustConfig(config)
	return nil
}

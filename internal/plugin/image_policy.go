package plugin

import (
	"encoding/json"
	"fmt"
	"os"
)

// ImagePolicy controls which images the runtime-agent is allowed to launch.
// Production (default) requires an exact digest allowlist so a tag cannot drift
// and an arbitrary image cannot be pulled; the manifest's docker:// entrypoint
// is a plugin request, never an authorization. Development mode
// (WEKNORA_PLUGIN_IMAGE_POLICY=development) relaxes to any image for local work.
type ImagePolicy struct {
	development bool
	allowlist   map[string]string
}

// NewImagePolicyFromEnv builds the policy from environment configuration.
// WEKNORA_PLUGIN_IMAGE_ALLOWLIST is a JSON object of plugin_id -> image@digest.
func NewImagePolicyFromEnv() (*ImagePolicy, error) {
	if os.Getenv("WEKNORA_PLUGIN_IMAGE_POLICY") == "development" {
		return &ImagePolicy{development: true}, nil
	}
	raw := os.Getenv("WEKNORA_PLUGIN_IMAGE_ALLOWLIST")
	if raw == "" {
		return nil, fmt.Errorf("image allowlist is required: set WEKNORA_PLUGIN_IMAGE_ALLOWLIST (JSON map plugin_id -> image@sha256:...) or WEKNORA_PLUGIN_IMAGE_POLICY=development")
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("parse WEKNORA_PLUGIN_IMAGE_ALLOWLIST: %w", err)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("image allowlist is empty")
	}
	return &ImagePolicy{allowlist: m}, nil
}

// Check verifies that the requested image is authorized for the plugin.
func (p *ImagePolicy) Check(pluginID, image string) error {
	if p == nil || p.development {
		return nil
	}
	want, ok := p.allowlist[pluginID]
	if !ok {
		return fmt.Errorf("plugin %q is not in the image allowlist", pluginID)
	}
	if image != want {
		return fmt.Errorf("plugin %q image %q does not match the pinned image", pluginID, image)
	}
	return nil
}

// Package sandbox: what a named backend config must carry on its own.
//
// A named config is self-contained. Every value the provider client cannot
// synthesize has to be present in the workspace config itself, so this file
// names those values once and both save and resolve paths check against it.
//
// Two classes are deliberately NOT required:
//
//   - Credentials a self-hosted deployment does not use. The common single-node
//     Cube setup runs unauthenticated, so CubeAPIKey stays optional.
//   - Values the SDK resolves by itself. go-e2b defaults both the API base URL
//     and the sandbox domain when they are left empty, so demanding them would
//     force operators to spell out constants they cannot verify.
//
// Everything else is required precisely because its absence fails late and
// obscurely: a missing Cube proxy URL or sandbox domain still creates a sandbox
// and only breaks when envd traffic is routed, which reads as a provider outage
// rather than a typo in the form.
package sandbox

import (
	"errors"
	"fmt"
	"strings"
)

// ErrSandboxConfigIncomplete marks a named config that cannot build a working
// client because required fields are empty. Callers map it onto 400.
var ErrSandboxConfigIncomplete = errors.New("sandbox: config is missing required fields")

// MissingRequiredFields lists the fields cfg fails to supply for its own
// provider, named after the JSON keys the API and the settings form use so the
// message can be surfaced without translation. Local and disabled hold no
// backend-specific values; Docker must explicitly name its image.
func MissingRequiredFields(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	var missing []string
	require := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, field)
		}
	}
	switch cfg.Type {
	case SandboxTypeCube:
		require("api_url", cfg.CubeAPIURL)
		require("proxy_url", cfg.CubeProxyURL)
		require("sandbox_domain", cfg.CubeSandboxDomain)
		require("template_id", cfg.CubeTemplate)
	case SandboxTypeE2B:
		require("api_key", cfg.E2BAPIKey)
		require("template_id", cfg.E2BTemplate)
	case SandboxTypeDocker:
		require("image", cfg.DockerImage)
	}
	return missing
}

// RequireCompleteConfig is the error form of MissingRequiredFields, shared by
// the save path and the resolve path so both reject the same configs with the
// same wording.
func RequireCompleteConfig(cfg *Config) error {
	missing := MissingRequiredFields(cfg)
	if len(missing) == 0 {
		return nil
	}
	provider := ""
	if cfg != nil {
		provider = string(cfg.Type)
	}
	return fmt.Errorf(
		"%w: %s backend requires %s",
		ErrSandboxConfigIncomplete, provider, strings.Join(missing, ", "),
	)
}

package sandbox

// Runtime tuning has safe built-in defaults. Unlike endpoints, credentials,
// and templates, these values do not identify an external backend.
func applyCubeRuntimeDefaults(cfg *Config) {
	if cfg == nil {
		return
	}
	if cfg.CubeSandboxTTL <= 0 {
		cfg.CubeSandboxTTL = DefaultCubeSandboxTTL
	}
	if cfg.CubeHTTPTimeout <= 0 {
		cfg.CubeHTTPTimeout = DefaultCubeHTTPTimeout
	}
}

func applyDockerRuntimeDefaults(cfg *Config) {
	if cfg == nil {
		return
	}
	// The image is deliberately not defaulted here: it is this backend's
	// template, and a config that fails to name one must be reported as
	// incomplete rather than silently pointed at whatever image the release
	// happens to ship.
	if cfg.DockerHost == "" {
		cfg.DockerHost = DetectLocalDockerHost()
	}
	if cfg.DockerCPULimit <= 0 {
		cfg.DockerCPULimit = DefaultDockerCPULimit
	}
	if cfg.DockerMemoryBytes <= 0 {
		cfg.DockerMemoryBytes = DefaultDockerMemoryLimit
	}
	if cfg.DockerPidsLimit <= 0 {
		cfg.DockerPidsLimit = DefaultDockerPidsLimit
	}
	if cfg.DockerIdleTTL <= 0 {
		cfg.DockerIdleTTL = DefaultDockerIdleTTL
	}
	if cfg.DockerHTTPTimeout <= 0 {
		cfg.DockerHTTPTimeout = DefaultDockerHTTPTimeout
	}
}

func applyE2BRuntimeDefaults(cfg *Config) {
	if cfg == nil {
		return
	}
	if cfg.E2BSandboxTTL <= 0 {
		cfg.E2BSandboxTTL = DefaultE2BSandboxTTL
	}
	if cfg.E2BHTTPTimeout <= 0 {
		cfg.E2BHTTPTimeout = DefaultE2BHTTPTimeout
	}
}

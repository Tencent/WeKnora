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

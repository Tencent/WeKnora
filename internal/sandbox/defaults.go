// Package sandbox: Cube configuration defaults.
//
// applyCubeDefaults fills in Cube-related Config fields that other code (the
// Cube adapter and the session-bound manager) rely on being non-zero. It is
// kept in its own file so future providers can introduce their own
// applyE2BDefaults helper without touching the shared session-bound manager.
package sandbox

// applyCubeDefaults mutates cfg in-place so downstream code can rely on the
// Cube-specific fields being non-zero. Safe to call multiple times.
func applyCubeDefaults(cfg *Config) {
	if cfg == nil {
		return
	}
	if cfg.CubeAPIURL == "" {
		cfg.CubeAPIURL = DefaultCubeAPIURL
	}
	if cfg.CubeProxyURL == "" {
		cfg.CubeProxyURL = DefaultCubeProxyURL
	}
	if cfg.CubeSandboxDomain == "" {
		cfg.CubeSandboxDomain = DefaultCubeSandboxDomain
	}
	if cfg.CubeTemplate == "" {
		cfg.CubeTemplate = DefaultCubeTemplate
	}
	if cfg.CubeSandboxTTL <= 0 {
		cfg.CubeSandboxTTL = DefaultCubeSandboxTTL
	}
	if cfg.CubeHTTPTimeout <= 0 {
		cfg.CubeHTTPTimeout = DefaultCubeHTTPTimeout
	}
}

// applyE2BDefaults mutates cfg in-place so downstream code can rely on the
// E2B-specific timeout fields being non-zero.
//
// Unlike Cube, there is no repository-wide default E2B template ID — an
// empty E2BTemplate causes SessionBoundManager construction to fail so
// operators get a clear misconfiguration message instead of a silent
// fallback.
func applyE2BDefaults(cfg *Config) {
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

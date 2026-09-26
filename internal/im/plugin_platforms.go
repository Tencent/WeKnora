package im

import "sync/atomic"

// PluginPlatforms supplies the IM platforms plugins contribute. Their IDs
// are qualified contribution IDs ("acme.zulip/zulip"), so they never clash
// with the builtin platforms.
type PluginPlatforms interface {
	// Platforms lists the loaded plugin platforms.
	Platforms() []PlatformInfo
	// Platform returns one, with the factory for its adapters.
	Platform(id string) (PlatformInfo, AdapterFactory, bool)
}

var pluginPlatforms atomic.Pointer[PluginPlatforms]

// SetPluginPlatforms installs the source of plugin IM platforms.
func SetPluginPlatforms(p PluginPlatforms) { pluginPlatforms.Store(&p) }

func pluginPlatform(id string) (PlatformInfo, AdapterFactory, bool) {
	if p := pluginPlatforms.Load(); p != nil {
		return (*p).Platform(id)
	}
	return PlatformInfo{}, nil, false
}

// IsPluginPlatform reports whether a platform ID is a loaded plugin's.
func IsPluginPlatform(id string) bool {
	_, _, ok := pluginPlatform(id)
	return ok
}

func listPluginPlatforms() []PlatformInfo {
	if p := pluginPlatforms.Load(); p != nil {
		return (*p).Platforms()
	}
	return nil
}

// factoryFor is the adapter factory of a platform, builtin or plugin.
// Callers hold s.mu.
func (s *Service) factoryFor(platform string) (AdapterFactory, bool) {
	if f, ok := s.adapterFactories[platform]; ok {
		return f, true
	}
	_, f, ok := pluginPlatform(platform)
	return f, ok
}

package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/datasource"
	infraWebSearch "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/logger"
)

// rescanMu serializes external plugin discovery/loading so a manual rescan
// cannot race the startup load or a concurrent rescan. It is a blocking lock:
// a second scan simply queues behind the first and then sees every plugin as
// already loaded, reporting them as skipped (idempotent).
var rescanMu sync.Mutex

// LoadExternal discovers packages from independent plugin roots, creates a
// runtime from the manifest entrypoint, registers it in the lifecycle manager,
// and starts it. The datasource path also registers an instance-aware factory
// in the existing connector registry.
func LoadExternal(ctx context.Context, roots []string, manager *Manager, registry *datasource.ConnectorRegistry) error {
	return LoadExternalWithRegistries(ctx, roots, manager, registry, nil, nil)
}

// LoadExternalWithRegistries is the full v1 loader. Datasource and parser
// packages can be loaded with the legacy LoadExternal helper; a host that also
// exposes external web-search providers supplies the web-search registry.
//
// Extension-specific registration is delegated to an ExtensionAdapterRegistry,
// so adding a new extension type requires registering a new adapter rather than
// adding a switch case here.
func LoadExternalWithRegistries(ctx context.Context, roots []string, manager *Manager, registry *datasource.ConnectorRegistry, searchRegistry *infraWebSearch.Registry, retrieverRegistry *RetrieverProviderRegistry) error {
	rescanMu.Lock()
	defer rescanMu.Unlock()
	packages, err := DiscoverPackages(roots)
	if err != nil {
		return err
	}
	adapters := NewExtensionAdapterRegistry(registry, searchRegistry, retrieverRegistry)
	// 插件级隔离：单个插件加载失败只记录并跳过，成功插件保留运行，不再整体回滚。
	// 一个损坏的外部插件不应拖垮宿主启动（与热重扫 Rescan 的隔离语义一致）。
	var failed []string
	for _, pkg := range packages {
		if _, err := loadOnePackage(ctx, pkg, manager, adapters); err != nil {
			logger.Errorf(ctx, "load external plugin %q failed: %v", pkg.Manifest.ID, err)
			failed = append(failed, pkg.Manifest.ID)
			continue
		}
	}
	if len(failed) > 0 {
		logger.Warnf(ctx, "isolated %d failed external plugin(s): %v (host continues startup)", len(failed), failed)
	}
	return nil
}

// loadedAdapter records a completed load for rollback on partial failure.
type loadedAdapter struct {
	id      string
	adapter ExtensionAdapter
	handle  adapterHandle
}

// loadOnePackage builds a runtime for one discovered package, registers it via
// its extension adapter, and starts it. On failure it rolls back the plugin's
// own partial state so a retry starts from a clean slate.
func loadOnePackage(ctx context.Context, pkg Package, manager *Manager, adapters *ExtensionAdapterRegistry) (loadedAdapter, error) {
	manifest := pkg.Manifest
	if manifest.Entrypoint == "" {
		return loadedAdapter{}, fmt.Errorf("plugin %q has no entrypoint", manifest.ID)
	}
	plan, err := ResolveExecutionPlan(manifest, manager.PluginTrustLevel(manifest.ID))
	if err != nil {
		// Keep a failed placeholder so the plugin card stays visible in the
		// management UI and the trust level can be corrected; rescan retries
		// failed placeholders once the configuration is fixed.
		manager.RegisterFailed(manifest, err.Error())
		return loadedAdapter{}, err
	}
	// Apply deployment policy to the runtime copy only.
	runtimeManifest := manifest
	runtimeManifest.Permissions.Network = plan.Network
	runtimeManifest.Permissions.AllowedDestinations = append([]string(nil), plan.Allowlist...)
	var runtime Runtime
	if plan.Isolation == IsolationOCI {
		runtime = newOCIRuntime(runtimeManifest, strings.TrimPrefix(manifest.Entrypoint, "docker://"))
	} else {
		entrypoint := manifest.Entrypoint
		if !filepath.IsAbs(entrypoint) {
			entrypoint = filepath.Join(pkg.Root, entrypoint)
		}
		runtime = NewProcessRuntime(runtimeManifest, entrypoint)
	}
	adapter, ok := adapters.Get(manifest.ExtensionType)
	if !ok {
		return loadedAdapter{}, fmt.Errorf("plugin %q uses extension_type %q, but no adapter is registered for it", manifest.ID, manifest.ExtensionType)
	}
	handle, err := adapter.Register(manager, manifest, runtime)
	if err != nil {
		rollbackLoad(ctx, manager, adapter, handle, manifest)
		return loadedAdapter{}, err
	}
	if err := manager.Start(ctx, manifest.ID); err != nil {
		// Keep the manager entry — manager.Start already recorded StateFailed on
		// it — so the failed plugin stays visible in the plugin management UI and
		// can be retried after a trust change or a rescan. Only detach it from
		// the business registry so a dead plugin is not selectable downstream.
		adapter.Unregister(handle)
		return loadedAdapter{}, err
	}
	return loadedAdapter{id: manifest.ID, adapter: adapter, handle: handle}, nil
}

// rollbackLoad unwinds a partially loaded plugin. adapter.Unregister is
// idempotent; manager.Unregister only runs while the entry still exists because
// its Stop step errors on a missing id.
func rollbackLoad(ctx context.Context, manager *Manager, adapter ExtensionAdapter, handle adapterHandle, manifest Manifest) {
	if adapter != nil {
		adapter.Unregister(handle)
	}
	if _, ok := manager.Get(manifest.ID); ok {
		_ = manager.Unregister(ctx, manifest.ID)
	}
}

func LoadExternalFromEnv(ctx context.Context, manager *Manager, registry *datasource.ConnectorRegistry) error {
	return LoadExternalFromEnvWithRegistries(ctx, manager, registry, nil, nil)
}

// pluginDirEnvVars maps each extension type to the environment variable that
// names the directory holding plugins of that type. Directories are organized
// by extension type so a host can point each entry point at its own plugin
// directory; every directory is still scanned and loaded at startup.
var pluginDirEnvVars = []struct {
	extensionType string
	envVar        string
}{
	{ExtensionDataSource, "WEKNORA_PLUGIN_DIR_DATASOURCE"},
	{ExtensionParser, "WEKNORA_PLUGIN_DIR_PARSER"},
	{ExtensionSearch, "WEKNORA_PLUGIN_DIR_SEARCH"},
	{ExtensionModel, "WEKNORA_PLUGIN_DIR_MODEL"},
	{ExtensionRetriever, "WEKNORA_PLUGIN_DIR_RETRIEVER"},
}

// pluginRootsFromEnv collects the configured plugin directories from the
// WEKNORA_PLUGIN_DIR_* environment variables.
func pluginRootsFromEnv() []string {
	var roots []string
	for _, entry := range pluginDirEnvVars {
		value := strings.TrimSpace(os.Getenv(entry.envVar))
		if value == "" {
			continue
		}
		for _, dir := range strings.FieldsFunc(value, func(r rune) bool {
			return r == os.PathListSeparator || r == ','
		}) {
			if dir = strings.TrimSpace(dir); dir != "" {
				roots = append(roots, dir)
			}
		}
	}
	return roots
}

func LoadExternalFromEnvWithRegistries(ctx context.Context, manager *Manager, registry *datasource.ConnectorRegistry, searchRegistry *infraWebSearch.Registry, retrieverRegistry *RetrieverProviderRegistry) error {
	roots := pluginRootsFromEnv()
	if len(roots) == 0 {
		return nil
	}
	return LoadExternalWithRegistries(ctx, roots, manager, registry, searchRegistry, retrieverRegistry)
}

// RescanReport summarizes one incremental plugin rescan pass.
type RescanReport struct {
	Added   []string `json:"added"`
	Skipped []string `json:"skipped"`
	Changed []string `json:"changed"`
	Errors  []string `json:"errors,omitempty"`
}

// RescanExternalFromEnvWithRegistries re-runs discovery over the configured
// plugin directories and incrementally loads plugins that appeared since the
// last load. Already-loaded plugins with an unchanged manifest are skipped; a
// changed manifest is reported in Changed (applying it still requires a
// restart). A single plugin's failure is isolated and reported without
// affecting the others.
func RescanExternalFromEnvWithRegistries(ctx context.Context, manager *Manager, registry *datasource.ConnectorRegistry, searchRegistry *infraWebSearch.Registry, retrieverRegistry *RetrieverProviderRegistry) RescanReport {
	roots := pluginRootsFromEnv()
	if len(roots) == 0 {
		return RescanReport{}
	}
	return RescanExternalWithRegistries(ctx, roots, manager, registry, searchRegistry, retrieverRegistry)
}

// RescanExternalWithRegistries is RescanExternalFromEnvWithRegistries over an
// explicit set of roots.
func RescanExternalWithRegistries(ctx context.Context, roots []string, manager *Manager, registry *datasource.ConnectorRegistry, searchRegistry *infraWebSearch.Registry, retrieverRegistry *RetrieverProviderRegistry) RescanReport {
	rescanMu.Lock()
	defer rescanMu.Unlock()
	report := RescanReport{}
	packages, err := DiscoverPackages(roots)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	existing := make(map[string]Manifest, len(packages))
	for _, info := range manager.List() {
		if info.Manifest.Entrypoint == "" {
			continue // built-in plugins are not re-discovered
		}
		existing[info.Manifest.ID] = info.Manifest
	}
	adapters := NewExtensionAdapterRegistry(registry, searchRegistry, retrieverRegistry)
	for _, pkg := range packages {
		manifest := pkg.Manifest
		if prev, ok := existing[manifest.ID]; ok {
			trustUnchanged := manager.PluginTrustLevel(manifest.ID) == manager.LoadedTrustLevel(manifest.ID)
			snap, _ := manager.HealthSnapshot(manifest.ID)
			// Retry plugins that failed to start OR turned unhealthy on a
			// previous pass (e.g. their container was removed externally), so a
			// rescan is also a healing pass — not only a manifest diff.
			previouslyBroken := snap.State == StateFailed || snap.State == StateUnhealthy
			if manifestsEqual(prev, manifest) && trustUnchanged && !previouslyBroken {
				report.Skipped = append(report.Skipped, manifest.ID)
			} else if err := reloadPackage(ctx, prev, pkg, manager, adapters); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", manifest.ID, err))
			} else {
				report.Changed = append(report.Changed, manifest.ID)
			}
			continue
		}
		if _, err := loadOnePackage(ctx, pkg, manager, adapters); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", manifest.ID, err))
			continue
		}
		report.Added = append(report.Added, manifest.ID)
	}
	return report
}

// manifestsEqual compares two manifests ignoring the discovery-populated
// SourceDir, which is a filesystem detail rather than a semantic field.
func manifestsEqual(a, b Manifest) bool {
	a.SourceDir, b.SourceDir = "", ""
	return reflect.DeepEqual(a, b)
}

// reloadPackage unloads a previously-loaded plugin whose manifest changed and
// reloads it from the newly discovered package. The unload recomputes the
// adapter handle from the PREVIOUS manifest — handles are deterministic, but a
// registration name (e.g. metadata.provider) may itself change, so the old
// handle must be derived from the old manifest to unregister the old entry. No
// cross-call state needs to be kept. If the new manifest is invalid the reload
// fails and the plugin is left unloaded — reported via Errors — which matches
// what a cold restart would do.
func reloadPackage(ctx context.Context, prev Manifest, pkg Package, manager *Manager, adapters *ExtensionAdapterRegistry) error {
	manifest := pkg.Manifest
	adapter, ok := adapters.Get(manifest.ExtensionType)
	if !ok {
		return fmt.Errorf("plugin %q uses extension_type %q, but no adapter is registered for it", manifest.ID, manifest.ExtensionType)
	}
	// 卸载旧插件：用旧 manifest 推导 handle 清理旧业务注册表，manager 按 ID 停掉旧 runtime。
	adapter.Unregister(adapter.HandleFromManifest(prev))
	if _, ok := manager.Get(manifest.ID); ok {
		_ = manager.Unregister(ctx, manifest.ID)
	}
	// 重新加载新 manifest。
	if _, err := loadOnePackage(ctx, pkg, manager, adapters); err != nil {
		return err
	}
	return nil
}

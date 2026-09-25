package docparser

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// EngineRegistration is what every locally registered parser engine provides:
// the metadata the engine list shows, and the reader that does the parsing.
// Remote-only engines (e.g. markitdown) live in the Python docreader, are
// discovered through its ListEngines RPC, and never register here — the
// registry routes them to the docreader client by default.
type EngineRegistration interface {
	Name() string
	Description() string
	FileTypes(docreaderConnected bool) []string
	CheckAvailable(docreaderConnected bool, overrides map[string]string) (available bool, reason string)
	// NewReader builds the reader for one parse request. Returning an error
	// means this engine cannot serve the request (missing credentials,
	// unreachable service); the caller reports it rather than silently
	// parsing with something else.
	NewReader(ctx context.Context, deps ReaderDeps) (interfaces.DocReader, error)
}

// ReaderDeps carries everything an engine may need to build its reader but
// cannot construct itself: tenant configuration, tenant credentials, and the
// shared docreader connection.
type ReaderDeps struct {
	// Overrides holds tenant-level engine configuration (service endpoints,
	// API keys), as produced by ParserEngineConfig.ToOverridesMap.
	Overrides map[string]string
	// Remote is the docreader client. Nil when the service is not connected.
	Remote interfaces.DocReader
	// WeKnoraCloudCredentials resolves the tenant's WeKnora Cloud
	// credentials. It is a function rather than a value because resolving
	// them can hit the database, which most engines never need. Nil, or a
	// nil return, means the tenant has not configured them.
	WeKnoraCloudCredentials func(ctx context.Context) *types.WeKnoraCloudCredentials
}

// enginesMu guards the registries: plugin engines come and go while the
// server runs.
var enginesMu sync.RWMutex

// localEngines holds all locally registered parser engines, in registration
// order — which is also the order the engine list is shown in.
var localEngines []EngineRegistration

// pluginEngines holds engines installed plugins provide, by qualified name
// ("acme.ocr/ocr"). They are kept apart from localEngines so the builtin
// plugin catalog never describes them as builtins.
var pluginEngines = map[string]EngineRegistration{}

// PluginEngineInfo is what a plugin engine adds to the engine list.
type PluginEngineInfo interface {
	PluginID() string
	// DisplayNames localizes the engine's name, keyed by locale.
	DisplayNames() map[string]string
}

// RegisterEngine adds an engine to the local registry. Called from init().
func RegisterEngine(e EngineRegistration) {
	enginesMu.Lock()
	defer enginesMu.Unlock()
	localEngines = append(localEngines, e)
}

// RegisterPluginEngine adds or replaces a plugin's engine. It refuses a name
// a builtin engine uses.
func RegisterPluginEngine(e EngineRegistration) error {
	enginesMu.Lock()
	defer enginesMu.Unlock()
	for _, builtin := range localEngines {
		if builtin.Name() == e.Name() {
			return fmt.Errorf("parser engine %s already exists", e.Name())
		}
	}
	pluginEngines[e.Name()] = e
	return nil
}

// UnregisterPluginEngine removes a plugin's engine.
func UnregisterPluginEngine(name string) {
	enginesMu.Lock()
	defer enginesMu.Unlock()
	delete(pluginEngines, name)
}

// Engines returns the builtin engines in registration order.
func Engines() []EngineRegistration {
	enginesMu.RLock()
	defer enginesMu.RUnlock()
	out := make([]EngineRegistration, len(localEngines))
	copy(out, localEngines)
	return out
}

// pluginEngineList returns the plugin engines sorted by name.
func pluginEngineList() []EngineRegistration {
	enginesMu.RLock()
	defer enginesMu.RUnlock()
	out := make([]EngineRegistration, 0, len(pluginEngines))
	for _, e := range pluginEngines {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// PluginFileTypes returns the file types plugin engines parse, so uploads
// of those types are accepted.
func PluginFileTypes() []string {
	var out []string
	for _, e := range pluginEngineList() {
		out = append(out, e.FileTypes(true)...)
	}
	return out
}

// lookupEngine returns the registered engine with this name.
func lookupEngine(name string) (EngineRegistration, bool) {
	enginesMu.RLock()
	defer enginesMu.RUnlock()
	for _, engine := range localEngines {
		if engine.Name() == name {
			return engine, true
		}
	}
	e, ok := pluginEngines[name]
	return e, ok
}

// isPluginEngineName reports whether a name is a qualified plugin
// contribution ID, which no builtin or docreader engine uses.
func isPluginEngineName(name string) bool { return strings.Contains(name, "/") }

// NewReader builds the reader for an engine.
//
// An empty engine name means "no explicit choice": simple formats are handled
// in Go and everything else goes to the docreader service. An unknown name is
// routed to the docreader too, so engines that only exist in the Python
// service keep working without a Go-side registration.
func NewReader(
	ctx context.Context, engine, fileType string, isURL bool, deps ReaderDeps,
) (interfaces.DocReader, error) {
	if registration, ok := lookupEngine(engine); ok {
		return registration.NewReader(ctx, deps)
	}
	if isPluginEngineName(engine) {
		// Its plugin was uninstalled or disabled: the Python docreader does
		// not know it either, so say so instead of sending it there.
		return nil, errEngineUnavailable(engine, "its plugin is not installed or not running")
	}
	if engine == "" && !isURL && IsSimpleFormat(fileType) {
		return &SimpleFormatReader{}, nil
	}
	return remoteReader(deps)
}

// remoteReader returns the docreader client, or an error when the service is
// not connected — a nil interface value here would panic at the call site.
func remoteReader(deps ReaderDeps) (interfaces.DocReader, error) {
	if deps.Remote == nil {
		return nil, errNotConnected
	}
	return deps.Remote, nil
}

// ListAllEngines returns the merged engine list: locally registered engines
// plus engines discovered from the remote docreader via ListEngines RPC.
//
// Merge rules:
//   - Local engines are always included, with Go-side availability checks.
//   - For a remote engine whose name matches a local one, the remote's
//     file_types and description take precedence (the remote service is
//     authoritative for its own capabilities).
//   - Remote engines not present locally are appended as-is, enabling
//     auto-discovery of newly added docreader engines without Go changes.
func ListAllEngines(
	docreaderConnected bool, overrides map[string]string, remoteEngines []types.ParserEngineInfo,
) []types.ParserEngineInfo {
	remoteMap := make(map[string]types.ParserEngineInfo, len(remoteEngines))
	for _, re := range remoteEngines {
		remoteMap[re.Name] = re
	}

	locals := Engines()
	seen := make(map[string]bool, len(locals))
	result := make([]types.ParserEngineInfo, 0, len(locals)+len(remoteEngines))

	for _, e := range locals {
		name := e.Name()
		seen[name] = true

		fileTypes := e.FileTypes(docreaderConnected)
		description := e.Description()

		if re, ok := remoteMap[name]; ok {
			if len(re.FileTypes) > 0 {
				fileTypes = re.FileTypes
			}
			if re.Description != "" {
				description = re.Description
			}
		}

		available, reason := e.CheckAvailable(docreaderConnected, overrides)
		result = append(result, types.ParserEngineInfo{
			Name:              name,
			Description:       description,
			FileTypes:         fileTypes,
			Available:         available,
			UnavailableReason: reason,
		})
	}

	for _, re := range remoteEngines {
		if seen[re.Name] {
			continue
		}
		result = append(result, re)
	}

	for _, e := range pluginEngineList() {
		available, reason := e.CheckAvailable(docreaderConnected, overrides)
		info := types.ParserEngineInfo{
			Name: e.Name(), Description: e.Description(), FileTypes: e.FileTypes(docreaderConnected),
			Available: available, UnavailableReason: reason,
		}
		if meta, ok := e.(PluginEngineInfo); ok {
			info.PluginID, info.DisplayNames = meta.PluginID(), meta.DisplayNames()
		}
		result = append(result, info)
	}

	return result
}

// errEngineUnavailable reports an engine that is registered but cannot run for
// this tenant or this build.
func errEngineUnavailable(engine, reason string) error {
	return fmt.Errorf("parser engine %q is unavailable: %s", engine, reason)
}

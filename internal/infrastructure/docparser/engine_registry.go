package docparser

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// EngineRegistration is the interface every locally registered parser engine
// must implement. Remote-only engines (e.g. markitdown) are discovered via
// the docreader ListEngines RPC and do not need a local registration.
type EngineRegistration interface {
	Name() string
	Description() string
	FileTypes(docreaderConnected bool) []string
	CheckAvailable(docreaderConnected bool, overrides map[string]string) (available bool, reason string)
}

// localEngines holds all locally registered parser engines.
var localEngines []EngineRegistration

// RegisterEngine adds an engine to the local registry. Called in init().
func RegisterEngine(e EngineRegistration) {
	localEngines = append(localEngines, e)
}

func init() {
	RegisterEngine(&builtinEngine{})
	RegisterEngine(&simpleEngine{})
	RegisterEngine(&mineruEngine{})
	RegisterEngine(&mineruCloudEngine{})
}

// ---------------------------------------------------------------------------
// builtin — DocReader-backed parser for complex document formats.
// ---------------------------------------------------------------------------

type builtinEngine struct{}

func (e *builtinEngine) Name() string { return "builtin" }
func (e *builtinEngine) Description() string {
	return "DocReader built-in parser engine"
}
func (e *builtinEngine) FileTypes(_ bool) []string {
	return []string{"docx", "doc", "pdf", "md", "markdown", "xlsx", "xls", "jpg", "jpeg", "png", "gif", "bmp", "tiff", "webp"}
}
func (e *builtinEngine) CheckAvailable(docreaderConnected bool, _ map[string]string) (bool, string) {
	if docreaderConnected {
		return true, ""
	}
	return false, "DocReader service not connected"
}

// SimpleEngineName is the engine name for Go-native simple format handling.
const SimpleEngineName = "simple"

// ---------------------------------------------------------------------------
// simple — Go handles md/txt/csv natively, no external service needed.
// Distinct from docreader's "builtin" which uses Python libraries for
// complex formats (docx, pdf, etc.).
// ---------------------------------------------------------------------------

type simpleEngine struct{}

func (e *simpleEngine) Name() string { return SimpleEngineName }
func (e *simpleEngine) Description() string {
	return "Simple format & image parsing (no external service required)"
}
func (e *simpleEngine) FileTypes(_ bool) []string {
	return []string{"md", "markdown", "txt", "csv", "json", "jpg", "jpeg", "png", "gif", "bmp", "tiff", "webp", "mp3", "wav", "m4a", "flac", "ogg"}
}
func (e *simpleEngine) CheckAvailable(_ bool, _ map[string]string) (bool, string) {
	return true, ""
}

// ---------------------------------------------------------------------------
// mineru — Go-native, calls self-hosted MinerU API directly
// ---------------------------------------------------------------------------

type mineruEngine struct{}

func (e *mineruEngine) Name() string        { return "mineru" }
func (e *mineruEngine) Description() string { return "MinerU self-hosted service" }
func (e *mineruEngine) FileTypes(_ bool) []string {
	return []string{"pdf", "jpg", "jpeg", "png", "bmp", "tiff", "doc", "docx", "ppt", "pptx"}
}
func (e *mineruEngine) CheckAvailable(_ bool, overrides map[string]string) (bool, string) {
	endpoint := strings.TrimSpace(overrides["mineru_endpoint"])
	if endpoint == "" {
		return false, "MinerU service not configured"
	}
	return PingMinerU(endpoint)
}

// ---------------------------------------------------------------------------
// mineru_cloud — Go-native, calls MinerU Cloud API directly
// ---------------------------------------------------------------------------

type mineruCloudEngine struct{}

func (e *mineruCloudEngine) Name() string        { return "mineru_cloud" }
func (e *mineruCloudEngine) Description() string { return "MinerU Cloud API" }
func (e *mineruCloudEngine) FileTypes(_ bool) []string {
	return []string{"pdf", "jpg", "jpeg", "png", "bmp", "tiff", "doc", "docx", "ppt", "pptx"}
}
func (e *mineruCloudEngine) CheckAvailable(_ bool, overrides map[string]string) (bool, string) {
	apiKey := strings.TrimSpace(overrides["mineru_api_key"])
	if apiKey == "" {
		return false, "MinerU API Key not configured"
	}
	return PingMinerUCloud(apiKey)
}

// ---------------------------------------------------------------------------
// ListAllEngines — merge local + remote
// ---------------------------------------------------------------------------

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
func ListAllEngines(docreaderConnected bool, overrides map[string]string, remoteEngines []types.ParserEngineInfo) []types.ParserEngineInfo {
	remoteMap := make(map[string]types.ParserEngineInfo, len(remoteEngines))
	for _, re := range remoteEngines {
		remoteMap[re.Name] = re
	}

	seen := make(map[string]bool, len(localEngines))
	result := make([]types.ParserEngineInfo, 0, len(localEngines)+len(remoteEngines))

	for _, e := range localEngines {
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

	return result
}

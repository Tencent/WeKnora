package docparser

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Engine names, as stored in knowledge base parser rules and shown in the UI.
const (
	// BuiltinEngineName is the DocReader (Python) parser suite.
	BuiltinEngineName = "builtin"
	// SimpleEngineName is Go-native handling of text formats and images.
	SimpleEngineName = "simple"
	// AnydocEngineName is the in-process anydoc office-document converter.
	AnydocEngineName = "anydoc"
	// WeKnoraCloudEngineName is the hosted WeKnora Cloud document reader.
	WeKnoraCloudEngineName = "weknoracloud"
	// MinerUEngineName is a self-hosted MinerU service.
	MinerUEngineName = "mineru"
	// MinerUCloudEngineName is the MinerU Cloud API.
	MinerUCloudEngineName = "mineru_cloud"
	// PaddleOCRVLEngineName is a self-hosted PaddleOCR-VL pipeline.
	PaddleOCRVLEngineName = "paddleocr_vl"
	// PaddleOCRVLCloudEngineName is the PaddleOCR-VL AI Studio cloud API.
	PaddleOCRVLCloudEngineName = "paddleocr_vl_cloud"
)

func init() {
	RegisterEngine(&builtinEngine{})
	RegisterEngine(&simpleEngine{})
	RegisterEngine(&anydocEngine{})
	RegisterEngine(&weKnoraCloudEngine{})
	RegisterEngine(&mineruEngine{})
	RegisterEngine(&mineruCloudEngine{})
	RegisterEngine(&paddleOCRVLEngine{})
	RegisterEngine(&paddleOCRVLCloudEngine{})
}

// ---------------------------------------------------------------------------
// builtin — DocReader-backed parser for complex document formats.
// ---------------------------------------------------------------------------

type builtinEngine struct{}

func (e *builtinEngine) Name() string { return BuiltinEngineName }

func (e *builtinEngine) Description() string { return "DocReader built-in parser engine" }

func (e *builtinEngine) FileTypes(_ bool) []string {
	return []string{
		"docx", "doc", "pdf", "md", "markdown", "xlsx", "xls", "epub",
		"html", "htm", "mhtml",
		"jpg", "jpeg", "png", "gif", "bmp", "tiff", "webp",
		"mp3", "wav", "m4a", "flac", "ogg",
	}
}

func (e *builtinEngine) CheckAvailable(docreaderConnected bool, _ map[string]string) (bool, string) {
	if docreaderConnected {
		return true, ""
	}
	return false, "DocReader service not connected"
}

// NewReader returns the docreader client. Selecting "builtin" explicitly means
// the docreader is wanted even for formats the simple reader could handle.
func (e *builtinEngine) NewReader(_ context.Context, deps ReaderDeps) (interfaces.DocReader, error) {
	return remoteReader(deps)
}

// ---------------------------------------------------------------------------
// simple — Go handles md/txt/csv/json natively, no external service needed.
// Distinct from docreader's "builtin", which uses Python libraries for complex
// formats (docx, pdf, ...).
// ---------------------------------------------------------------------------

type simpleEngine struct{}

func (e *simpleEngine) Name() string { return SimpleEngineName }

func (e *simpleEngine) Description() string {
	return "Simple format & image parsing (no external service required)"
}

func (e *simpleEngine) FileTypes(_ bool) []string {
	return []string{
		"md", "markdown", "txt", "csv", "json",
		"jpg", "jpeg", "png", "gif", "bmp", "tiff", "webp",
		"mp3", "wav", "m4a", "flac", "ogg",
	}
}

func (e *simpleEngine) CheckAvailable(_ bool, _ map[string]string) (bool, string) {
	return true, ""
}

func (e *simpleEngine) NewReader(_ context.Context, _ ReaderDeps) (interfaces.DocReader, error) {
	return &SimpleFormatReader{}, nil
}

// ---------------------------------------------------------------------------
// anydoc — office documents converted in this process, no external service.
// Only present in builds that link the converter (see the anydoc package).
// ---------------------------------------------------------------------------

type anydocEngine struct{}

func (e *anydocEngine) Name() string { return AnydocEngineName }

func (e *anydocEngine) Description() string {
	return "anydoc in-process office document converter (no external service required)"
}

func (e *anydocEngine) FileTypes(_ bool) []string { return anydoc.SupportedFileTypes() }

func (e *anydocEngine) CheckAvailable(_ bool, _ map[string]string) (bool, string) {
	if anydoc.Available() {
		return true, ""
	}
	return false, anydoc.UnavailableReason()
}

func (e *anydocEngine) NewReader(_ context.Context, deps ReaderDeps) (interfaces.DocReader, error) {
	if !anydoc.Available() {
		return nil, errEngineUnavailable(AnydocEngineName, anydoc.UnavailableReason())
	}
	return NewAnydocReader(deps.Overrides, deps.Remote), nil
}

// ---------------------------------------------------------------------------
// weknoracloud — Tenant-scoped WeKnoraCloud docreader with signed requests.
// ---------------------------------------------------------------------------

type weKnoraCloudEngine struct{}

func (e *weKnoraCloudEngine) Name() string { return WeKnoraCloudEngineName }

func (e *weKnoraCloudEngine) Description() string { return "WeKnoraCloud document reader" }

func (e *weKnoraCloudEngine) FileTypes(_ bool) []string {
	return []string{"docx", "doc", "pdf", "md", "markdown", "xlsx", "xls", "pptx", "ppt"}
}

func (e *weKnoraCloudEngine) CheckAvailable(_ bool, overrides map[string]string) (bool, string) {
	if overrides["weknoracloud_app_id"] != "" {
		return true, ""
	}
	return false, "WeKnora Cloud credentials not configured. Go to Settings → WeKnora Cloud to set up."
}

func (e *weKnoraCloudEngine) NewReader(
	ctx context.Context, deps ReaderDeps,
) (interfaces.DocReader, error) {
	if deps.WeKnoraCloudCredentials == nil {
		return nil, errEngineUnavailable(WeKnoraCloudEngineName, "no credential resolver configured")
	}
	creds := deps.WeKnoraCloudCredentials(ctx)
	if creds == nil {
		return nil, errEngineUnavailable(WeKnoraCloudEngineName, "tenant credentials not configured")
	}
	return NewWeKnoraCloudSignedDocumentReader(creds.AppID, creds.AppSecret)
}

// ---------------------------------------------------------------------------
// mineru — Go-native, calls self-hosted MinerU API directly
// ---------------------------------------------------------------------------

type mineruEngine struct{}

func (e *mineruEngine) Name() string { return MinerUEngineName }

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

func (e *mineruEngine) NewReader(_ context.Context, deps ReaderDeps) (interfaces.DocReader, error) {
	return NewMinerUReader(deps.Overrides), nil
}

// ---------------------------------------------------------------------------
// mineru_cloud — Go-native, calls MinerU Cloud API directly
// ---------------------------------------------------------------------------

type mineruCloudEngine struct{}

func (e *mineruCloudEngine) Name() string { return MinerUCloudEngineName }

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

func (e *mineruCloudEngine) NewReader(_ context.Context, deps ReaderDeps) (interfaces.DocReader, error) {
	return NewMinerUCloudReader(deps.Overrides), nil
}

// ---------------------------------------------------------------------------
// paddleocr_vl — Go-native, calls a self-hosted PaddleOCR-VL pipeline service
// ---------------------------------------------------------------------------

type paddleOCRVLEngine struct{}

func (e *paddleOCRVLEngine) Name() string { return PaddleOCRVLEngineName }

func (e *paddleOCRVLEngine) Description() string { return "PaddleOCR-VL self-hosted service" }

func (e *paddleOCRVLEngine) FileTypes(_ bool) []string {
	return []string{"pdf", "jpg", "jpeg", "png", "bmp", "tiff"}
}

func (e *paddleOCRVLEngine) CheckAvailable(_ bool, overrides map[string]string) (bool, string) {
	endpoint := strings.TrimSpace(overrides["paddleocr_vl_endpoint"])
	if endpoint == "" {
		return false, "PaddleOCR-VL service not configured"
	}
	return PingPaddleOCRVL(endpoint)
}

func (e *paddleOCRVLEngine) NewReader(_ context.Context, deps ReaderDeps) (interfaces.DocReader, error) {
	return NewPaddleOCRVLReader(deps.Overrides), nil
}

// ---------------------------------------------------------------------------
// paddleocr_vl_cloud — Go-native, calls the PaddleOCR-VL AI Studio cloud API
// ---------------------------------------------------------------------------

type paddleOCRVLCloudEngine struct{}

func (e *paddleOCRVLCloudEngine) Name() string { return PaddleOCRVLCloudEngineName }

func (e *paddleOCRVLCloudEngine) Description() string { return "PaddleOCR-VL Cloud API" }

func (e *paddleOCRVLCloudEngine) FileTypes(_ bool) []string {
	return []string{"pdf", "jpg", "jpeg", "png", "bmp", "tiff"}
}

func (e *paddleOCRVLCloudEngine) CheckAvailable(_ bool, overrides map[string]string) (bool, string) {
	token := strings.TrimSpace(overrides["paddleocr_vl_cloud_token"])
	if token == "" {
		return false, "PaddleOCR-VL Cloud Token not configured"
	}
	return PingPaddleOCRVLCloud(token)
}

func (e *paddleOCRVLCloudEngine) NewReader(
	_ context.Context, deps ReaderDeps,
) (interfaces.DocReader, error) {
	return NewPaddleOCRVLCloudReader(deps.Overrides), nil
}

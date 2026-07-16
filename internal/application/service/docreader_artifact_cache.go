package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	docReaderArtifactStage           = "docreader.parse"
	docReaderArtifactKeyVersion      = uint16(1)
	docReaderArtifactCodecVersion    = byte(1)
	docReaderArtifactRenderVersionV1 = "1"
)

var (
	errDocReaderArtifactProvider = errors.New("DocReader artifact provider failed")
	errDocReaderArtifactStore    = errors.New("DocReader artifact store failed")
	errDocReaderArtifactCodec    = errors.New("DocReader artifact codec failed")
)

type docReaderArtifactRequest struct {
	tenantID       uint64
	sourcePath     string
	read           func(context.Context, *types.ReadRequest) (*types.ReadResult, error)
	readerIdentity string
	renderVersion  string
	readRequest    *types.ReadRequest
}

type docReaderArtifactOverride struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type docReaderArtifactPayload struct {
	MarkdownContent string                   `json:"markdown_content"`
	ImageRefs       []docReaderArtifactImage `json:"image_refs,omitempty"`
	Metadata        map[string]string        `json:"metadata,omitempty"`
}

type docReaderArtifactImage struct {
	Filename    string `json:"filename,omitempty"`
	OriginalRef string `json:"original_ref,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	ImageData   []byte `json:"image_data,omitempty"`
	IsOriginal  bool   `json:"is_original,omitempty"`
}

type docReaderArtifactOverrideKind uint8

const (
	docReaderArtifactOverrideCredential docReaderArtifactOverrideKind = iota
	docReaderArtifactOverrideEndpoint
	docReaderArtifactOverrideBoolean
	docReaderArtifactOverrideString
)

type docReaderArtifactOverrideSpec struct {
	kind    docReaderArtifactOverrideKind
	engines map[string]struct{}
}

var docReaderArtifactKnownEngines = docReaderArtifactStringSet(
	"builtin", "markitdown", "mineru", "mineru_cloud", "opendataloader",
	"paddleocr_vl", "paddleocr_vl_cloud", "simple", "weknoracloud",
)

var docReaderArtifactOverrideSpecs = map[string]docReaderArtifactOverrideSpec{
	"mineru_api_key":           newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideCredential, "mineru_cloud", "opendataloader"),
	"paddleocr_vl_cloud_token": newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideCredential, "paddleocr_vl_cloud"),

	"mineru_endpoint":             newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideEndpoint, "mineru"),
	"mineru_vlm_server_url":       newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideEndpoint, "mineru"),
	"odl_hybrid_url":              newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideEndpoint, "opendataloader"),
	"paddleocr_vl_endpoint":       newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideEndpoint, "paddleocr_vl"),
	"paddleocr_vl_cloud_base_url": newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideEndpoint, "paddleocr_vl_cloud"),

	"mineru_enable_formula":                    newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "mineru"),
	"mineru_enable_table":                      newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "mineru"),
	"mineru_enable_ocr":                        newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "mineru"),
	"mineru_cloud_enable_formula":              newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "mineru_cloud"),
	"mineru_cloud_enable_table":                newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "mineru_cloud"),
	"mineru_cloud_enable_ocr":                  newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "mineru_cloud"),
	"odl_hybrid_fallback":                      newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "opendataloader"),
	"odl_markdown_with_html":                   newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "opendataloader"),
	"paddleocr_vl_use_seal_recognition":        newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "paddleocr_vl"),
	"paddleocr_vl_use_chart_recognition":       newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "paddleocr_vl"),
	"paddleocr_vl_cloud_use_seal_recognition":  newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "paddleocr_vl_cloud"),
	"paddleocr_vl_cloud_use_chart_recognition": newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "paddleocr_vl_cloud"),
	"pdf_force_scanned":                        newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideBoolean, "builtin"),

	"mineru_model":             newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideString, "mineru"),
	"mineru_language":          newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideString, "mineru"),
	"mineru_cloud_model":       newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideString, "mineru_cloud"),
	"mineru_cloud_language":    newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideString, "mineru_cloud"),
	"odl_hybrid":               newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideString, "opendataloader"),
	"odl_hybrid_mode":          newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideString, "opendataloader"),
	"paddleocr_vl_cloud_model": newDocReaderArtifactOverrideSpec(docReaderArtifactOverrideString, "paddleocr_vl_cloud"),
}

var docReaderArtifactAudioExtensions = map[string]struct{}{
	".aac":  {},
	".amr":  {},
	".flac": {},
	".m4a":  {},
	".mp3":  {},
	".ogg":  {},
	".opus": {},
	".wav":  {},
	".wma":  {},
}

func newDocReaderArtifactKey(
	request docReaderArtifactRequest,
) (types.ProcessingArtifactKey, bool, error) {
	if request.readRequest == nil {
		return types.ProcessingArtifactKey{}, false, errors.New("DocReader artifact request must not be nil")
	}
	if !isDocReaderArtifactCacheable(request) {
		return types.ProcessingArtifactKey{}, false, nil
	}

	engine := effectiveDocReaderArtifactEngine(
		request.readRequest.ParserEngine,
		request.readRequest.FileType,
	)
	readerIdentity := ""
	if docReaderArtifactEngineUsesSharedReader(engine) {
		readerIdentity = strings.TrimSpace(request.readerIdentity)
		if readerIdentity == "" {
			return types.ProcessingArtifactKey{}, false, nil
		}
	}
	overrides, eligible, err := canonicalDocReaderArtifactOverrides(
		engine,
		request.readRequest.FileType,
		request.readRequest.ParserEngineOverrides,
	)
	if err != nil || !eligible {
		return types.ProcessingArtifactKey{}, eligible, err
	}

	contentDigest := sha256.Sum256(request.readRequest.FileContent)
	key, err := types.NewProcessingArtifactKey(
		request.tenantID,
		docReaderArtifactStage,
		docReaderArtifactKeyVersion,
		contentDigest[:],
		[]byte(engine),
		[]byte(readerIdentity),
		[]byte(strings.ToLower(strings.TrimSpace(request.readRequest.FileType))),
		[]byte(strings.TrimSpace(filepath.Base(request.readRequest.FileName))),
		[]byte(request.readRequest.Title),
		overrides,
		[]byte(request.renderVersion),
	)
	if err != nil {
		return types.ProcessingArtifactKey{}, false, err
	}
	return key, true, nil
}

func isDocReaderArtifactCacheable(request docReaderArtifactRequest) bool {
	readRequest := request.readRequest
	if readRequest == nil || request.sourcePath == "" || len(readRequest.FileContent) == 0 {
		return false
	}
	if strings.TrimSpace(request.renderVersion) == "" || strings.TrimSpace(readRequest.URL) != "" {
		return false
	}
	if isTemporaryDocReaderArtifactPath(request.sourcePath) {
		return false
	}
	fileType := strings.ToLower(strings.TrimSpace(readRequest.FileType))
	if strings.HasPrefix(fileType, "audio/") || IsAudioType(fileType) {
		return false
	}
	_, audio := docReaderArtifactAudioExtensions[strings.ToLower(filepath.Ext(readRequest.FileName))]
	return !audio
}

func isTemporaryDocReaderArtifactPath(path string) bool {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	temporaryRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		return true
	}
	relative, err := filepath.Rel(temporaryRoot, absolutePath)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func canonicalDocReaderArtifactOverrides(
	parserEngine string,
	fileType string,
	overrides map[string]string,
) ([]byte, bool, error) {
	engine := effectiveDocReaderArtifactEngine(parserEngine, fileType)
	_, knownEngine := docReaderArtifactKnownEngines[engine]
	if !knownEngine && len(overrides) > 0 {
		return nil, false, nil
	}

	canonical := make([]docReaderArtifactOverride, 0, len(overrides))
	for key, value := range overrides {
		key = strings.ToLower(strings.TrimSpace(key))
		spec, knownOverride := docReaderArtifactOverrideSpecs[key]
		if !knownOverride {
			if docReaderArtifactUnknownOverrideAffectsEngine(engine, key) {
				return nil, false, nil
			}
			continue
		}
		if !docReaderArtifactOverrideIsEffective(engine, fileType, key, spec) {
			continue
		}
		if spec.kind == docReaderArtifactOverrideCredential {
			continue
		}

		switch spec.kind {
		case docReaderArtifactOverrideEndpoint:
			normalized, ok := normalizeDocReaderArtifactEndpoint(value)
			if !ok {
				return nil, false, nil
			}
			value = normalized
		case docReaderArtifactOverrideBoolean:
			value = strings.ToLower(strings.TrimSpace(value))
		case docReaderArtifactOverrideString:
			value = strings.TrimSpace(value)
		default:
			return nil, false, nil
		}
		canonical = append(canonical, docReaderArtifactOverride{Key: key, Value: value})
	}

	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Key < canonical[j].Key })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, false, fmt.Errorf("%w: canonicalize overrides: %w", errDocReaderArtifactCodec, err)
	}
	return encoded, true, nil
}

func effectiveDocReaderArtifactEngine(parserEngine, fileType string) string {
	engine := normalizeDocReaderArtifactEngine(parserEngine)
	if engine != "" {
		return engine
	}
	if docparser.IsSimpleFormat(fileType) {
		return "simple"
	}
	return "builtin"
}

func normalizeDocReaderArtifactEngine(engine string) string {
	return strings.ToLower(strings.TrimSpace(engine))
}

func docReaderArtifactEngineUsesSharedReader(engine string) bool {
	switch engine {
	case "simple", "mineru", "mineru_cloud", "paddleocr_vl", "paddleocr_vl_cloud", "weknoracloud":
		return false
	default:
		return true
	}
}

type docReaderArtifactIdentityProvider interface {
	ArtifactIdentity() string
}

func docReaderArtifactReaderIdentity(reader interfaces.DocReader) string {
	provider, ok := reader.(docReaderArtifactIdentityProvider)
	if !ok {
		return ""
	}
	return strings.TrimSpace(provider.ArtifactIdentity())
}

func docReaderArtifactStringSet(keys ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

func newDocReaderArtifactOverrideSpec(
	kind docReaderArtifactOverrideKind,
	engines ...string,
) docReaderArtifactOverrideSpec {
	return docReaderArtifactOverrideSpec{kind: kind, engines: docReaderArtifactStringSet(engines...)}
}

func docReaderArtifactOverrideIsEffective(
	engine string,
	fileType string,
	key string,
	spec docReaderArtifactOverrideSpec,
) bool {
	if engine == "builtin" && key == "pdf_force_scanned" &&
		strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fileType)), ".") != "pdf" {
		return false
	}
	_, ok := spec.engines[engine]
	return ok
}

func docReaderArtifactUnknownOverrideAffectsEngine(engine, key string) bool {
	switch engine {
	case "mineru":
		return strings.HasPrefix(key, "mineru_") && !strings.HasPrefix(key, "mineru_cloud_")
	case "mineru_cloud":
		return strings.HasPrefix(key, "mineru_cloud_")
	case "paddleocr_vl":
		return strings.HasPrefix(key, "paddleocr_vl_") && !strings.HasPrefix(key, "paddleocr_vl_cloud_")
	case "paddleocr_vl_cloud":
		return strings.HasPrefix(key, "paddleocr_vl_cloud_")
	case "opendataloader":
		return strings.HasPrefix(key, "odl_")
	case "builtin":
		namespace := docReaderArtifactOverrideNamespace(key)
		return namespace == "" || namespace == "pdf_"
	case "markitdown":
		return docReaderArtifactOverrideNamespace(key) == ""
	default:
		return false
	}
}

func docReaderArtifactOverrideNamespace(key string) string {
	for _, prefix := range []string{"mineru_cloud_", "mineru_", "paddleocr_vl_cloud_", "paddleocr_vl_", "odl_", "pdf_"} {
		if strings.HasPrefix(key, prefix) {
			return prefix
		}
	}
	return ""
}

func normalizeDocReaderArtifactEndpoint(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if raw != trimmed {
		return "", false
	}
	endpoint, err := url.Parse(trimmed)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return "", false
	}
	endpoint.Scheme = strings.ToLower(endpoint.Scheme)
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return "", false
	}
	endpoint.Host = strings.ToLower(endpoint.Host)
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", false
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	return endpoint.String(), true
}

func encodeDocReaderArtifact(result *types.ReadResult) ([]byte, error) {
	if result == nil {
		return nil, fmt.Errorf("%w: result must not be nil", errDocReaderArtifactCodec)
	}
	payload := docReaderArtifactPayload{
		MarkdownContent: result.MarkdownContent,
		Metadata:        result.Metadata,
		ImageRefs:       make([]docReaderArtifactImage, len(result.ImageRefs)),
	}
	for i, image := range result.ImageRefs {
		payload.ImageRefs[i] = docReaderArtifactImage{
			Filename:    image.Filename,
			OriginalRef: image.OriginalRef,
			MimeType:    image.MimeType,
			ImageData:   image.ImageData,
			IsOriginal:  image.IsOriginal,
		}
	}

	var compressed bytes.Buffer
	compressed.WriteByte(docReaderArtifactCodecVersion)
	writer := gzip.NewWriter(&compressed)
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("%w: encode: %w", errDocReaderArtifactCodec, err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("%w: close compressor: %w", errDocReaderArtifactCodec, err)
	}
	return compressed.Bytes(), nil
}

func decodeDocReaderArtifact(payload []byte) (*types.ReadResult, error) {
	if len(payload) < 2 || payload[0] != docReaderArtifactCodecVersion {
		return nil, fmt.Errorf("%w: unsupported or missing version", errDocReaderArtifactCodec)
	}
	reader, err := gzip.NewReader(bytes.NewReader(payload[1:]))
	if err != nil {
		return nil, fmt.Errorf("%w: decompress: %w", errDocReaderArtifactCodec, err)
	}
	var cached docReaderArtifactPayload
	decoder := json.NewDecoder(reader)
	decodeErr := decoder.Decode(&cached)
	if decodeErr == nil {
		var trailing json.RawMessage
		trailingErr := decoder.Decode(&trailing)
		switch {
		case errors.Is(trailingErr, io.EOF):
		case trailingErr == nil:
			decodeErr = errors.New("multiple JSON values")
		default:
			decodeErr = trailingErr
		}
	}
	closeErr := reader.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("%w: decode: %w", errDocReaderArtifactCodec, decodeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w: close decompressor: %w", errDocReaderArtifactCodec, closeErr)
	}
	result := &types.ReadResult{
		MarkdownContent: cached.MarkdownContent,
		Metadata:        cached.Metadata,
		ImageRefs:       make([]types.ImageRef, len(cached.ImageRefs)),
	}
	for i, image := range cached.ImageRefs {
		result.ImageRefs[i] = types.ImageRef{
			Filename:    image.Filename,
			OriginalRef: image.OriginalRef,
			MimeType:    image.MimeType,
			ImageData:   image.ImageData,
			IsOriginal:  image.IsOriginal,
		}
	}
	return result, nil
}

func readDocReaderArtifact(
	ctx context.Context,
	store interfaces.ProcessingArtifactStore,
	request docReaderArtifactRequest,
) (*types.ReadResult, bool, error) {
	if request.read == nil {
		return nil, false, errors.New("DocReader artifact reader must not be nil")
	}
	if request.readRequest == nil {
		return nil, false, errors.New("DocReader artifact request must not be nil")
	}
	if store == nil {
		result, err := callDocReaderArtifactProvider(ctx, request)
		return result, false, err
	}

	key, eligible, err := newDocReaderArtifactKey(request)
	if err != nil {
		return nil, false, err
	}
	if !eligible {
		result, err := callDocReaderArtifactProvider(ctx, request)
		return result, false, err
	}

	cached, hit, err := store.Get(ctx, key)
	if err != nil {
		return nil, false, fmt.Errorf("%w: get: %w", errDocReaderArtifactStore, err)
	}
	if hit {
		if result, decodeErr := decodeDocReaderArtifact(cached); decodeErr == nil {
			return result, true, nil
		}
	}

	fresh, err := callDocReaderArtifactProvider(ctx, request)
	if err != nil || fresh.Error != "" || fresh.IsAudio || len(fresh.AudioData) > 0 {
		return fresh, false, err
	}
	payload, err := encodeDocReaderArtifact(fresh)
	if err != nil {
		return nil, false, err
	}
	canonical, _, err := store.PutIfAbsent(ctx, key, payload)
	if err != nil {
		return nil, false, fmt.Errorf("%w: put: %w", errDocReaderArtifactStore, err)
	}
	if winner, decodeErr := decodeDocReaderArtifact(canonical); decodeErr == nil {
		return winner, false, nil
	}
	return fresh, false, nil
}

func callDocReaderArtifactProvider(
	ctx context.Context,
	request docReaderArtifactRequest,
) (*types.ReadResult, error) {
	result, err := request.read(ctx, request.readRequest)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errDocReaderArtifactProvider, err)
	}
	if result == nil {
		return nil, fmt.Errorf("%w: reader returned nil result", errDocReaderArtifactProvider)
	}
	return result, nil
}

package docparser

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// ParserCacheIdentity hashes effective reader configuration. Remote protocols do
// not report a trustworthy deployment version: explicitly version those engines
// with DOCREADER_CACHE_VERSION, otherwise reuse is conservatively disabled.
func ParserCacheIdentity(reader interfaces.DocReader, req *types.ReadRequest) string {
	if reader == nil {
		return ""
	}
	if identity, ok := reader.(interface{ ProcessingCacheIdentity() string }); ok {
		return identity.ProcessingCacheIdentity()
	}
	versioned := func(kind string, config any) string {
		version := os.Getenv("DOCREADER_CACHE_VERSION")
		if version == "" {
			return ""
		}
		return processingcache.Digest([]any{"parser.v1", kind, version, config})
	}
	switch r := reader.(type) {
	case *SimpleFormatReader:
		return processingcache.Digest([]any{"simple.reader.v1"})
	case *AnydocReader:
		fallback := ""
		if req != nil && fileTypeOf(req) == "pdf" && r.fallback != nil {
			fallback = ParserCacheIdentity(r.fallback, req)
			if fallback == "" {
				return ""
			}
		}
		return processingcache.Digest([]any{"anydoc.reader.v1", anydoc.Version(), r.extractImages, fallback})
	case *HTTPDocumentReader:
		return versioned("http", r.base())
	case *MinerUReader:
		return versioned("mineru", []any{r.endpoint, r.timeout, r.apiKey, r.tier, r.backend, r.vlmServerURL, r.formulaEnable, r.tableEnable, r.language, r.parseMethod})
	case *MinerUCloudReader:
		return versioned("mineru.cloud", []any{r.apiKey, r.baseURL, r.timeout, r.pollInterval, r.model, r.formulaEnable, r.tableEnable, r.ocrEnable, r.language})
	case *PaddleOCRVLReader:
		return versioned("paddleocr.vl", []any{r.endpoint, r.timeout, r.useSeal, r.useChart})
	case *PaddleOCRVLCloudReader:
		return versioned("paddleocr.vl.cloud", []any{r.token, r.baseURL, r.model, r.useSeal, r.useChart, r.timeout, r.pollInterval})
	case *WeKnoraCloudSignedDocumentReader:
		return versioned("weknora.cloud", []any{weKnoraCloudReaderBaseURL, r.appID, r.apiKey, r.initialPollInterval, r.maxPollInterval, r.pollTimeout})
	}
	return ""
}

// ValidateProcessingCacheArtifact rejects path-dependent and partial results.
// The returned parser result can still be consumed by its original live path;
// failure here only refuses persistent reuse.
func ValidateProcessingCacheArtifact(result *types.ReadResult) error {
	if result == nil || result.Error != "" {
		return fmt.Errorf("nil or failed parser artifact")
	}
	if result.Metadata["processing_cache_incomplete"] != "" {
		return fmt.Errorf("partial parser images")
	}
	if result.ImageDirPath != "" {
		return fmt.Errorf("temporary parser image directory")
	}
	if result.IsAudio && len(result.AudioData) == 0 {
		return fmt.Errorf("missing parser audio")
	}
	refs := map[string]types.ImageRef{}
	for _, ref := range result.ImageRefs {
		if len(ref.ImageData) == 0 || ref.StorageKey != "" || ref.OriginalRef == "" || ephemeralParserReference(ref.OriginalRef) || filepath.Base(ref.Filename) != ref.Filename {
			return fmt.Errorf("nonportable parser image")
		}
		if err := validateParserImageBytes(ref.ImageData); err != nil {
			return err
		}
		refs[ref.OriginalRef] = ref
	}
	// Read HTML separately: the legacy regex also returns entire Markdown
	// targets (including titles), which are not image reference identities.
	targets := extractImageRefsFromContent(StripMarkdownImages(result.MarkdownContent))
	for _, span := range scanMarkdownImageTargets(result.MarkdownContent) {
		raw := result.MarkdownContent[span.TargetStart:span.TargetEnd]
		if ref, _, _, ok := splitMarkdownImageTarget(raw, refs); ok {
			targets = append(targets, ref)
		} else {
			raw = strings.TrimSpace(raw)
			if title, ok := parseMarkdownImageTitleSuffix(raw); ok {
				raw = strings.TrimSpace(raw[:title])
			}
			if strings.HasPrefix(raw, "<") && strings.HasSuffix(raw, ">") {
				raw = raw[1 : len(raw)-1]
			}
			targets = append(targets, raw)
		}
	}
	for _, target := range targets {
		if strings.HasPrefix(target, "data:image/") {
			i := strings.Index(target, ",")
			if i < 0 {
				return fmt.Errorf("invalid inline parser image")
			}
			data, err := base64.StdEncoding.DecodeString(target[i+1:])
			if err != nil {
				return err
			}
			if err = validateParserImageBytes(data); err != nil {
				return err
			}
			continue
		}
		if _, ok := refs[target]; !ok {
			return fmt.Errorf("parser image %q has no portable bytes", target)
		}
	}
	return nil
}
func ephemeralParserReference(ref string) bool {
	return filepath.IsAbs(ref) || strings.HasPrefix(ref, "/") || strings.Contains(ref, `:\`) || strings.Contains(ref, "://") || strings.HasPrefix(ref, `\\`)
}
func validateParserImageBytes(data []byte) error {
	if bytes.Contains(data, []byte("<svg")) {
		var root struct{ XMLName xml.Name }
		if err := xml.Unmarshal(data, &root); err == nil && root.XMLName.Local == "svg" {
			return nil
		}
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 32<<20 {
		return fmt.Errorf("parser image exceeds cache validation pixel budget")
	}
	_, _, err = image.Decode(bytes.NewReader(data))
	return err
}

package docparser

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// AnydocReader converts office documents to markdown in this process, using
// the anydoc converter instead of the Python docreader service.
//
// Embedded images are rendered in place from the document model. PDFs with no
// text layer (scanned exam papers) cannot be converted by anydoc; when a
// fallback reader is configured (typically the builtin docreader) those files
// are handed off so pages can be rasterized for OCR.
type AnydocReader struct {
	// extractImages controls whether embedded images are parsed and placed
	// in the markdown. Off halves parse time for text-only indexing.
	extractImages bool
	// fallback is used when a PDF has no extractable text. Nil means the
	// conversion error is returned to the caller.
	fallback interfaces.DocReader
}

// NewAnydocReader builds a reader. The "anydoc_extract_images" override turns
// off image extraction. fallback may be nil.
func NewAnydocReader(overrides map[string]string, fallback interfaces.DocReader) *AnydocReader {
	return &AnydocReader{
		extractImages: !isFalsey(overrides["anydoc_extract_images"]),
		fallback:      fallback,
	}
}

// Read converts the document carried by the request.
func (r *AnydocReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if req.URL != "" && len(req.FileContent) == 0 {
		return nil, fmt.Errorf("anydoc engine reads uploaded documents, not URLs")
	}

	format, ok := anydoc.FormatForFile(req.FileType, req.FileName)
	if !ok {
		return nil, fmt.Errorf("anydoc engine does not support file type %q", fileTypeOf(req))
	}

	converted, err := anydoc.Convert(req.FileContent, anydoc.Options{
		Format:     format,
		WithAssets: r.extractImages,
	})
	if format == "pdf" && (anydoc.PDFNeedsOCR(err) || pdfMarkdownIsEmpty(converted, err)) {
		return r.readScannedPDF(ctx, req, err)
	}
	if err != nil {
		return nil, fmt.Errorf("anydoc conversion failed for %q: %w", req.FileName, err)
	}
	if converted.AssetsError != nil {
		logger.Warnf(ctx, "[anydoc] %q parsed as text but its images were dropped: %v",
			req.FileName, converted.AssetsError)
	}

	return &types.ReadResult{
		MarkdownContent: converted.Markdown,
		ImageRefs:       imageRefsFromAssets(converted.Assets),
		Metadata: map[string]string{
			"parser":         AnydocEngineName,
			"anydoc_version": anydoc.Version(),
			"source_format":  format,
		},
	}, nil
}

func pdfMarkdownIsEmpty(converted *anydoc.Result, err error) bool {
	if err != nil || converted == nil {
		return false
	}
	return strings.TrimSpace(converted.Markdown) == ""
}

func (r *AnydocReader) readScannedPDF(ctx context.Context, req *types.ReadRequest, convertErr error) (*types.ReadResult, error) {
	if !r.canFallback() {
		if convertErr != nil {
			return nil, fmt.Errorf("anydoc conversion failed for %q: %w", req.FileName, convertErr)
		}
		return nil, fmt.Errorf("anydoc conversion failed for %q: PDF has no extractable text; OCR is required", req.FileName)
	}

	logger.Infof(ctx, "[anydoc] %q has no text layer, falling back to builtin for scanned-PDF OCR", req.FileName)
	fallbackReq := *req
	fallbackReq.ParserEngine = BuiltinEngineName
	result, err := r.fallback.Read(ctx, &fallbackReq)
	if err != nil {
		return nil, fmt.Errorf("anydoc scanned-PDF fallback failed for %q: %w", req.FileName, err)
	}
	if result == nil {
		return nil, fmt.Errorf("anydoc scanned-PDF fallback returned no result for %q", req.FileName)
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	if result.Metadata["parser"] == "" {
		result.Metadata["parser"] = BuiltinEngineName
	}
	result.Metadata["anydoc_fallback"] = "scanned_pdf"
	if result.Metadata["image_source_type"] == "" {
		result.Metadata["image_source_type"] = "scanned_pdf"
	}
	return result, nil
}

func (r *AnydocReader) canFallback() bool {
	if r.fallback == nil {
		return false
	}
	if connected, ok := r.fallback.(interface{ IsConnected() bool }); ok {
		return connected.IsConnected()
	}
	return true
}

// imageRefsFromAssets turns extracted assets into ImageRefs that match the
// in-place markdown links renderMarkdown wrote.
func imageRefsFromAssets(assets []anydoc.Asset) []types.ImageRef {
	if len(assets) == 0 {
		return nil
	}
	refs := make([]types.ImageRef, 0, len(assets))
	for _, asset := range assets {
		ref := anydoc.ImageDir + asset.Name
		refs = append(refs, types.ImageRef{
			Filename:    asset.Name,
			OriginalRef: ref,
			MimeType:    asset.MediaType,
			ImageData:   asset.Data,
		})
	}
	return refs
}

// fileTypeOf reports the request's file type, falling back to the file name's
// extension the way every reader in this package does.
func fileTypeOf(req *types.ReadRequest) string {
	if ft := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(req.FileType)), "."); ft != "" {
		return ft
	}
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(req.FileName)), ".")
}

func isFalsey(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "no", "off":
		return true
	}
	return false
}

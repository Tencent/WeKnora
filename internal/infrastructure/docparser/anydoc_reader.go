package docparser

import (
	"archive/zip"
	"bytes"
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
// text layer or a structure anydoc rejects are handed to the configured
// fallback reader (typically the builtin docreader). PPTX files with EMF/WMF
// media use the docreader's MarkItDown path to rasterize those embedded images.
type AnydocReader struct {
	// extractImages controls whether embedded images are parsed and placed
	// in the markdown. Off halves parse time for text-only indexing.
	extractImages bool
	// fallback handles PDFs requiring OCR/structure recovery and PPTX vector
	// media. Nil means the required conversion is reported as an error.
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
	// Anydoc preserves vector bytes, but cannot rasterize EMF/WMF for the
	// browser or VLM. MarkItDown's PPTX path already provides that conversion.
	// Check the actual extension: anydoc also maps PPTM to its PPTX parser,
	// but docreader does not support PPTM.
	if r.extractImages && fileTypeOf(req) == "pptx" && pptxHasVectorMedia(req.FileContent) {
		return r.readVectorPPTX(ctx, req)
	}

	converted, err := anydoc.Convert(req.FileContent, anydoc.Options{
		Format:     format,
		WithAssets: r.extractImages,
	})
	if format == "pdf" && anydoc.PDFInvalidStructure(err) {
		return r.readPDFFallback(ctx, req, err, "invalid_structure")
	}
	if format == "pdf" && (anydoc.PDFNeedsOCR(err) || pdfMarkdownIsEmpty(converted, err)) {
		return r.readPDFFallback(ctx, req, err, "scanned_pdf")
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

// Inspect only the ZIP directory: no media decompression or filesystem writes
// are needed to decide which reader can preserve the presentation's images.
func pptxHasVectorMedia(data []byte) bool {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false // Let the converter report malformed input normally.
	}
	for _, file := range archive.File {
		if file.FileInfo().IsDir() || !strings.HasPrefix(file.Name, "ppt/media/") {
			continue
		}
		switch strings.ToLower(filepath.Ext(file.Name)) {
		case ".emf", ".wmf":
			return true
		}
	}
	return false
}

func (r *AnydocReader) readVectorPPTX(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if !r.canFallback() {
		return nil, fmt.Errorf(
			"anydoc conversion of %q with EMF/WMF images requires a connected docreader "+
				"with MarkItDown and ImageMagick",
			req.FileName)
	}
	logger.Infof(ctx, "[anydoc] %q contains EMF/WMF images, falling back to markitdown for rasterization", req.FileName)
	fallbackReq := *req
	fallbackReq.ParserEngine = "markitdown"
	result, err := r.fallback.Read(ctx, &fallbackReq)
	if err != nil {
		return nil, fmt.Errorf("anydoc PPTX vector-media fallback failed for %q: %w", req.FileName, err)
	}
	if result == nil {
		return nil, fmt.Errorf("anydoc PPTX vector-media fallback returned no result for %q", req.FileName)
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	if result.Metadata["parser"] == "" {
		result.Metadata["parser"] = "markitdown"
	}
	result.Metadata["anydoc_fallback"] = "pptx_vector_media"
	return result, nil
}

func pdfMarkdownIsEmpty(converted *anydoc.Result, err error) bool {
	if err != nil || converted == nil {
		return false
	}
	return strings.TrimSpace(converted.Markdown) == ""
}

func (r *AnydocReader) readPDFFallback(
	ctx context.Context, req *types.ReadRequest, convertErr error, reason string,
) (*types.ReadResult, error) {
	if !r.canFallback() {
		if convertErr != nil {
			return nil, fmt.Errorf("anydoc conversion failed for %q: %w", req.FileName, convertErr)
		}
		return nil, fmt.Errorf("anydoc conversion failed for %q: PDF has no extractable text; OCR is required", req.FileName)
	}

	logger.Infof(ctx, "[anydoc] falling back to builtin for %q: %s", req.FileName, reason)
	fallbackReq := *req
	fallbackReq.ParserEngine = BuiltinEngineName
	result, err := r.fallback.Read(ctx, &fallbackReq)
	if err != nil {
		return nil, fmt.Errorf("anydoc PDF fallback (%s) failed for %q: %w", reason, req.FileName, err)
	}
	if result == nil {
		return nil, fmt.Errorf("anydoc PDF fallback (%s) returned no result for %q", reason, req.FileName)
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	if result.Metadata["parser"] == "" {
		result.Metadata["parser"] = BuiltinEngineName
	}
	result.Metadata["anydoc_fallback"] = reason
	if reason == "scanned_pdf" && result.Metadata["image_source_type"] == "" {
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

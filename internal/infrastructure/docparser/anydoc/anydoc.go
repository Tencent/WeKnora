// Package anydoc converts office documents to Markdown inside the Go process,
// without the Python docreader service.
//
// The conversion itself is the anydoc Rust library, linked as a static archive
// through cgo (see third_party/anydoc-go). That archive needs a Rust toolchain
// to produce, so it is opt-in: only builds tagged `anydoc` link it. Every other
// build compiles the stub in backend_stub.go, where Available reports false and
// the engine registry hides the engine with a reason the UI can show.
//
// The exported surface is deliberately narrow — bytes in, Markdown and embedded
// images out — so the backend can be replaced (a WASI runtime, a future
// upstream Go module) without touching the parser code that calls it.
package anydoc

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// ImageDir is the markdown path prefix for extracted images. The image
// resolver matches references by this path and swaps them for storage URLs.
const ImageDir = "images/"

// Result is one converted document.
type Result struct {
	// Markdown is GitHub-Flavored Markdown for the whole document.
	// When WithAssets is set, embedded images appear in place as
	// `![alt](images/image-N.ext)` via anydoc's official serializer.
	Markdown string
	// Assets are the images embedded in the document, in document order.
	// Always empty for PDF, which anydoc renders straight to Markdown
	// without a document model.
	Assets []Asset
	// AssetsError explains why Assets is empty when images were asked for.
	// The document-model parse is what both extracts images and places them
	// in the markdown; when it fails, Markdown still comes from the text-only
	// renderer so the conversion succeeds and the caller decides whether to
	// log or ignore the loss.
	AssetsError error
}

// Asset is one image embedded in a document.
type Asset struct {
	// ID is the document-model asset index, as referenced by in-place image
	// sources. It is stable for a single conversion.
	ID uint64
	// Name is a generated, extension-carrying file name ("image-1.png"):
	// embedded assets have no name of their own inside the container.
	Name string
	// MediaType is the IANA media type the container declared.
	MediaType string
	// Data is the raw image bytes.
	Data []byte
	// Alt is the image's alternative text as the document author wrote it,
	// empty when the document carries none.
	Alt string
	// Section is the text of the nearest heading above the image, empty when
	// the image sits before the first heading.
	Section string
}

// Options tunes a single conversion.
type Options struct {
	// Format names the parser explicitly ("docx", "csv", ...). Empty means
	// the format is detected from the content, which every format except
	// CSV carries a signature for.
	Format string
	// WithAssets extracts embedded images and renders them in place in the
	// markdown using anydoc's official GFM serializer (asset images are
	// rewritten to `images/image-N.ext` links first). Ignored for PDF.
	WithAssets bool
}

// ErrUnavailable is returned by Convert when the binding is not linked into
// this build. Callers that can fall back to another engine should check for it
// with errors.Is.
var ErrUnavailable = errors.New("anydoc: binding not built into this binary")

// supportedExtensions are the file types anydoc converts, mapped to the format
// name its parser selector uses. The list is static so that engine metadata
// (file types shown in the UI) is identical whether or not the binding is
// linked in.
var supportedExtensions = map[string]string{
	"doc":  "doc",
	"docx": "docx",
	"docm": "docx",
	"odt":  "odt",
	"rtf":  "rtf",
	"ppt":  "ppt",
	"pptx": "pptx",
	"pptm": "pptx",
	"odp":  "odp",
	"xls":  "xlsx",
	"xlsx": "xlsx",
	"xlsm": "xlsx",
	"ods":  "ods",
	"epub": "epub",
	"csv":  "csv",
	"pdf":  "pdf",
}

// SupportedFileTypes returns the extensions anydoc can convert, sorted so the
// engine list is stable across restarts.
func SupportedFileTypes() []string {
	types := make([]string, 0, len(supportedExtensions))
	for ext := range supportedExtensions {
		types = append(types, ext)
	}
	slices.Sort(types)
	return types
}

// FormatForFile resolves the anydoc format name for a file type or file name.
// ok is false for anything anydoc does not convert.
func FormatForFile(fileType, fileName string) (format string, ok bool) {
	ext := normalizeExt(fileType)
	if ext == "" {
		ext = normalizeExt(filepath.Ext(fileName))
	}
	format, ok = supportedExtensions[ext]
	return format, ok
}

// Supports reports whether anydoc converts this file type, regardless of
// whether the binding is linked into this build.
func Supports(fileType, fileName string) bool {
	_, ok := FormatForFile(fileType, fileName)
	return ok
}

// Available reports whether conversions can actually run in this binary.
func Available() bool { return backendAvailable() }

// UnavailableReason explains, for the UI, why Available is false. It returns
// "" when the binding is available.
func UnavailableReason() string { return backendUnavailableReason() }

// Version reports the anydoc release the linked binding was built from, or ""
// when no binding is linked.
func Version() string { return backendVersion() }

// Convert turns document bytes into Markdown. It returns ErrUnavailable when
// the binding is not linked into this build.
func Convert(data []byte, opts Options) (*Result, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("anydoc: empty document")
	}
	if !backendAvailable() {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, backendUnavailableReason())
	}
	// PDF has no document model, so asset extraction would only produce an
	// error the caller has to special-case. Drop the request instead.
	if opts.Format == "pdf" {
		opts.WithAssets = false
	}
	return backendConvert(data, opts)
}

// PDFNeedsOCR reports whether a Convert error means the PDF has no usable
// text layer. Scanned exam papers fail this way; the caller should fall back
// to an engine that can rasterize pages for OCR.
func PDFNeedsOCR(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "ocr is required") ||
		strings.Contains(msg, "no extractable text")
}

func normalizeExt(s string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "."))
}

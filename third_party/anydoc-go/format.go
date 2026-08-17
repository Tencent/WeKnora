package anydoc

/*
#include "include/anydoc.h"
*/
import "C"

import (
	"strings"
	"unsafe"
)

// Format names a document format. The string values are the lowercase
// extension names, matching the Node and Python bindings ("doc", "docx",
// "pdf", ...). Use the exported constants rather than raw strings where
// possible: the constants are pinned to the C ABI's tag values at build
// time.
type Format string

// Format constants. Every value is stable across versions.
const (
	FormatDoc  Format = "doc"
	FormatDocx Format = "docx"
	FormatOdt  Format = "odt"
	FormatPdf  Format = "pdf"
	FormatPpt  Format = "ppt"
	FormatPptx Format = "pptx"
	FormatRtf  Format = "rtf"
	FormatEpub Format = "epub"
	FormatXlsx Format = "xlsx"
	FormatOds  Format = "ods"
	FormatOdp  Format = "odp"
	FormatCsv  Format = "csv"
)

// formatToTag maps a Format to its C ABI tag. Returns ANYDOC_FORMAT_NONE for
// an unknown format.
func formatToTag(f Format) C.int {
	switch f {
	case FormatDoc:
		return C.int(C.ANYDOC_FORMAT_DOC)
	case FormatDocx:
		return C.int(C.ANYDOC_FORMAT_DOCX)
	case FormatOdt:
		return C.int(C.ANYDOC_FORMAT_ODT)
	case FormatPdf:
		return C.int(C.ANYDOC_FORMAT_PDF)
	case FormatPpt:
		return C.int(C.ANYDOC_FORMAT_PPT)
	case FormatPptx:
		return C.int(C.ANYDOC_FORMAT_PPTX)
	case FormatRtf:
		return C.int(C.ANYDOC_FORMAT_RTF)
	case FormatEpub:
		return C.int(C.ANYDOC_FORMAT_EPUB)
	case FormatXlsx:
		return C.int(C.ANYDOC_FORMAT_XLSX)
	case FormatOds:
		return C.int(C.ANYDOC_FORMAT_ODS)
	case FormatOdp:
		return C.int(C.ANYDOC_FORMAT_ODP)
	case FormatCsv:
		return C.int(C.ANYDOC_FORMAT_CSV)
	}
	return C.int(C.ANYDOC_FORMAT_NONE)
}

// tagToFormat maps a C ABI tag back to a Format. ok is false for NONE or an
// unknown tag.
func tagToFormat(tag C.int) (Format, bool) {
	switch tag {
	case C.ANYDOC_FORMAT_DOC:
		return FormatDoc, true
	case C.ANYDOC_FORMAT_DOCX:
		return FormatDocx, true
	case C.ANYDOC_FORMAT_ODT:
		return FormatOdt, true
	case C.ANYDOC_FORMAT_PDF:
		return FormatPdf, true
	case C.ANYDOC_FORMAT_PPT:
		return FormatPpt, true
	case C.ANYDOC_FORMAT_PPTX:
		return FormatPptx, true
	case C.ANYDOC_FORMAT_RTF:
		return FormatRtf, true
	case C.ANYDOC_FORMAT_EPUB:
		return FormatEpub, true
	case C.ANYDOC_FORMAT_XLSX:
		return FormatXlsx, true
	case C.ANYDOC_FORMAT_ODS:
		return FormatOds, true
	case C.ANYDOC_FORMAT_ODP:
		return FormatOdp, true
	case C.ANYDOC_FORMAT_CSV:
		return FormatCsv, true
	}
	return "", false
}

// FormatFromBytes detects the format from the content itself: the signature
// and identity each container specification designates (PDF header, RTF open
// group, OLE stream names, ZIP package mimetype/content types). Plain-text
// formats (CSV) carry no signature and return the zero Format and ok=false;
// so does anything unrecognized.
func FormatFromBytes(data []byte) (f Format, ok bool) {
	if len(data) == 0 {
		return "", false
	}
	var tag C.int
	code := C.anydoc_format_from_bytes((*C.uint8_t)(unsafe.Pointer(&data[0])), C.uintptr_t(len(data)), &tag)
	if code != errOK {
		return "", false
	}
	return tagToFormat(tag)
}

// FormatFromExtension returns the format a bare extension names (with or
// without a leading dot), matched case-insensitively. ok is false for
// anything unrecognized.
func FormatFromExtension(ext string) (f Format, ok bool) {
	ext = strings.TrimPrefix(ext, ".")
	cext := C.CString(strings.ToLower(ext))
	defer C.free(unsafe.Pointer(cext))
	var tag C.int
	code := C.anydoc_format_from_extension(cext, &tag)
	if code != errOK {
		return "", false
	}
	return tagToFormat(tag)
}

// FormatFromPath returns the format a path's extension names. ok is false
// when the path has no extension or names nothing recognized.
func FormatFromPath(path string) (f Format, ok bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var tag C.int
	code := C.anydoc_format_from_path(cpath, &tag)
	if code != errOK {
		return "", false
	}
	return tagToFormat(tag)
}

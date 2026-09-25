// Package anydoc converts documents (Word, PowerPoint, Excel, OpenDocument,
// RTF, EPUB, CSV, and PDF) to GitHub-Flavored Markdown, with full access to
// the parsed document model and embedded assets.
//
// This is the Go binding. It links a Rust static library through cgo; the
// module packages archives for every supported platform. See the README for
// the supported platform matrix and musl build tag.
package anydoc

/*
#cgo CFLAGS: -I${SRCDIR}/include
#cgo darwin,arm64 LDFLAGS: -L${SRCDIR}/lib/darwin_arm64 -lanydoc_go -lm -lstdc++
#cgo darwin,amd64 LDFLAGS: -L${SRCDIR}/lib/darwin_amd64 -lanydoc_go -lm -lstdc++
#cgo windows,amd64 LDFLAGS: -L${SRCDIR}/lib/windows_amd64 -lanydoc_go

#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "include/anydoc.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"
)

// call runs one ABI entry point and turns its status code into an error.
//
// The goroutine is pinned for the duration because the ABI reports the error
// message through a thread-local slot that a *second* call
// (`anydoc_last_error`) reads. Go is free to resume a goroutine on a different
// OS thread after a cgo call returns, and a conversion is long enough for that
// to happen: the message would then be missing, or belong to another
// conversion that ran on the thread we landed on.
func call(entry func() C.int) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if code := entry(); code != errOK {
		return convertError(code)
	}
	return nil
}

// ToMarkdown converts a document file to Markdown. The format is detected
// from the file content; the extension is the fallback for signature-less
// formats (CSV) and unrecognizable containers.
func ToMarkdown(path string) (string, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var out *C.char
	var outLen C.uintptr_t
	if err := call(func() C.int { return C.anydoc_to_markdown(cpath, &out, &outLen) }); err != nil {
		return "", err
	}
	defer C.anydoc_string_free(out)
	markdown, err := cStringN(out, outLen)
	if err != nil {
		return "", err
	}
	return markdown, nil
}

// ToMarkdownBytes converts an in-memory document to Markdown. Pass a Format
// to select the parser, or nil to detect it from the content, which
// signature-less formats (CSV) have to name explicitly.
func ToMarkdownBytes(data []byte, format *Format) (string, error) {
	if len(data) == 0 {
		return "", &ConvertError{Kind: "unsupported", Detail: "empty input"}
	}
	tag := C.int(C.ANYDOC_FORMAT_NONE)
	if format != nil {
		tag = formatToTag(*format)
		if tag == C.int(C.ANYDOC_FORMAT_NONE) {
			return "", &ConvertError{Kind: "unknown_format", Detail: "unknown format: " + string(*format)}
		}
	}
	var out *C.char
	var outLen C.uintptr_t
	if err := call(func() C.int {
		return C.anydoc_to_markdown_bytes(
			(*C.uint8_t)(unsafe.Pointer(&data[0])), C.uintptr_t(len(data)), tag, &out, &outLen,
		)
	}); err != nil {
		return "", err
	}
	defer C.anydoc_string_free(out)
	markdown, err := cStringN(out, outLen)
	if err != nil {
		return "", err
	}
	return markdown, nil
}

// ToMarkdownWithAssetLinks converts an in-memory document to Markdown with
// embedded images rewritten as `![alt](images/image-N.ext)` so they keep
// their original positions. The Markdown itself is produced by anydoc's
// official GFM serializer.
func ToMarkdownWithAssetLinks(data []byte, format *Format) (string, error) {
	if len(data) == 0 {
		return "", &ConvertError{Kind: "unsupported", Detail: "empty input"}
	}
	tag := C.int(C.ANYDOC_FORMAT_NONE)
	if format != nil {
		tag = formatToTag(*format)
		if tag == C.int(C.ANYDOC_FORMAT_NONE) {
			return "", &ConvertError{Kind: "unknown_format", Detail: "unknown format: " + string(*format)}
		}
	}
	var out *C.char
	var outLen C.uintptr_t
	if err := call(func() C.int {
		return C.anydoc_to_markdown_with_asset_links(
			(*C.uint8_t)(unsafe.Pointer(&data[0])), C.uintptr_t(len(data)), tag, &out, &outLen,
		)
	}); err != nil {
		return "", err
	}
	defer C.anydoc_string_free(out)
	markdown, err := cStringN(out, outLen)
	if err != nil {
		return "", err
	}
	return markdown, nil
}

// ToDocument parses an in-memory document into the document model, which also
// carries the embedded assets. Pass a Format to select the parser, or nil to
// detect it from the content.
//
// Unsupported for PDF: PDF conversion produces Markdown directly and has no
// document-model form; use ToMarkdownBytes.
func ToDocument(data []byte, format *Format) (*Document, error) {
	if len(data) == 0 {
		return nil, &ConvertError{Kind: "unsupported", Detail: "empty input"}
	}
	tag := C.int(C.ANYDOC_FORMAT_NONE)
	if format != nil {
		tag = formatToTag(*format)
		if tag == C.int(C.ANYDOC_FORMAT_NONE) {
			return nil, &ConvertError{Kind: "unknown_format", Detail: "unknown format: " + string(*format)}
		}
	}
	var buf *C.uint8_t
	var bufLen C.uintptr_t
	if err := call(func() C.int {
		return C.anydoc_to_document(
			(*C.uint8_t)(unsafe.Pointer(&data[0])), C.uintptr_t(len(data)), tag, &buf, &bufLen,
		)
	}); err != nil {
		return nil, err
	}
	defer C.anydoc_buffer_free(buf, bufLen)
	if buf == nil || bufLen == 0 {
		return &Document{}, nil
	}
	// Copy the C buffer into a Go-owned slice before decoding, so the C
	// buffer can be freed immediately rather than tying its lifetime to the
	// decoded Document.
	raw, err := cGoBytes(unsafe.Pointer(buf), bufLen)
	if err != nil {
		return nil, err
	}
	return decodeDocument(raw)
}

// cStringN reads a length-prefixed C buffer as a Go string. The bytes are
// copied, so the caller may free the C buffer immediately after.
func cStringN(s *C.char, n C.uintptr_t) (string, error) {
	if s == nil || n == 0 {
		return "", nil
	}
	b, err := cGoBytes(unsafe.Pointer(s), n)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func cGoBytes(ptr unsafe.Pointer, n C.uintptr_t) ([]byte, error) {
	// C.GoBytes accepts a C int length even on 64-bit Go builds.
	if uint64(n) > 1<<31-1 {
		return nil, &ConvertError{Kind: "resource_limit", Detail: fmt.Sprintf("FFI output is too large for Go: %d bytes", n)}
	}
	return C.GoBytes(ptr, C.int(n)), nil
}

// cString reads a NUL-terminated C string as a Go string.
func cString(s *C.char) string {
	if s == nil {
		return ""
	}
	return C.GoString(s)
}

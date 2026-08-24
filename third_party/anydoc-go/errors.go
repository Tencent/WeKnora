package anydoc

/*
#include <stdint.h>
#include "include/anydoc.h"
*/
import "C"

import "fmt"

// Error codes mirroring the C ABI (ERR_*). Stable across versions.
const (
	errOK            = C.ERR_OK
	errUnsupported   = C.ERR_UNSUPPORTED
	errMalformed     = C.ERR_MALFORMED
	errEncrypted     = C.ERR_ENCRYPTED
	errResourceLimit = C.ERR_RESOURCE_LIMIT
	errMissingPart   = C.ERR_MISSING_PART
	errIO            = C.ERR_IO
	errPDFNoModel    = C.ERR_PDF_NO_MODEL
	errInvalidArg    = C.ERR_INVALID_ARG
	errUnknownFormat = C.ERR_UNKNOWN_FORMAT
)

// ConvertError is the typed error every conversion function returns. It
// carries the same variant kind the Node and Python bindings expose
// ("unsupported", "malformed", "encrypted", ...) plus the crate's
// human-readable detail.
type ConvertError struct {
	// Kind is the lowercase variant name, matching the Node and Python
	// bindings: "unsupported", "malformed", "encrypted", "resource_limit",
	// "missing_part", "io", "pdf_no_model". Go also reports
	// "unknown_format" for an invalid explicit Format.
	Kind string
	// Detail is the crate's Display output for the error.
	Detail string
}

func (e *ConvertError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Detail == "" {
		return "anydoc: " + e.Kind
	}
	return "anydoc: " + e.Kind + ": " + e.Detail
}

// convertError builds a ConvertError from an ABI error code, pulling the
// detail message from the thread-local last-error slot.
func convertError(code C.int) error {
	if code == errOK {
		return nil
	}
	kind := errorKind(code)
	detail := lastError()
	return &ConvertError{Kind: kind, Detail: detail}
}

func errorKind(code C.int) string {
	switch code {
	case errUnsupported:
		return "unsupported"
	case errMalformed:
		return "malformed"
	case errEncrypted:
		return "encrypted"
	case errResourceLimit:
		return "resource_limit"
	case errMissingPart:
		return "missing_part"
	case errIO:
		return "io"
	case errPDFNoModel:
		return "pdf_no_model"
	case errInvalidArg:
		return "invalid_argument"
	case errUnknownFormat:
		return "unknown_format"
	default:
		return fmt.Sprintf("unknown_error(%d)", int(code))
	}
}

// lastError returns the human-readable message the last ABI call stashed on
// the current OS thread, or "" when there was none. The C side returns a
// freshly allocated string the caller must free.
func lastError() string {
	ptr := C.anydoc_last_error()
	if ptr == nil {
		return ""
	}
	defer C.anydoc_string_free(ptr)
	return cString(ptr)
}

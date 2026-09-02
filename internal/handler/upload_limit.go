package handler

import (
	stderrors "errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// uploadEnvelopeSlack is the multipart framing allowed on top of the file
// itself, so a body cap refuses a genuinely oversized upload without
// rejecting a legal one for its boundary lines.
const uploadEnvelopeSlack = 1 << 20

// limitUploadBody caps the request body before anything parses it.
//
// It has to run before FormFile rather than after: ParseMultipartForm buffers
// the whole request, spilling to temp files, so a handler that only checks the
// declared part size has already accepted every byte by the time it looks. The
// frontend nginx used to be the only thing standing here, and its
// client_max_body_size is now the larger skill-bundle cap, so every
// knowledge-sized upload has to state its own limit.
func limitUploadBody(c *gin.Context, maxBytes int64) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+uploadEnvelopeSlack)
}

// isRequestBodyTooLarge reports whether a multipart parsing error is the body
// cap firing rather than a malformed or absent part.
//
// The two need different answers. An oversized upload is a limit the caller
// can see and act on, while a missing field is a different request; a handler
// that treats the first as the second reports "no file was sent" for a file
// that was sent and rejected.
func isRequestBodyTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	return stderrors.As(err, &tooLarge)
}

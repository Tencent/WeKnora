package web_search

import (
	"errors"
	"fmt"
	"io"
)

// maxJSONResponseBytes bounds every provider response body. A search result set
// is JSON metadata over a page of hits; a larger body means a broken or
// misconfigured endpoint, not more results.
const maxJSONResponseBytes = 16 << 20

// maxErrorPreviewBytes bounds how much of a failed response is echoed into a log
// line or an error string. Without it a failing endpoint can inject an unbounded
// blob into both.
const maxErrorPreviewBytes = 4 << 10

// errResponseTooLarge marks a body over the cap. It is deterministic: the same
// request returns the same oversized body, so callers must not retry it.
var errResponseTooLarge = errors.New("response exceeds maximum size")

// readCapped reads a response body, refusing anything larger than limit instead
// of buffering it. Oversized payloads are reported as an error: a truncated body
// would otherwise be parsed as if it were the whole response. A non-positive
// limit (e.g. a provider built without one) falls back to maxJSONResponseBytes.
//
// Mirrors the cap the datasource connectors already apply to their API bodies.
func readCapped(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = maxJSONResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w (%d bytes)", errResponseTooLarge, limit)
	}
	return data, nil
}

// readErrorPreview reads just enough of a failed body to put in a log line or an
// error message. It truncates rather than refusing: a preview is diagnostic, and
// the interesting part of a large error body is its start. Read errors are
// dropped because the caller is already on the error path and is about to report
// the status code, which is the useful part.
func readErrorPreview(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, maxErrorPreviewBytes))
	return string(data)
}

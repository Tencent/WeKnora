package docparser

import (
	"errors"
	"fmt"
	"io"
)

// The parse APIs answer with small JSON documents - a job id, a batch status, a
// list of parser engines - and the clients read them with an unbounded
// io.ReadAll. The result package downloaded alongside them is capped (see
// zip_limits.go), so a response this size going unbounded is not a consistent
// reading of the same vendor.
//
// The web_search clients had exactly this gap and now share these names, so a
// reader can diff the two packages or grep for either.
const (
	// maxJSONResponseBytes bounds one parse-API response body.
	//
	// It is the same figure mineru_v1_client.go already applies to its JSON
	// calls, so the two MinerU clients agree: a document that parses through one
	// cannot be refused for size by the other. Job submissions and status polls
	// are a few hundred bytes; 16 MiB is far above anything they return.
	maxJSONResponseBytes = 16 << 20

	// maxErrorPreviewBytes bounds how much of a failed response is echoed into a
	// log line or an error string. Without it a failing endpoint can inject an
	// unbounded blob into both.
	maxErrorPreviewBytes = 4 << 10
)

// errResponseTooLarge marks a body over a cap above.
var errResponseTooLarge = errors.New("response exceeds maximum size")

// readCapped reads a response body, refusing anything larger than limit instead
// of buffering it. An oversized body is reported as an error rather than
// truncated, because a truncated document would be parsed as if it were the
// whole reply. A non-positive limit falls back to maxJSONResponseBytes.
//
// Mirrors the cap the datasource connectors apply to their API bodies.
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

// readErrorPreview reads just enough of a failed body to put in a log line or
// an error message. It truncates rather than refusing: the start of a large
// error body is the diagnostic part. Read errors are dropped because the caller
// is already on the error path and is about to report the status code.
func readErrorPreview(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, maxErrorPreviewBytes))
	return string(data)
}

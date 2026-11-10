package api

import (
	"errors"
	"fmt"
	"io"
)

// MaxResponseBytes bounds a single non-streaming provider response body.
//
// Streaming replies are assembled incrementally and are not covered by this. It
// has to clear every non-streaming payload the catalog can legitimately return:
// an embeddings batch is bounded by BATCH_EMBED_SIZE (default 5) times the
// model's dimensions, and a chat reply by the model's output-token ceiling, so
// both sit far below this even at their largest. It is also well above the 20 MiB
// the older models/chat layer applies to the same replies, so nothing that
// worked before can be refused by it.
const MaxResponseBytes = 64 << 20

// ErrResponseTooLarge marks a body over the cap. It is deterministic: the same
// request returns the same oversized body, so a retry cannot help.
var ErrResponseTooLarge = errors.New("response exceeds maximum size")

// ReadResponseCapped reads a non-streaming response body, refusing anything
// larger than limit instead of buffering it. Oversized payloads are reported as
// an error rather than truncated, because a truncated body would otherwise be
// decoded as if it were the whole reply - producing a short answer or a
// plausible-looking partial embedding instead of an error. A non-positive limit
// falls back to MaxResponseBytes.
//
// Endpoint.Do already bounds the error body; this is the same guard for the
// success body, which until now was read whole.
func ReadResponseCapped(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = MaxResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w (%d bytes)", ErrResponseTooLarge, limit)
	}
	return data, nil
}

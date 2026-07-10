package limiter

import (
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

// Token estimation for the TPM (tokens-per-minute) budget. The estimate is
// deliberately approximate: it is used only to size a rate reservation BEFORE
// the provider round-trip, and for chat the reservation is reconciled against
// the API's authoritative Usage afterwards (see Reservation.Release). The
// cl100k_base encoding is a good-enough proxy across model families — over- or
// under-estimating by a small margin only shifts throttling timing slightly.
//
// This lives in the limiter package (rather than reusing internal/agent/token)
// so the chat/embedding/vlm client wrappers can depend on it without importing
// the agent layer — internal/agent/token imports internal/models/chat, so a
// chat-side dependency on it would be circular.

var (
	estCodecOnce sync.Once
	estCodec     tokenizer.Codec
)

func codec() tokenizer.Codec {
	estCodecOnce.Do(func() {
		// Error is ignored: on failure estCodec stays nil and EstimateTokens
		// falls back to a char-based heuristic. A missing tokenizer must never
		// break model traffic.
		estCodec, _ = tokenizer.Get(tokenizer.Cl100kBase)
	})
	return estCodec
}

// EstimateTokens returns an approximate token count for s. Falls back to a
// ~4-chars-per-token heuristic if the tokenizer is unavailable or errors.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	if c := codec(); c != nil {
		if ids, _, err := c.Encode(s); err == nil {
			return len(ids)
		}
	}
	return (len(s) + 3) / 4
}

// Package token provides token estimation for LLM context window management.
package token

import (
	"unicode"

	"github.com/Tencent/WeKnora/internal/models/chat"
)

const (
	// DefaultCharsPerToken is a blended ratio for mixed English/CJK text.
	// English ≈ 4 chars/token, CJK ≈ 1.5 chars/token, blended ≈ 3.5.
	DefaultCharsPerToken = 3.5

	// perMessageOverhead is the estimated token overhead per message
	// (role label, formatting, separator tokens).
	perMessageOverhead = 4
)

// Estimator estimates token counts for messages and strings.
// Uses a character-based heuristic that accounts for CJK characters.
type Estimator struct {
	charsPerToken float64
}

// NewEstimator creates a token estimator.
// If charsPerToken <= 0, DefaultCharsPerToken is used.
func NewEstimator(charsPerToken float64) *Estimator {
	if charsPerToken <= 0 {
		charsPerToken = DefaultCharsPerToken
	}
	return &Estimator{charsPerToken: charsPerToken}
}

// EstimateString estimates the token count for a string.
// It uses a CJK-aware heuristic: CJK characters are counted at ~1.5 chars/token,
// while ASCII/Latin characters use the configured charsPerToken ratio.
func (e *Estimator) EstimateString(s string) int {
	if len(s) == 0 {
		return 0
	}

	var cjkRunes, otherRunes int
	for _, r := range s {
		if isCJK(r) {
			cjkRunes++
		} else {
			otherRunes++
		}
	}

	// CJK characters are roughly 1.5 chars per token
	cjkTokens := float64(cjkRunes) / 1.5
	otherTokens := float64(otherRunes) / e.charsPerToken

	return int(cjkTokens+otherTokens) + 1 // +1 to avoid underestimation
}

// EstimateMessages estimates the total token count for a slice of chat messages.
func (e *Estimator) EstimateMessages(messages []chat.Message) int {
	total := 0
	for i := range messages {
		total += e.EstimateMessage(&messages[i])
	}
	return total
}

// EstimateMessage estimates the token count for a single message.
func (e *Estimator) EstimateMessage(msg *chat.Message) int {
	tokens := perMessageOverhead
	tokens += e.EstimateString(msg.Role)
	tokens += e.EstimateString(msg.Content)
	tokens += e.EstimateString(msg.Name)

	// Tool calls in assistant messages
	for _, tc := range msg.ToolCalls {
		tokens += e.EstimateString(tc.Function.Name)
		tokens += e.EstimateString(tc.Function.Arguments)
		tokens += 4 // overhead for tool call structure
	}

	return tokens
}

// isCJK checks if a rune is a CJK (Chinese, Japanese, Korean) character.
func isCJK(r rune) bool {
	// CJK Unified Ideographs
	if r >= 0x4E00 && r <= 0x9FFF {
		return true
	}
	// CJK Unified Ideographs Extension A
	if r >= 0x3400 && r <= 0x4DBF {
		return true
	}
	// CJK Unified Ideographs Extension B
	if r >= 0x20000 && r <= 0x2A6DF {
		return true
	}
	// CJK Compatibility Ideographs
	if r >= 0xF900 && r <= 0xFAFF {
		return true
	}
	// Hiragana + Katakana
	if r >= 0x3040 && r <= 0x30FF {
		return true
	}
	// Hangul Syllables
	if r >= 0xAC00 && r <= 0xD7AF {
		return true
	}
	// Full-width characters
	if unicode.Is(unicode.Han, r) {
		return true
	}
	return false
}

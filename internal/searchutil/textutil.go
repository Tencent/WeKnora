package searchutil

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/types"
)

// BuildContentSignature creates a normalized MD5 signature for content to detect duplicates.
// It normalizes the content by lowercasing, trimming whitespace, and collapsing multiple spaces.
func BuildContentSignature(content string) string {
	c := strings.ToLower(strings.TrimSpace(content))
	if c == "" {
		return ""
	}
	// Normalize whitespace
	c = strings.Join(strings.Fields(c), " ")
	// Use MD5 hash of full content
	hash := md5.Sum([]byte(c))
	return hex.EncodeToString(hash[:])
}

// containsChinese checks whether text contains any CJK unified ideographs.
func containsChinese(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// TokenizeSimple tokenizes text into a set of unique tokens.
// For text containing Chinese characters, it uses jieba segmentation for accurate word boundaries.
// For pure non-Chinese text, it falls back to whitespace-based splitting.
// Returns a map where keys are lowercase tokens with rune length > 1.
func TokenizeSimple(text string) map[string]struct{} {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}

	var words []string
	if containsChinese(text) {
		// Use jieba for Chinese text segmentation (search mode for finer granularity)
		words = types.GetJieba().CutForSearch(text, true)
	} else {
		words = strings.Fields(text)
	}

	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		w = strings.TrimSpace(w)
		// Filter out single-rune tokens and pure punctuation/whitespace
		if len([]rune(w)) > 1 && !isAllPunct(w) {
			set[w] = struct{}{}
		}
	}
	return set
}

// isAllPunct checks if a string consists entirely of punctuation or whitespace.
func isAllPunct(s string) bool {
	for _, r := range s {
		if !unicode.IsPunct(r) && !unicode.IsSpace(r) && !unicode.IsSymbol(r) {
			return false
		}
	}
	return true
}

// Jaccard calculates Jaccard similarity between two token sets.
// Returns a value between 0 and 1, where 1 means identical sets.
func Jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	// small set drives large set
	if len(a) > len(b) {
		return Jaccard(b, a)
	}

	// Calculate intersection
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}

	// Calculate union
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}

	return float64(inter) / float64(union)
}

// NormalizeContent returns a lowercased, whitespace-collapsed version of s
// suitable for containment and overlap checks.
func NormalizeContent(s string) string {
	c := strings.ToLower(strings.TrimSpace(s))
	if c == "" {
		return ""
	}
	return strings.Join(strings.Fields(c), " ")
}

// IsContentContained reports whether the normalized form of short is a
// substring of the normalized form of long. Both inputs must already be
// normalized via NormalizeContent.
func IsContentContained(normalizedShort, normalizedLong string) bool {
	if normalizedShort == "" || normalizedLong == "" {
		return false
	}
	if len(normalizedShort) > len(normalizedLong) {
		return false
	}
	return strings.Contains(normalizedLong, normalizedShort)
}

// ContentOverlapRatio estimates how much of a's content overlaps with b by
// comparing their token sets (Jaccard-like but using overlap coefficient:
// |intersection| / |smaller set|). Both inputs should be raw content strings.
// Returns a value in [0, 1] where 1 means the smaller set is fully contained
// in the larger set.
func ContentOverlapRatio(a, b string) float64 {
	tokA := TokenizeSimple(a)
	tokB := TokenizeSimple(b)
	if len(tokA) == 0 || len(tokB) == 0 {
		return 0
	}

	small, large := tokA, tokB
	if len(tokA) > len(tokB) {
		small, large = tokB, tokA
	}

	inter := 0
	for k := range small {
		if _, ok := large[k]; ok {
			inter++
		}
	}
	return float64(inter) / float64(len(small))
}

// ClampFloat clamps a float value to the specified range [minV, maxV].
func ClampFloat(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

package searchutil

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
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

// TokenizeSimple tokenizes text into a set of words (simple whitespace-based).
// Returns a map where keys are lowercase tokens with length > 1.
func TokenizeSimple(text string) map[string]struct{} {
	text = strings.ToLower(text)
	fields := strings.Fields(text)
	set := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		if len(f) > 1 {
			set[f] = struct{}{}
		}
	}
	return set
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

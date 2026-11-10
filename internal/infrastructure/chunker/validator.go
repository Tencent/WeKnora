// Package chunker - validator.go inspects a tier's output and decides whether
// it is good enough to ship or whether the strategy chain should fall through
// to the next tier. The validator is intentionally permissive: a single
// "obviously broken" output is rejected, but plausible-looking variation is
// accepted so we don't oscillate between tiers.
package chunker

import "math"

// ValidationResult captures the verdict and reason for a chunk-set.
type ValidationResult struct {
	OK     bool
	Reason string
}

// ValidateChunks checks whether the given chunks form a usable result for a
// document of totalChars characters with a target chunkSize. Returns OK=true
// when no broken-output indicator triggers.
func ValidateChunks(chunks []Chunk, totalChars, chunkSize int) ValidationResult {
	if len(chunks) == 0 {
		return ValidationResult{Reason: "no chunks produced"}
	}

	// A single chunk for a document much larger than chunkSize means the
	// strategy did not actually split — fail so the next tier runs.
	if len(chunks) == 1 && totalChars > 2*chunkSize {
		return ValidationResult{Reason: "single chunk for large document"}
	}

	// Compute size statistics.
	var sum, sumSq float64
	maxLen, minLen := 0, math.MaxInt32
	for _, c := range chunks {
		l := len([]rune(c.Content))
		sum += float64(l)
		sumSq += float64(l * l)
		if l > maxLen {
			maxLen = l
		}
		if l < minLen {
			minLen = l
		}
	}
	avg := sum / float64(len(chunks))

	// All but the last chunk should carry meaningful content. We allow the
	// last chunk to be tiny because tail residue is normal.
	tinyCount := 0
	for i, c := range chunks {
		if i == len(chunks)-1 {
			continue
		}
		if len([]rune(c.Content)) < 50 {
			tinyCount++
		}
	}
	if tinyCount > len(chunks)/4 && tinyCount > 2 {
		return ValidationResult{Reason: "too many tiny chunks"}
	}

	// Reject when no chunk reached at least 25% of the target — the splitter
	// is fragmenting too aggressively to be useful.
	if maxLen < chunkSize/4 && totalChars > chunkSize {
		return ValidationResult{Reason: "all chunks far below target size"}
	}

	// Sanity check on absolute upper bound. Anything past 2x chunkSize is a
	// red flag — the splitter ignored its size budget.
	if maxLen > 2*chunkSize && chunkSize > 0 {
		return ValidationResult{Reason: "chunk exceeds 2x target size"}
	}

	_ = avg
	return ValidationResult{OK: true}
}

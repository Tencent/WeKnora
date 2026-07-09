package types

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"

	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// ContentNormalizationVersion is the version tag of the NormalizeContent
// function. It is part of every cache key derived from a normalized text so
// that changes to normalization invalidate caches exactly. See
// reparse-content-cache / stable-chunk-id specs.
//
// Bump this when NormalizeContent behavior changes.
const ContentNormalizationVersion = "n1"

// nbws is the set of characters that should collapse to a plain ASCII space
// during normalization. It covers the Unicode whitespace most commonly found
// in copy-pasted text (incl. the U+00A0 NBSP that NFKC maps to space but
// width.Fold re-expands when run first), while leaving meaningful whitespace
// (single ASCII space) intact.
var nbwsReplacer = strings.NewReplacer(
	"\r\n", "\n",
	"\r", "\n",
	"\u00A0", " ",
	"\u2002", " ",
	"\u2003", " ",
	"\u2007", " ",
	"\u200B", "",
	"\uFEFF", "",
)

// NormalizeContent canonicalizes chunk text for content-addressed hashing:
// the chunk-stable-ID derivation and the embedding cache key MUST derive from
// the same normalized form so identical content yields identical IDs/keys.
//
// Pipeline:
//  1. Width-fold (full-width ASCII → ASCII, NBSP variants folded later).
//  2. NFKC Unicode normalization (compatibility decomposition).
//  3. NBSP / zero-width / BOM strip.
//  4. Per-line trim of leading/trailing whitespace.
//  5. Collapse internal runs of spaces/tabs on each line to a single space.
//  6. Collapse 3+ newlines → 2 (paragraph) and trim leading/trailing newlines.
//
// The function is a pure string→string transform. It does NOT touch the
// original Chunk.Content (which must preserve the rune-offset invariant), only
// the *derived* ID/cache key input.
func NormalizeContent(s string) string {
	if s == "" {
		return ""
	}
	// Fold full/half-width first; width.Fold is a no-op for already-ASCII text.
	s = width.Fold.String(s)
	// NFKC compatibility decomposition.
	s = norm.NFKC.String(s)
	// Strip NBSP / zero-width / BOM and normalize CR.
	s = nbwsReplacer.Replace(s)

	// Per-line trim + collapse internal space runs.
	lines := strings.Split(s, "\n")
	var out []string
	for _, ln := range lines {
		// Collapse runs of spaces and tabs (NOT newlines; we're per-line) into
		// a single space, then trim the line ends. This makes incidental
		// indentation/alignment noise irrelevant to the hash.
		ln = collapseSpaces(ln)
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	// Join with a single newline; collapse 3+ blank lines was implicit because
	// we dropped all-empty lines above.
	joined := strings.Join(out, "\n")
	return strings.TrimSpace(joined)
}

// collapseSpaces turns any maximal run of spaces/tabs within a line into a
// single space. It is allocation-free for the common case (no run > 1).
func collapseSpaces(ln string) string {
	if !strings.ContainsAny(ln, " \t") {
		return ln
	}
	var b strings.Builder
	b.Grow(len(ln))
	inRun := false
	for _, r := range ln {
		if r == ' ' || r == '\t' {
			inRun = true
			continue
		}
		if inRun {
			b.WriteByte(' ')
			inRun = false
		}
		b.WriteRune(r)
	}
	if inRun {
		b.WriteByte(' ')
	}
	return b.String()
}

// SHAChecksum returns the lowercase hex SHA-256 of the input.
func SHAChecksum(data string) string {
	h := sha256.Sum256([]byte(data))
	const hex = "0123456789abcdef"
	out := make([]byte, 64)
	for i, v := range h {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0F]
	}
	return string(out)
}

// StableContentHash returns the SHA-256 of NormalizeContent(s), prefixed with
// the ContentNormalizationVersion so a normalization bump (manual deployment)
// produces a distinct hash and naturally invalidates any keyed cache.
func StableContentHash(s string) string {
	return ContentNormalizationVersion + ":" + SHAChecksum(NormalizeContent(s))
}

// StableChunkID derives a content-addressed chunk ID from (knowledgeID,
// normalized content, stableSeq). Two calls with the same tuple MUST yield
// the same ID; the output is a 36-char UUID-shaped string (hex with dashes at
// the canonical 8-4-4-4-12 offsets) so it remains a valid varchar(36) primary
// key and needs no schema change relative to legacy UUID IDs.
//
// stableSeq disambiguates chunks within a single knowledge that would
// otherwise collide (identical normalized content at different positions).
// The common case (distinct content) passes stableSeq=0; the caller only
// increments stableSeq when a collision is detected within the document.
//
// knowledgeID is part of the input so identical chunk content in two
// different knowledge documents produces DIFFERENT IDs (we deliberately do
// NOT collapse cross-document chunk identity — only *embeddings* dedup
// cross-doc).
func StableChunkID(knowledgeID, content string, stableSeq int) string {
	// Combine the three inputs in a way resistant to ambiguity (length
	// prefixes) before hashing. A naïgue `knowledgeID|content|seq` would
	// collide for ("ab|","c",0) and ("a","b|c",0).
	h := sha256.New()
	h.Write([]byte(knowledgeID))
	h.Write([]byte{0})
	h.Write([]byte(StableContentHash(content)))
	h.Write([]byte{0})
	var sb [8]byte
	binary.BigEndian.PutUint64(sb[:], uint64(stableSeq))
	h.Write(sb[:])
	sum := h.Sum(nil)
	// Apply UUID variant (top 2 bits of byte index 8 → 10xxxxxx) and
	// version (high nibble of byte index 6 → 5, SHA namespace) to the
	// raw bytes BEFORE hex encoding so the bits land in the right place.
	sum[8] = (sum[8] & 0x3F) | 0x80
	sum[6] = (sum[6] & 0x0F) | 0x50

	// Render as a UUID string: hex groups 8-4-4-4-12 separated by dashes.
	const hex = "0123456789abcdef"
	hb := make([]byte, 32)
	for i, v := range sum[:16] {
		hb[i*2] = hex[v>>4]
		hb[i*2+1] = hex[v&0x0F]
	}
	// 8-4-4-4-12 hex groups from hb with dashes between them.
	b := make([]byte, 36)
	copy(b[0:8], hb[0:8])
	b[8] = '-'
	copy(b[9:13], hb[8:12])
	b[13] = '-'
	copy(b[14:18], hb[12:16])
	b[18] = '-'
	copy(b[19:23], hb[16:20])
	b[23] = '-'
	copy(b[24:36], hb[20:32])
	return string(b)
}

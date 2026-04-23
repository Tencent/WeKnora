// Package chunker - markdown_splitter.go implements structure-aware chunking
// for Markdown documents. Unlike the default recursive splitter (splitter.go),
// this one treats ATX headings (#, ##, ###) as hard chunk boundaries and
// produces chunks that align with the author's sectioning.
//
// Each emitted chunk carries a heading path (breadcrumb) so retrieval can
// reason about "where in the document this snippet comes from". For medical
// RAG this is the single most impactful chunking improvement: a short line
// like "本品不得与含钙静脉输液同管路给药" becomes retrievable under the
// query "头孢曲松 禁忌" only if the chunk knows it sits under that heading.
//
// Oversized sections (larger than SoftMaxChars) fall back to the recursive
// splitter for sub-division, and each sub-chunk inherits its parent section's
// heading path. Tables, code fences, lists, and formulas are preserved atomic
// via the existing protected-pattern machinery.
package chunker

import (
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// MarkdownConfig controls the behaviour of SplitMarkdown. Sensible defaults
// are applied by DefaultMarkdownConfig for unset zero values.
type MarkdownConfig struct {
	// MaxHeadingDepth limits which heading levels form section boundaries.
	// Headings deeper than this are kept as content. Default 3 (# ## ###).
	MaxHeadingDepth int

	// SoftMaxChars is the character soft cap per section. Sections larger
	// than this get sub-split by the recursive splitter. Default 4000.
	SoftMaxChars int

	// ChildChunkSize is the target size for sub-chunks when a section
	// exceeds SoftMaxChars. Default 1500.
	ChildChunkSize int

	// ChildChunkOverlap is the overlap applied to sub-chunks. Default 100.
	ChildChunkOverlap int

	// Separators used by the recursive sub-splitter. Default covers common
	// Chinese and English sentence terminators.
	Separators []string

	// InjectBreadcrumbs prepends a "> a > b > c\n\n" breadcrumb line to each
	// emitted chunk's content. Strongly recommended (default true) for
	// medical/academic corpora where section context is load-bearing.
	InjectBreadcrumbs bool

	// KeepListIntact marks sections dominated by list items as atomic even
	// when they exceed SoftMaxChars (unless they exceed HardMaxChars, in
	// which case we fall back to recursive splitting to avoid blowing up
	// downstream embedding limits).
	KeepListIntact bool

	// HardMaxChars is an absolute ceiling. Any chunk larger than this is
	// always sub-split, regardless of KeepListIntact. Default 7500.
	HardMaxChars int
}

// DefaultMarkdownConfig returns a config tuned for MinerU-produced markdown
// and medical literature. Callers should override SoftMaxChars / child sizes
// per knowledge base as needed.
func DefaultMarkdownConfig() MarkdownConfig {
	return MarkdownConfig{
		MaxHeadingDepth:   3,
		SoftMaxChars:      4000,
		ChildChunkSize:    1500,
		ChildChunkOverlap: 100,
		Separators:        []string{"\n\n", "\n", "。", "；", "！", "？"},
		InjectBreadcrumbs: true,
		KeepListIntact:    true,
		HardMaxChars:      7500,
	}
}

func (c *MarkdownConfig) applyDefaults() {
	if c.MaxHeadingDepth <= 0 {
		c.MaxHeadingDepth = 3
	}
	if c.MaxHeadingDepth > 6 {
		c.MaxHeadingDepth = 6
	}
	if c.SoftMaxChars <= 0 {
		c.SoftMaxChars = 4000
	}
	if c.HardMaxChars <= 0 {
		c.HardMaxChars = 7500
	}
	if c.HardMaxChars < c.SoftMaxChars {
		c.HardMaxChars = c.SoftMaxChars * 2
	}
	if c.ChildChunkSize <= 0 {
		c.ChildChunkSize = 1500
	}
	if c.ChildChunkOverlap < 0 {
		c.ChildChunkOverlap = 0
	}
	if len(c.Separators) == 0 {
		c.Separators = []string{"\n\n", "\n", "。", "；", "！", "？"}
	}
}

// MarkdownChunk is the result of splitting a Markdown document. It embeds
// the existing Chunk (Content/Seq/Start/End) and layers on structural hints
// that downstream consumers persist into chunks.metadata.
type MarkdownChunk struct {
	Chunk

	// HeadingPath is the breadcrumb trail from document root to this chunk's
	// section, e.g. ["头孢曲松说明书", "三、禁忌", "（一）与钙剂合用"].
	// Empty slice means the chunk is in the document preamble (before any heading).
	HeadingPath []string

	// HeadingLevel is the deepest heading level active for this chunk
	// (e.g. 3 for "### xxx"). 0 means no active heading.
	HeadingLevel int

	// SectionCode is a best-effort parse of the numeric prefix of the
	// innermost heading title (e.g. "3.2.1", "一", "（一）"). Empty when
	// no structured numbering is detected.
	SectionCode string

	// Category tags the dominant content kind of the chunk.
	Category string
}

// ToMetadata produces the loose map that the chunker hands to
// processChunks via ParsedChunk.Metadata. Keys match types.MetaKey*.
func (m MarkdownChunk) ToMetadata() map[string]any {
	if len(m.HeadingPath) == 0 && m.HeadingLevel == 0 && m.SectionCode == "" && m.Category == "" {
		return nil
	}
	md := make(map[string]any, 4)
	if len(m.HeadingPath) > 0 {
		md[types.MetaKeyHeadingPath] = append([]string(nil), m.HeadingPath...)
	}
	if m.HeadingLevel > 0 {
		md[types.MetaKeyHeadingLevel] = m.HeadingLevel
	}
	if m.SectionCode != "" {
		md[types.MetaKeySectionCode] = m.SectionCode
	}
	if m.Category != "" {
		md[types.MetaKeyCategory] = m.Category
	}
	return md
}

// ---------------------------------------------------------------------------
// Heading detection
// ---------------------------------------------------------------------------

// atxHeadingPattern matches a single ATX heading line.
// Groups: 1 = hashes, 2 = title text (trailing hashes stripped).
var atxHeadingPattern = regexp.MustCompile(`^([#]{1,6})\s+(.+?)\s*#*\s*$`)

// inlineAtxHeadingPattern finds ATX headings that got embedded inside a
// paragraph stream, which happens reliably with MinerU output on two-column
// medical PDFs: layout engines merge columns into one long paragraph and
// only emit the `##` marker mid-stream. We re-insert hard newlines around
// such markers so the regular parseHeadings pass sees them as real headings.
//
// Matches: a hash run preceded by any non-newline char and followed by
// whitespace + heading content.
//
// Two-step form:
//  1. non-newline char immediately before "##" → inject "\n" before it
//  2. heading line followed immediately by non-newline content → inject "\n"
//
// Both steps are conservative: hashes inside fenced code blocks are ignored
// because the fence detector in parseHeadings() runs AFTER normalisation and
// already rejects them, but to be extra safe we skip fences here too.
var (
	inlineHeadingBeforePattern = regexp.MustCompile(`([^\n])[ \t]+(#{1,6}[ \t]+)`)
	inlineHeadingAfterPattern  = regexp.MustCompile(`(?m)^(#{1,6}[ \t]+[^\n]*?)[ \t]+([^\s#][^\n]*)$`)
)

// subsectionMarkerPattern matches medical-paper numbered subsections (1.1,
// 2.3, 3.2.1, ...) that appear mid-paragraph, i.e. immediately after a
// sentence terminator or a closing bracket. MinerU pipeline backend reliably
// drops these as inline text even though they are real H3 headings in the
// source PDF.
//
// RE2 has no lookahead, so we capture the follower character (Chinese
// ideograph or ASCII letter) as group 3 and put it back in the replacement.
// Constraints on the follower ensure numeric ranges like "(10-15%)" and
// Figure references like "Fig.3B" are not matched.
var subsectionMarkerPattern = regexp.MustCompile(
	`([。；？！:：)）\]])\s*(\d{1,2}\.\d{1,3}(?:\.\d{1,3})?)([\p{Han}A-Za-z])`,
)

// breakSubsectionParagraphs inserts a blank line before inline "数字.数字"
// subsection markers so the recursive sub-splitter (invoked for oversize
// sections) breaks at these boundaries rather than mid-sentence.
func breakSubsectionParagraphs(text string) string {
	if !strings.Contains(text, ".") {
		return text
	}
	return subsectionMarkerPattern.ReplaceAllString(text, "$1\n\n$2$3")
}

// lineStartSubsectionPattern matches medical-paper subsection lines when
// they already stand alone (after MinerU VLM clean newline separation) but
// lack an ATX heading marker:
//
//	"1.1 实验动物和细胞系 8周龄雄性DBA/2J小鼠..."
//
// Captures:
//
//	1 — section code (1.1 / 2.3 / 1.4.2 ...)
//	2 — title: 2–12 Han chars (ending at the first non-Han char, which is
//	    usually a space followed by a digit/Latin/punct that kicks off body)
//	3 — body: the remainder of the line starting from the next non-space
//
// We intentionally restrict title to Han chars so that lines like
// "1.5 BUN 和 Scr 检测 小鼠麻醉后" (title starting with Latin) aren't
// mis-carved; those are handled conservatively by the paragraph-break pass.
var lineStartSubsectionPattern = regexp.MustCompile(
	`(?m)^(\d{1,2}(?:\.\d{1,3}){1,2})[ \t]+([\p{Han}]{2,12})[ \t]+([^\s\n][^\n]*)$`,
)

// promoteMineruSubsectionHeadings promotes line-start "1.1 实验动物 正文..."
// patterns into proper H3 ATX headings so heading_path breadcrumbs can
// distinguish subsections inside an otherwise-monolithic "## 1 材料与方法"
// section. Idempotent on well-formed markdown (already-marked H3 lines
// start with "###" and don't match the pattern).
func promoteMineruSubsectionHeadings(text string) string {
	if !strings.Contains(text, ".") {
		return text
	}
	return lineStartSubsectionPattern.ReplaceAllString(
		text, "### $1 $2\n\n$3",
	)
}

// orphanNumberHeadingPattern detects MinerU VLM's two-column layout artefact
// where the heading row and its title row get parsed as separate blocks:
//
//	# 1
//
//	材料与方法
//
// We only target level-1 because a bare-number heading is almost certainly a
// section marker (1, 2, 3...) that the layout parser mis-levelled — real
// paper titles are never just "1". Captures:
//
//	1 — section code (bare digit group like "1" or "2.3")
//	2 — orphan title row (Han chars)
var orphanNumberHeadingPattern = regexp.MustCompile(
	`(?m)^#[ \t]+(\d+(?:\.\d+){0,2})[ \t]*\n\s*\n([\p{Han}]{2,20})\s*(?:\n|$)`,
)

// mergeOrphanNumberHeadings fixes the "heading contains only a section
// number" pattern AND simultaneously demotes the merged heading from H1 to
// H2, because section markers like "1 材料与方法" / "2 结果" are logically
// H2 siblings of the paper's real H1 title.
func mergeOrphanNumberHeadings(text string) string {
	if !strings.Contains(text, "#") {
		return text
	}
	return orphanNumberHeadingPattern.ReplaceAllString(text, "## $1 $2\n\n")
}

// normalizeInlineHeadings rewrites MinerU-style inline "## 标题 正文" streams
// into well-formed Markdown where each heading occupies its own line:
//
//	"正文 ## 标题 正文后续"  →  "正文\n\n## 标题\n\n正文后续"
//
// We keep the operation idempotent: well-formed markdown passes through
// unchanged.
func normalizeInlineHeadings(text string) string {
	// Fast path: nothing to normalise when no #'s exist at all.
	if !strings.Contains(text, "#") {
		return text
	}

	// Mask fenced code blocks so we do not touch hashes inside them.
	type fenceSpan struct{ start, end int }
	var fences []fenceSpan
	lines := splitLinesKeepOffsets(text)
	offset := 0
	inFence := false
	fenceStart := 0
	for _, l := range lines {
		lineStart := offset
		offset += len(l.raw)
		trimmed := strings.TrimRight(l.raw, "\r\n")
		if fenceOpenPattern.MatchString(trimmed) {
			if inFence {
				fences = append(fences, fenceSpan{fenceStart, offset})
				inFence = false
			} else {
				inFence = true
				fenceStart = lineStart
			}
		}
	}
	inRange := func(i int) bool {
		for _, f := range fences {
			if i >= f.start && i < f.end {
				return true
			}
		}
		return false
	}

	// Step 1: push a newline BEFORE any "##" that follows non-whitespace.
	// We do this by scanning submatch indices so we can filter by fence range.
	rebuild := func(src string, pat *regexp.Regexp, insert string) string {
		matches := pat.FindAllStringSubmatchIndex(src, -1)
		if len(matches) == 0 {
			return src
		}
		var b strings.Builder
		b.Grow(len(src) + len(matches)*2)
		cursor := 0
		for _, m := range matches {
			if inRange(m[0]) {
				continue
			}
			// Preserve group 1 as-is, then the injected separator, then group 2.
			b.WriteString(src[cursor:m[3]])
			b.WriteString(insert)
			b.WriteString(src[m[4]:m[1]])
			cursor = m[1]
		}
		b.WriteString(src[cursor:])
		return b.String()
	}

	out := rebuild(text, inlineHeadingBeforePattern, "\n\n")
	out = rebuild(out, inlineHeadingAfterPattern, "\n\n")
	return out
}

// fenceOpenPattern matches the start of a fenced code block.
// Supports ``` and ~~~ fences with optional language spec.
var fenceOpenPattern = regexp.MustCompile("^(\\s{0,3})(```+|~~~+)[^\\n]*$")

// sectionCodePatterns extracts a leading numeric/ordinal marker from a
// heading title. Tried in order; first match wins.
var sectionCodePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\s*(\d+(?:\.\d+){0,4})[\s、．.\-)]`),                  // "3.2.1 xxx"
	regexp.MustCompile(`^\s*第?([一二三四五六七八九十百零百千两]+)[章节篇、．.\s]`), // "第三章" / "三、"
	regexp.MustCompile(`^\s*[（(]([一二三四五六七八九十百零]+)[)）]\s*`),            // "（一）"
	regexp.MustCompile(`^\s*[（(](\d+)[)）]\s*`),                                   // "(1)"
}

// extractSectionCode returns the leading ordinal/number of a heading title,
// or "" when the title has no structured prefix.
func extractSectionCode(title string) string {
	for _, pat := range sectionCodePatterns {
		if m := pat.FindStringSubmatch(title); m != nil {
			return m[1]
		}
	}
	return ""
}

// headingLine represents one ATX heading found in the raw markdown.
type headingLine struct {
	level       int
	title       string
	sectionCode string
	// lineStart / lineEnd are byte offsets of this heading line in the raw text.
	lineStart int
	lineEnd   int // exclusive; includes the trailing newline
}

// parseHeadings scans the raw markdown and returns all ATX headings in
// document order. Headings inside fenced code blocks are ignored.
//
// Only headings with level <= maxDepth are treated as section boundaries;
// deeper headings are left in content. Setext headings ("Title\n====")
// are not supported in v1 because MinerU rarely emits them.
func parseHeadings(text string, maxDepth int) []headingLine {
	var headings []headingLine
	inFence := false
	offset := 0
	for _, line := range splitLinesKeepOffsets(text) {
		lineStart := offset
		offset += len(line.raw)
		trimmed := strings.TrimRight(line.raw, "\r\n")

		// Fence handling: ```/~~~ toggles the inFence flag. We don't care
		// about language specs or matching fence lengths precisely here;
		// the goal is only to avoid treating "# foo" inside code as heading.
		if fenceOpenPattern.MatchString(trimmed) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		m := atxHeadingPattern.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		level := len(m[1])
		if level > maxDepth {
			continue
		}
		title := strings.TrimSpace(m[2])
		headings = append(headings, headingLine{
			level:       level,
			title:       title,
			sectionCode: extractSectionCode(title),
			lineStart:   lineStart,
			lineEnd:     offset,
		})
	}
	return headings
}

// rawLine keeps the raw line bytes (including trailing newline) plus its
// starting offset in the source text.
type rawLine struct {
	raw string
}

// splitLinesKeepOffsets yields every line of text with its trailing newline
// preserved, so the caller can sum len(raw) to walk the byte offset cursor.
// Unlike strings.Split / bufio.Scanner, the final line without a trailing
// newline still produces an entry.
func splitLinesKeepOffsets(text string) []rawLine {
	if text == "" {
		return nil
	}
	var out []rawLine
	i := 0
	for i < len(text) {
		j := strings.IndexByte(text[i:], '\n')
		if j < 0 {
			out = append(out, rawLine{raw: text[i:]})
			break
		}
		out = append(out, rawLine{raw: text[i : i+j+1]})
		i += j + 1
	}
	return out
}

// ---------------------------------------------------------------------------
// Section walk
// ---------------------------------------------------------------------------

// section is a contiguous slice of the document between two headings (or
// between the document start and the first heading). It carries the
// full breadcrumb path that was active when the content was written.
type section struct {
	headingPath []string // excludes preamble
	level       int      // level of the innermost heading (0 for preamble)
	sectionCode string   // from the innermost heading
	start       int      // byte offset where the section's content starts
	end         int      // byte offset where the section's content ends (exclusive)
}

// buildSections walks headings and produces the linear list of sections.
// Each section's content = the bytes between its own heading line end and
// the next heading line start (or document end). Preamble (text before the
// first heading) is emitted as a section with empty headingPath.
func buildSections(text string, headings []headingLine) []section {
	var sections []section

	// Preamble, if any.
	preambleEnd := len(text)
	if len(headings) > 0 {
		preambleEnd = headings[0].lineStart
	}
	if strings.TrimSpace(text[:preambleEnd]) != "" {
		sections = append(sections, section{
			start: 0,
			end:   preambleEnd,
		})
	}

	if len(headings) == 0 {
		return sections
	}

	// Heading stack: index = level-1, value = title of the active heading
	// at that depth. Levels deeper than current are reset when we drop
	// back to a shallower one.
	maxDepth := 6
	stack := make([]string, maxDepth)

	for i, h := range headings {
		// Reset deeper levels when the current heading is shallower-or-equal.
		for l := h.level; l <= maxDepth; l++ {
			stack[l-1] = ""
		}
		stack[h.level-1] = h.title

		// Build breadcrumb from the stack, skipping empty slots.
		path := make([]string, 0, h.level)
		for _, t := range stack {
			if t == "" {
				continue
			}
			path = append(path, t)
		}

		// Content spans from the end of this heading line to the start of the
		// next heading line (or document end).
		start := h.lineEnd
		end := len(text)
		if i+1 < len(headings) {
			end = headings[i+1].lineStart
		}

		sections = append(sections, section{
			headingPath: path,
			level:       h.level,
			sectionCode: h.sectionCode,
			start:       start,
			end:         end,
		})
	}
	return sections
}

// ---------------------------------------------------------------------------
// Content classification
// ---------------------------------------------------------------------------

var (
	// mdTablePattern detects a Markdown table by its separator row.
	mdTablePattern = regexp.MustCompile(`(?m)^\s*\|?\s*:?-{3,}:?(?:\s*\|\s*:?-{3,}:?)+\s*\|?\s*$`)
	// mdListItemPattern matches an unordered or ordered list item line.
	mdListItemPattern = regexp.MustCompile(`(?m)^\s*(?:[-*+]\s|\d+[.)]\s|[一二三四五六七八九十]+[、.])`)
	// mdCodeFencePattern detects any fenced code block.
	mdCodeFencePattern = regexp.MustCompile("(?m)^\\s{0,3}(?:```|~~~)")
	// mdFormulaPattern matches a block-level math formula ($$...$$).
	mdFormulaPattern = regexp.MustCompile(`(?s)\$\$.*?\$\$`)
)

// classifyContent returns the dominant category for a piece of markdown.
// The heuristic is simple: whichever of the structural patterns covers the
// majority of non-whitespace lines wins; otherwise "text".
func classifyContent(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return types.ChunkCategoryText
	}

	hasTable := mdTablePattern.MatchString(text)
	hasCode := mdCodeFencePattern.MatchString(text)
	hasFormula := mdFormulaPattern.MatchString(text)

	// Count list lines as a ratio of all non-empty lines.
	lines := strings.Split(text, "\n")
	total, listN := 0, 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		total++
		if mdListItemPattern.MatchString(l) {
			listN++
		}
	}
	listDominant := total > 0 && listN*2 >= total // ≥ 50 %

	switch {
	case hasTable && !listDominant && !hasCode:
		return types.ChunkCategoryTable
	case hasCode && !hasTable && !listDominant:
		return types.ChunkCategoryCode
	case hasFormula && !hasTable && !listDominant && !hasCode:
		return types.ChunkCategoryFormula
	case listDominant && !hasTable && !hasCode:
		return types.ChunkCategoryList
	case (hasTable || hasCode || hasFormula) && listDominant:
		return types.ChunkCategoryMixed
	default:
		return types.ChunkCategoryText
	}
}

// ---------------------------------------------------------------------------
// Breadcrumb rendering
// ---------------------------------------------------------------------------

// renderBreadcrumb returns a single-line breadcrumb representation, e.g.
//
//	> 头孢曲松说明书 > 三、禁忌 > （一）与钙剂合用
//
// Returns "" for an empty path.
func renderBreadcrumb(path []string) string {
	if len(path) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("> ")
	for i, p := range path {
		if i > 0 {
			b.WriteString(" > ")
		}
		b.WriteString(strings.TrimSpace(p))
	}
	return b.String()
}

// prependBreadcrumb injects the breadcrumb in front of the chunk body,
// separated by a blank line. Skips injection when the body already starts
// with the breadcrumb (happens on re-chunking of previously injected content).
func prependBreadcrumb(body, breadcrumb string) string {
	if breadcrumb == "" {
		return body
	}
	bodyTrim := strings.TrimLeft(body, "\r\n\t ")
	if strings.HasPrefix(bodyTrim, breadcrumb) {
		return body
	}
	return breadcrumb + "\n\n" + strings.TrimLeft(body, "\r\n")
}

// ---------------------------------------------------------------------------
// Main entry
// ---------------------------------------------------------------------------

// SplitMarkdown is the public entry point. It parses the document structure,
// emits one chunk per heading section when the section fits in SoftMaxChars,
// and falls back to the recursive splitter for oversized sections. Every
// emitted chunk carries a heading_path / section_code / category via
// MarkdownChunk.ToMetadata().
//
// When cfg.InjectBreadcrumbs is true, each chunk's Content is prefixed with
// a breadcrumb line so the embedder and LLM see the section context.
// Positions (Start/End) refer to the ORIGINAL document, not the breadcrumb-
// prefixed content; this lets citation/UI surface exact source ranges.
func SplitMarkdown(text string, cfg MarkdownConfig) []MarkdownChunk {
	cfg.applyDefaults()

	if strings.TrimSpace(text) == "" {
		return nil
	}

	// Step 0a: normalise MinerU pipeline-style inline "## 标题" streams.
	text = normalizeInlineHeadings(text)
	// Step 0b: merge MinerU VLM's "number-only heading + orphan title row"
	// pairs so "# 1\n\n材料与方法" becomes the intended "# 1 材料与方法".
	text = mergeOrphanNumberHeadings(text)
	// Step 0c: break paragraphs in front of inline "。1.1xxx" subsection
	// markers (MinerU pipeline mode).
	text = breakSubsectionParagraphs(text)
	// Step 0d: promote line-start "1.1 实验动物 正文..." patterns (MinerU VLM
	// mode) into proper H3 ATX headings so heading_path distinguishes
	// subsections. All these passes are idempotent on clean markdown.
	text = promoteMineruSubsectionHeadings(text)

	headings := parseHeadings(text, cfg.MaxHeadingDepth)
	sections := buildSections(text, headings)

	if len(sections) == 0 {
		// Defensive: no parseable content. Fall back to a single chunk.
		return []MarkdownChunk{{
			Chunk: Chunk{
				Content: text,
				Seq:     0,
				Start:   0,
				End:     runeLen(text),
			},
			Category: types.ChunkCategoryText,
		}}
	}

	// Minimum meaningful section size. Preamble text (before the first
	// heading) below this threshold is typically a journal column label or
	// page-header artefact that MinerU leaked into the markdown stream
	// (e.g. "基础研究", "论著"). Dropping them removes tiny useless chunks
	// from the knowledge base without losing real content.
	const minPreambleRuneLen = 20

	var out []MarkdownChunk
	seq := 0
	for _, sec := range sections {
		body := text[sec.start:sec.end]
		trimmedBody := strings.TrimRight(body, "\r\n")
		if strings.TrimSpace(trimmedBody) == "" {
			continue // skip whitespace-only sections (consecutive headings)
		}
		// Drop short preamble sections that precede the first heading,
		// BUT only when the document has at least one real heading
		// elsewhere. A heading-free doc must keep everything, even tiny
		// content, because there is no alternative home for it.
		if len(sec.headingPath) == 0 && len(headings) > 0 &&
			runeLen(strings.TrimSpace(trimmedBody)) < minPreambleRuneLen {
			continue
		}

		category := classifyContent(trimmedBody)
		breadcrumb := ""
		if cfg.InjectBreadcrumbs {
			breadcrumb = renderBreadcrumb(sec.headingPath)
		}

		// Fast path: section fits.
		if runeLen(trimmedBody) <= cfg.SoftMaxChars {
			content := trimmedBody
			if breadcrumb != "" {
				content = prependBreadcrumb(content, breadcrumb)
			}
			out = append(out, MarkdownChunk{
				Chunk: Chunk{
					Content: content,
					Seq:     seq,
					// Use rune offsets from the original document. buildSections
					// tracks byte offsets; convert so positional tracking
					// matches the rest of the chunker pipeline.
					Start: runeLen(text[:sec.start]),
					End:   runeLen(text[:sec.start]) + runeLen(trimmedBody),
				},
				HeadingPath:  append([]string(nil), sec.headingPath...),
				HeadingLevel: sec.level,
				SectionCode:  sec.sectionCode,
				Category:     category,
			})
			seq++
			continue
		}

		// List-dominant section under the hard cap: keep intact.
		if cfg.KeepListIntact && category == types.ChunkCategoryList &&
			runeLen(trimmedBody) <= cfg.HardMaxChars {
			content := trimmedBody
			if breadcrumb != "" {
				content = prependBreadcrumb(content, breadcrumb)
			}
			out = append(out, MarkdownChunk{
				Chunk: Chunk{
					Content: content,
					Seq:     seq,
					Start:   runeLen(text[:sec.start]),
					End:     runeLen(text[:sec.start]) + runeLen(trimmedBody),
				},
				HeadingPath:  append([]string(nil), sec.headingPath...),
				HeadingLevel: sec.level,
				SectionCode:  sec.sectionCode,
				Category:     category,
			})
			seq++
			continue
		}

		// Slow path: sub-split via the recursive splitter, each sub-chunk
		// inherits the section's heading path.
		subCfg := SplitterConfig{
			ChunkSize:    cfg.ChildChunkSize,
			ChunkOverlap: cfg.ChildChunkOverlap,
			Separators:   cfg.Separators,
		}
		subs := SplitText(trimmedBody, subCfg)
		// Base rune offset of the section inside the document.
		secBaseRune := runeLen(text[:sec.start])
		for _, sub := range subs {
			subContent := sub.Content
			// Derive sub-category from the sub-chunk text rather than the
			// whole section so e.g. a trailing table inside a long "text"
			// section still gets a table category.
			subCategory := classifyContent(subContent)
			if breadcrumb != "" {
				subContent = prependBreadcrumb(subContent, breadcrumb)
			}
			out = append(out, MarkdownChunk{
				Chunk: Chunk{
					Content: subContent,
					Seq:     seq,
					Start:   secBaseRune + sub.Start,
					End:     secBaseRune + sub.End,
				},
				HeadingPath:  append([]string(nil), sec.headingPath...),
				HeadingLevel: sec.level,
				SectionCode:  sec.sectionCode,
				Category:     subCategory,
			})
			seq++
		}
	}

	return out
}

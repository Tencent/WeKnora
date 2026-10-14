package service

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
)

var (
	htmlTagPattern    = regexp.MustCompile(`<[^>]+>`)
	codeBlockPattern  = regexp.MustCompile("(?s)^\\s*```[a-zA-Z]*\\s*\n(.*?)\n\\s*```\\s*$")
	htmlDocPattern    = regexp.MustCompile(`(?i)^\s*(<\!DOCTYPE|<html|<body|<div|<p[\s>]|<table|<h[1-6][\s>])`)
	multipleNewlines  = regexp.MustCompile(`\n{3,}`)
	knownEmptyReplies = []string{
		"无文字内容",
		"无法识别",
		"no text",
		"no text content",
		"no content",
		"empty",
		"图片中没有文字",
		"图片中没有可识别的文字",
	}
)

// sanitizeOCRText cleans up VLM OCR output by stripping HTML wrappers,
// converting HTML to markdown, and filtering out useless responses.
func sanitizeOCRText(raw string) string {
	text, _ := validateOCRText(raw)
	return text
}

type ocrValidationError struct{ reason string }

func (e *ocrValidationError) Error() string { return fmt.Sprintf("invalid OCR output: %s", e.reason) }

type captionValidationError struct{ reason string }

func (e *captionValidationError) Error() string {
	return fmt.Sprintf("invalid caption output: %s", e.reason)
}

// validateOCRText distinguishes an explicit no-text answer from unusable model
// output. Reject the entire response, rather than indexing a plausible prefix
// followed by hallucinated repetition.
func validateOCRText(raw string) (string, error) {
	text := normalizeOCRText(raw)
	if isKnownEmptyReply(text) {
		return "", nil
	}
	if text == "" {
		return "", &ocrValidationError{reason: "empty_content"}
	}
	readable := false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) ||
			(unicode.IsSymbol(r) && !strings.ContainsRune("|~`", r)) {
			readable = true
			break
		}
	}
	if !readable {
		return "", &ocrValidationError{reason: "no_readable_content"}
	}
	if hasImageTextRepetition(text) {
		return "", &ocrValidationError{reason: "repetitive_content"}
	}
	return text, nil
}

// validateCaptionText rejects decoding loops before captions become searchable
// chunks. Unlike OCR output, an empty caption is not an error: models may omit
// a description when the image has no useful visual content.
func validateCaptionText(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", nil
	}
	if hasImageTextRepetition(text) {
		return "", &captionValidationError{reason: "repetitive_content"}
	}
	return text, nil
}

// Count the union of long periodic runs, including runs with different periods
// separated by readable text. Then catch two non-periodic loop shapes: repeated
// windows interrupted by small changing fields, and a runaway counter paired
// with one stuck value. Short repetitions (labels, table separators, numeric
// values) do not contribute. Normalize whitespace so line wrapping cannot hide
// a decoding loop. Work and storage remain linear in the response length.
func hasImageTextRepetition(text string) bool {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) < 512 {
		return false
	}
	// Range deltas avoid counting the same characters multiple times when a
	// run matches several periods, and keep interval recording constant-time.
	coverage := make([]int, len(runes)+1)
	for period := 1; period <= 64 && period <= len(runes)/32; period++ {
		matched := 0
		recordRun := func(end int) {
			span := matched + period
			if span >= 512 && span >= period*32 {
				coverage[end-span]++
				coverage[end]--
			}
		}
		for i := period; i < len(runes); i++ {
			if runes[i] == runes[i-period] {
				matched++
			} else {
				recordRun(i)
				matched = 0
			}
		}
		recordRun(len(runes))
	}
	active, repeated := 0, 0
	for i := range runes {
		active += coverage[i]
		if active > 0 {
			repeated++
		}
	}
	if repeated*5 >= len(runes)*4 {
		return true
	}
	return hasRepeatedWindowCoverage(runes) || hasAdvancingCounterLoop(text)
}

// hasRepeatedWindowCoverage catches a repeated sentence or clause even when a
// row number, counter, or other small field changes between copies. A window
// must occur at least 32 times, and qualifying windows must cover 80% of the
// output, so recurring table syntax or a handful of repeated labels is kept.
func hasRepeatedWindowCoverage(runes []rune) bool {
	const (
		windowRunes    = 16
		minOccurrences = 32
	)
	if len(runes) < windowRunes*minOccurrences {
		return false
	}

	counts := make(map[string]int, len(runes)-windowRunes+1)
	for start := 0; start+windowRunes <= len(runes); start++ {
		counts[string(runes[start:start+windowRunes])]++
	}

	coverage := make([]int, len(runes)+1)
	for start := 0; start+windowRunes <= len(runes); start++ {
		if counts[string(runes[start:start+windowRunes])] < minOccurrences {
			continue
		}
		coverage[start]++
		coverage[start+windowRunes]--
	}

	active, repeated := 0, 0
	for i := range runes {
		active += coverage[i]
		if active > 0 {
			repeated++
		}
	}
	return repeated*5 >= len(runes)*4
}

// hasAdvancingCounterLoop recognizes the observed "1 value 2 value ..."
// failure mode. Requiring at least 256 consecutive pairs and 80% token coverage
// avoids treating ordinary numbered lists or short constant-valued tables as
// model loops.
func hasAdvancingCounterLoop(text string) bool {
	const minPairs = 256
	tokens := strings.Fields(text)
	if len(tokens) < minPairs*2 {
		return false
	}

	for offset := 0; offset < 2; offset++ {
		pairs := 0
		for i := offset; i+3 < len(tokens); i += 2 {
			current, currentErr := strconv.ParseInt(tokens[i], 10, 64)
			next, nextErr := strconv.ParseInt(tokens[i+2], 10, 64)
			if currentErr == nil && nextErr == nil && next == current+1 && tokens[i+1] == tokens[i+3] {
				if pairs == 0 {
					pairs = 2
				} else {
					pairs++
				}
				if pairs >= minPairs && pairs*2*5 >= len(tokens)*4 {
					return true
				}
				continue
			}
			pairs = 0
		}
	}
	return false
}

func normalizeOCRText(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}

	text = stripMarkdownCodeBlock(text)

	// If stripping HTML tags leaves almost no text, the response is useless
	// (e.g. "<html><body><div class="image"><img/></div></body></html>").
	plainText := strings.TrimSpace(htmlTagPattern.ReplaceAllString(text, ""))
	if htmlTagPattern.MatchString(text) {
		// Preserve no-text answers before a short response is discarded or
		// inline tags become Markdown emphasis around the sentinel.
		if isKnownEmptyReply(plainText) {
			return plainText
		}
		if len(plainText) < 10 {
			return ""
		}
	}

	if looksLikeHTML(text) {
		text = ocrHTMLToMarkdown(text)
		text = strings.TrimSpace(text)
		if text == "" {
			return ""
		}
	}

	// Convert inline HTML <table> blocks to GFM tables. This is a safe no-op
	// when no <table> is present, so it runs unconditionally: a markdown body
	// with an embedded HTML table does not satisfy looksLikeHTML (it neither
	// starts with a tag nor is dominated by tag characters), yet leaving the
	// raw markup in place makes the chunker split inside table rows.
	text = docparser.NormalizeHTMLTables(text)

	text = multipleNewlines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// stripMarkdownCodeBlock removes a markdown code-fence wrapper that some
// models add around their output (e.g. ```html\n...\n``` or ```markdown\n...\n```).
func stripMarkdownCodeBlock(text string) string {
	if m := codeBlockPattern.FindStringSubmatch(text); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return text
}

// looksLikeHTML returns true when the text appears to be an HTML document
// or contains a significant amount of HTML tags.
func looksLikeHTML(text string) bool {
	if htmlDocPattern.MatchString(text) {
		return true
	}
	tags := htmlTagPattern.FindAllString(text, -1)
	if len(tags) == 0 {
		return false
	}
	tagChars := 0
	for _, t := range tags {
		tagChars += len(t)
	}
	return float64(tagChars)/float64(len(text)) > 0.3
}

// ocrHTMLToMarkdown converts HTML content to markdown, falling back to the
// original text on failure.
func ocrHTMLToMarkdown(content string) string {
	md, err := htmltomd.ConvertString(content)
	if err != nil {
		return content
	}
	return md
}

// isKnownEmptyReply checks whether the text matches a known "no content"
// reply pattern that VLM models produce when the image has no text.
// Trailing punctuation (., !, ?) is stripped before comparison so that
// responses like "No text content." still match "no text content".
func isKnownEmptyReply(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	lower = strings.TrimRight(lower, ".!?。！？")
	for _, phrase := range knownEmptyReplies {
		if lower == strings.ToLower(phrase) {
			return true
		}
	}
	return false
}

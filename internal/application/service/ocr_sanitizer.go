package service

import (
	"regexp"
	"strings"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
)

var (
	htmlTagPattern       = regexp.MustCompile(`<[^>]+>`)
	codeBlockPattern     = regexp.MustCompile("(?s)^\\s*```[a-zA-Z]*\\s*\n(.*?)\n\\s*```\\s*$")
	htmlDocPattern       = regexp.MustCompile(`(?i)^\s*(<\!DOCTYPE|<html|<body|<div|<p[\s>]|<table|<h[1-6][\s>])`)
	multipleNewlines     = regexp.MustCompile(`\n{3,}`)
	knownEmptyReplies    = []string{
		"无文字内容",
		"无法识别",
		"no text",
		"no content",
		"empty",
		"图片中没有文字",
		"图片中没有可识别的文字",
	}
)

// sanitizeOCRText cleans up VLM OCR output by stripping HTML wrappers,
// converting HTML to markdown, and filtering out useless responses.
func sanitizeOCRText(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}

	text = stripMarkdownCodeBlock(text)

	// If stripping HTML tags leaves almost no text, the response is useless
	// (e.g. "<html><body><div class="image"><img/></div></body></html>").
	plainText := strings.TrimSpace(htmlTagPattern.ReplaceAllString(text, ""))
	if len(plainText) < 10 && htmlTagPattern.MatchString(text) {
		return ""
	}

	if looksLikeHTML(text) {
		text = ocrHTMLToMarkdown(text)
		text = strings.TrimSpace(text)
		if text == "" {
			return ""
		}
	}

	if isKnownEmptyReply(text) {
		return ""
	}

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
func isKnownEmptyReply(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, phrase := range knownEmptyReplies {
		if lower == strings.ToLower(phrase) {
			return true
		}
	}
	return false
}

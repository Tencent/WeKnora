package service

import (
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var markdownWikiLinkRegex = regexp.MustCompile(`\[\[((?:\\.|[^\]\\\r\n])+)\]\]`)

type markdownWikiLink struct {
	start, end               int
	slug, display, separator string
}

func wikiOutLinks(content string) types.StringArray {
	seen := make(map[string]bool)
	var links types.StringArray
	for _, link := range markdownWikiLinks(content) {
		if !seen[link.slug] {
			seen[link.slug] = true
			links = append(links, link.slug)
		}
	}
	return links
}

func rewriteMarkdownWikiLinks(content string, replace func(markdownWikiLink) string) (string, bool) {
	var out strings.Builder
	last := 0
	changed := false
	for _, link := range markdownWikiLinks(content) {
		replacement := replace(link)
		if replacement == content[link.start:link.end] {
			continue
		}
		changed = true
		out.WriteString(content[last:link.start])
		out.WriteString(replacement)
		last = link.end
	}
	if !changed {
		return content, false
	}
	out.WriteString(content[last:])
	return out.String(), true
}

// markdownCodeSpans returns the byte ranges covered by fenced and indented
// code blocks (including a fence's info string) and inline code spans.
// Derivation, repair and ingest sanitization share it so literal examples
// never become graph edges or repair targets.
func markdownCodeSpans(content string) []span {
	source := []byte(content)
	var code []span
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.CodeBlock, *ast.FencedCodeBlock:
			if fence, ok := n.(*ast.FencedCodeBlock); ok && fence.Info != nil {
				segment := fence.Info.Segment
				code = append(code, span{segment.Start, segment.Stop})
			}
			for i := 0; i < node.Lines().Len(); i++ {
				segment := node.Lines().At(i)
				code = append(code, span{segment.Start, segment.Stop})
			}
			return ast.WalkSkipChildren, nil
		case *ast.CodeSpan:
			for child := n.FirstChild(); child != nil; child = child.NextSibling() {
				if textNode, ok := child.(*ast.Text); ok {
					segment := textNode.Segment
					code = append(code, span{segment.Start, segment.Stop})
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return code
}

// markdownWikiLinks keeps derivation and repair on the same source spans.
// In particular, a table's escaped alias separator is not part of its slug,
// and literal examples must not become graph edges or repair targets.
func markdownWikiLinks(content string) []markdownWikiLink {
	code := markdownCodeSpans(content)

	var links []markdownWikiLink
	for _, match := range markdownWikiLinkRegex.FindAllStringSubmatchIndex(content, -1) {
		start, end := match[0], match[1]
		for len(code) > 0 && code[0].end <= start {
			code = code[1:]
		}
		if len(code) > 0 && code[0].start < end || wikiLinkEscaped(content, start) {
			continue
		}
		inner := content[match[2]:match[3]]
		rawSlug, display, separator := inner, "", "|"
		if pipe := strings.IndexByte(inner, '|'); pipe >= 0 {
			slugEnd := pipe
			if wikiLinkEscaped(inner, pipe) {
				slugEnd--
			}
			rawSlug, display, separator = inner[:slugEnd], strings.TrimSpace(inner[pipe+1:]), inner[slugEnd:pipe+1]
		}
		slug := normalizeSlug(string(util.UnescapePunctuations([]byte(rawSlug))))
		if slug != "" {
			links = append(links, markdownWikiLink{
				start: start, end: end, slug: slug, display: display, separator: separator,
			})
		}
	}
	return links
}

func wikiLinkEscaped(content string, offset int) bool {
	backslashes := 0
	for i := offset - 1; i >= 0 && content[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 != 0
}

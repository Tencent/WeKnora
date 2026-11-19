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
	start, end                        int
	slug, rawSlug, display, separator string
}

func (link markdownWikiLink) targetSlug(isLive func(string) bool) string {
	// An exact link to a page created before slug normalization keeps its identity.
	if link.rawSlug != link.slug && isLive != nil && isLive(link.rawSlug) {
		return link.rawSlug
	}
	return link.slug
}

func (link markdownWikiLink) withSlug(slug string) string {
	replacement := "[[" + slug
	if link.display != "" {
		replacement += link.separator + link.display
	}
	return replacement + "]]"
}

func wikiOutLinks(links []markdownWikiLink, isLive func(string) bool) types.StringArray {
	seen := make(map[string]bool)
	var slugs types.StringArray
	for _, link := range links {
		slug := link.targetSlug(isLive)
		if !seen[slug] {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}
	return slugs
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

// markdownWikiLinks keeps derivation and repair on the same source spans.
// In particular, a table's escaped alias separator is not part of its slug,
// and literal examples must not become graph edges or repair targets.
func markdownWikiLinks(content string) []markdownWikiLink {
	source := []byte(content)
	type span struct{ start, end int }
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
		rawSlug = strings.TrimSpace(string(util.UnescapePunctuations([]byte(rawSlug))))
		slug := normalizeSlug(rawSlug)
		if slug != "" {
			links = append(links, markdownWikiLink{
				start: start, end: end, slug: slug, rawSlug: rawSlug, display: display, separator: separator,
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

package service

import (
	"html"
	"net/url"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

func normalizeWikiPageRelativeLinks(page *types.WikiPage) {
	page.Content = normalizeWikiRelativeLinks(page.Content)
	page.Summary = normalizeWikiRelativeLinks(page.Summary)
}

type wikiMarkdownLinkSpan struct {
	start, labelEnd, end int
}

// Capture source boundaries from the existing Markdown parser instead of
// reimplementing nested labels, destinations, titles and reference syntax.
type wikiMarkdownLinkParser struct {
	parser.InlineParser
	spans map[*ast.Link]wikiMarkdownLinkSpan
}

func (p *wikiMarkdownLinkParser) Parse(parent ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	_, before := reader.Position()
	node := p.InlineParser.Parse(parent, reader, pc)
	if link, ok := node.(*ast.Link); ok {
		_, after := reader.Position()
		p.spans[link] = wikiMarkdownLinkSpan{link.Pos(), before.Start, after.Start}
	}
	return node
}

func (p *wikiMarkdownLinkParser) CloseBlock(parent ast.Node, reader text.Reader, pc parser.Context) {
	if closer, ok := p.InlineParser.(parser.CloseBlocker); ok {
		closer.CloseBlock(parent, reader, pc)
	}
}

func isRelativeMarkdownDocument(destination []byte) bool {
	value := html.UnescapeString(string(util.UnescapePunctuations(destination)))
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "" && u.Host == "" &&
		!strings.HasPrefix(u.Path, "/") && strings.HasSuffix(strings.ToLower(u.Path), ".md")
}

// normalizeWikiRelativeLinks implements the same fallback as WikiBrowser for
// source-relative .md links: Wiki pages have slug routes, not source-file paths.
// Only link syntax is removed; labels, formatting, images and code stay intact.
func normalizeWikiRelativeLinks(content string) string {
	if !strings.Contains(content, "[") {
		return content
	}
	source := []byte(content)
	capture := &wikiMarkdownLinkParser{
		InlineParser: parser.NewLinkParser(), spans: make(map[*ast.Link]wikiMarkdownLinkSpan),
	}
	inlines := parser.DefaultInlineParsers()
	for i := range inlines {
		if inlines[i].Value == capture.InlineParser {
			inlines[i].Value = capture
		}
	}
	md := parser.NewParser(
		parser.WithBlockParsers(parser.DefaultBlockParsers()...),
		parser.WithInlineParsers(inlines...),
		parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
	)
	document := md.Parse(text.NewReader(source))
	type deletion struct{ start, end int }
	var deletions []deletion
	var definitions []*ast.LinkReferenceDefinition
	imageReferences := make(map[string]bool)
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Image:
			if n.Reference != nil {
				imageReferences[util.ToLinkReference(n.Reference.Value)] = true
			}
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			if !isRelativeMarkdownDocument(n.Destination) {
				break
			}
			if span, ok := capture.spans[n]; ok {
				deletions = append(deletions, deletion{span.start, span.start + 1}, deletion{span.labelEnd, span.end})
			}
		case *ast.LinkReferenceDefinition:
			definitions = append(definitions, n)
		}
		return ast.WalkContinue, nil
	})
	// Retain definitions used by images, but remove otherwise-unused relative
	// document definitions so API/search content does not retain dead paths.
	for _, definition := range definitions {
		if !isRelativeMarkdownDocument(definition.Destination) ||
			imageReferences[util.ToLinkReference(definition.Label)] {
			continue
		}
		for i := 0; i < definition.Lines().Len(); i++ {
			segment := definition.Lines().At(i)
			deletions = append(deletions, deletion{segment.Start, segment.Stop})
		}
	}
	if len(deletions) == 0 {
		return content
	}
	sort.Slice(deletions, func(i, j int) bool { return deletions[i].start < deletions[j].start })
	var out strings.Builder
	last := 0
	for _, edit := range deletions {
		if edit.start < last || edit.end > len(content) || edit.start > edit.end {
			continue
		}
		out.WriteString(content[last:edit.start])
		last = edit.end
	}
	out.WriteString(content[last:])
	return out.String()
}

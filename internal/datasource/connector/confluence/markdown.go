package confluence

import (
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"
)

// newMarkdownConverter builds an HTML -> Markdown converter tuned for the HTML
// that Confluence returns in `body.view`.
//
// The previous implementation used the bare htmltomd.ConvertString helper, which
// only enables the base + commonmark plugins. As a result the following content
// was silently lost during import:
//   - tables (no table plugin) -> cells were flattened into one line
//   - strikethrough / rich text decorations (no strikethrough plugin)
//   - inline SVG (drawio diagrams, and mermaid diagrams that Confluence renders
//     as SVG) -> the <svg> element was dropped, only its text leaked through
//   - mermaid source code blocks
//
// See internal/datasource/connector/confluence/connector.go (markdownItem).
func newMarkdownConverter() *converter.Converter {
	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(),
			strikethrough.NewStrikethroughPlugin(),
		),
	)

	// Keep inline SVG blocks (drawio diagrams and mermaid diagrams that are
	// rendered as SVG by the Confluence macro) verbatim, so the visual content
	// is not dropped during the HTML -> Markdown conversion.
	conv.Register.RendererFor("svg", converter.TagTypeBlock, func(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
		var sb strings.Builder
		if err := html.Render(&sb, node); err != nil {
			return converter.RenderTryNext
		}
		_, _ = w.WriteString("\n\n" + sb.String() + "\n\n")
		return converter.RenderSuccess
	}, 100)

	// Preserve mermaid diagram source as a ```mermaid fenced code block so it
	// can be re-rendered by the markdown viewer. Confluence mermaid macros
	// usually wrap the source in <pre class="mermaid"> or <div class="mermaid">.
	// Non-mermaid elements fall through to the default (commonmark) handler.
	for _, tag := range []string{"pre", "div"} {
		conv.Register.RendererFor(tag, converter.TagTypeBlock, func(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
			if !hasClass(node, "mermaid") && !hasClass(node, "language-mermaid") {
				return converter.RenderTryNext
			}
			_, _ = w.WriteString("\n\n```mermaid\n" + textContent(node) + "\n```\n\n")
			return converter.RenderSuccess
		}, 200)
	}

	return conv
}

// convertConfluenceHTML converts a Confluence page body (HTML) to Markdown.
//
// host is the Confluence base host; it is used to absolutize relative links and
// image references so they don't become dead links inside WeKnora (for example a
// parent page whose body is mostly links to its child pages).
func convertConfluenceHTML(host, html string) (string, error) {
	return newMarkdownConverter().ConvertString(html, converter.WithDomain(host))
}

// hasClass reports whether node carries the given CSS class.
func hasClass(node *html.Node, name string) bool {
	for _, attr := range node.Attr {
		if attr.Key != "class" {
			continue
		}
		for _, c := range strings.Fields(attr.Val) {
			if c == name {
				return true
			}
		}
	}
	return false
}

// textContent recursively collects the text of node (without any markup).
func textContent(node *html.Node) string {
	if node.Type == html.TextNode {
		return node.Data
	}
	var sb strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		sb.WriteString(textContent(child))
	}
	return sb.String()
}

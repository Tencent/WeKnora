package confluence

import (
	"bytes"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"
)

// convertConfluenceHTML converts Confluence page HTML (the rendered `view`
// representation) into Markdown while preserving content that the default
// html-to-markdown converter would otherwise drop:
//   - tables and strikethrough (commonmark + table/strikethrough plugins)
//   - inline diagrams: <svg> (drawio / rendered mermaid) are kept verbatim
//   - mermaid source blocks
//   - fenced code blocks with their language
//   - info/warning/note/tip/error panels as labeled blockquotes
//   - status lozenges, user mentions and change-diff markup
//
// domain is used to turn relative links/images into absolute URLs. Pass the
// Confluence base URL (including scheme, e.g. https://host/wiki) so links on
// HTTPS Cloud instances are not wrongly rewritten to http://.
func convertConfluenceHTML(domain, html string) (string, error) {
	conv := newConfluenceConverter()

	markdown, err := conv.ConvertString(html, converter.WithDomain(domain))
	if err != nil {
		return "", err
	}
	return markdown, nil
}

func newConfluenceConverter() *converter.Converter {
	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(),
			strikethrough.NewStrikethroughPlugin(),
		),
	)

	// Keep inline diagrams (drawio / rendered mermaid) verbatim so they survive
	// the round-trip instead of being stripped as unknown elements.
	conv.Register.RendererFor("svg", converter.TagTypeBlock, func(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
		var buf bytes.Buffer
		_ = html.Render(&buf, node)
		_, _ = w.WriteString("\n\n" + buf.String() + "\n\n")
		return converter.RenderSuccess
	}, 100)

	// <pre>: a mermaid block keeps its source; everything else becomes a fenced
	// code block with the language detected from Confluence's class attribute.
	conv.Register.RendererFor("pre", converter.TagTypeBlock, func(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
		if hasClass(node, "mermaid") || hasClass(node, "language-mermaid") {
			_, _ = w.WriteString("\n\n```mermaid\n" + strings.TrimRight(textContent(node), "\n") + "\n```\n\n")
			return converter.RenderSuccess
		}
		lang := codeLanguage(node)
		_, _ = w.WriteString("\n\n```" + lang + "\n" + strings.TrimRight(textContent(node), "\n") + "\n```\n\n")
		return converter.RenderSuccess
	}, 200)

	// <div>: mermaid wrappers and Confluence info/warning/note/tip panels.
	conv.Register.RendererFor("div", converter.TagTypeBlock, func(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
		if hasClass(node, "mermaid") {
			_, _ = w.WriteString("\n\n```mermaid\n" + strings.TrimRight(textContent(node), "\n") + "\n```\n\n")
			return converter.RenderSuccess
		}
		if isConfluencePanel(node) {
			return renderPanel(ctx, w, node)
		}
		return converter.RenderTryNext
	}, 200)

	// <span>: status lozenges and page change-diff markup.
	conv.Register.RendererFor("span", converter.TagTypeInline, func(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
		cls := attr(node, "class")
		switch {
		case strings.Contains(cls, "status-macro"):
			_, _ = w.WriteString("[" + strings.TrimSpace(textContent(node)) + "]")
			return converter.RenderSuccess
		case strings.Contains(cls, "diff-html-added"):
			_, _ = w.WriteString("**" + textContent(node) + "**")
			return converter.RenderSuccess
		case strings.Contains(cls, "diff-html-removed"):
			_, _ = w.WriteString("~~" + textContent(node) + "~~")
			return converter.RenderSuccess
		}
		return converter.RenderTryNext
	}, 200)

	// <a>: Confluence user mentions become a friendly @Name instead of a bare
	// profile link. Any other anchor falls back to the default link handling.
	conv.Register.RendererFor("a", converter.TagTypeInline, func(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
		if strings.Contains(attr(node, "class"), "user-mention") {
			name := strings.TrimSpace(textContent(node))
			if name != "" {
				_, _ = w.WriteString("@" + name)
				return converter.RenderSuccess
			}
		}
		return converter.RenderTryNext
	}, 200)

	return conv
}

// isConfluencePanel reports whether a <div> is a Confluence info/warning/note/
// tip/error panel. We match the specific panel-type classes (and panelMacro)
// rather than the bare "panel" token, because code macros render as
// class="code panel" and must not be mistaken for panels.
func isConfluencePanel(node *html.Node) bool {
	if hasClass(node, "panelMacro") {
		return true
	}
	for _, kind := range []string{"panelNote", "panelInfo", "panelWarning", "panelTip", "panelError"} {
		if hasClass(node, kind) {
			return true
		}
	}
	return false
}

// renderPanel converts a Confluence panel (info/warning/note/tip/error) into a
// labeled Markdown blockquote. The inner content is rendered recursively so any
// nested formatting (lists, tables, links) is preserved.
func renderPanel(ctx converter.Context, w converter.Writer, node *html.Node) converter.RenderStatus {
	label := ""
	for _, kind := range []string{"panelNote", "panelInfo", "panelWarning", "panelTip", "panelError"} {
		if hasClass(node, kind) {
			label = strings.TrimPrefix(kind, "panel")
			break
		}
	}

	var inner bytes.Buffer
	ctx.RenderChildNodes(ctx, &inner, node)
	innerStr := strings.Trim(inner.String(), "\n")
	if innerStr == "" {
		return converter.RenderSuccess
	}

	_, _ = w.WriteString("\n\n")
	if label != "" {
		_, _ = w.WriteString("> **" + label + "**\n")
	}
	for _, line := range strings.Split(innerStr, "\n") {
		if line == "" {
			_, _ = w.WriteString(">\n")
		} else {
			_, _ = w.WriteString("> " + line + "\n")
		}
	}
	_, _ = w.WriteString("\n")
	return converter.RenderSuccess
}

func attr(node *html.Node, name string) string {
	for _, a := range node.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func hasClass(node *html.Node, cls string) bool {
	return strings.Contains(" "+attr(node, "class")+" ", " "+cls+" ")
}

// textContent returns the concatenated text of a node, skipping <script>/<style>
// and turning <br> into newlines so code/mermaid blocks stay readable.
func textContent(node *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			sb.WriteString(n.Data)
		case html.ElementNode:
			if n.Data == "script" || n.Data == "style" {
				return
			}
			if n.Data == "br" {
				sb.WriteString("\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	return sb.String()
}

// codeLanguage detects the language of a Confluence code block from either the
// <pre> class (e.g. "brush:java; gutter:false") or a nested <code> element
// (e.g. class="language-java").
func codeLanguage(node *html.Node) string {
	for _, c := range strings.Fields(attr(node, "class")) {
		if strings.HasPrefix(c, "brush:") {
			v := strings.TrimPrefix(c, "brush:")
			if i := strings.IndexByte(v, ';'); i >= 0 {
				v = v[:i]
			}
			if v != "" {
				return v
			}
		}
		if strings.HasPrefix(c, "language-") {
			return strings.TrimPrefix(c, "language-")
		}
		if strings.HasPrefix(c, "lang-") {
			return strings.TrimPrefix(c, "lang-")
		}
	}
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "code" {
			for _, cc := range strings.Fields(attr(c, "class")) {
				if strings.HasPrefix(cc, "language-") {
					return strings.TrimPrefix(cc, "language-")
				}
				if strings.HasPrefix(cc, "lang-") {
					return strings.TrimPrefix(cc, "lang-")
				}
			}
		}
	}
	return ""
}

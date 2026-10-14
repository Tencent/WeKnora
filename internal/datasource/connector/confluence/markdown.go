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
//   - mermaid source blocks
//   - fenced code blocks with their language
//   - info/warning/note/tip/error panels as labeled blockquotes
//   - status lozenges, user mentions and change-diff markup
//
// domain is used to turn relative links/images into absolute URLs. Pass the
// Confluence base URL (including scheme, e.g. https://host/wiki) so links on
// HTTPS Cloud instances are not wrongly rewritten to http://.
//
// Raw <svg> (drawio) preservation is intentionally out of scope here; it is
// handled separately to keep the diagram markup out of the chunk/embedding
// pipeline.
func convertConfluenceHTML(domain, inHTML string) (string, error) {
	// Normalize Confluence-specific language hints into the `language-*` class
	// that the commonmark plugin already understands. This lets us reuse the
	// default code-block renderer (which protects against embedded closing
	// fences and preserves whitespace) instead of hand-rolling a <pre> renderer.
	inHTML = normalizeConfluenceCodeBlocks(inHTML)

	conv := newConfluenceConverter()

	markdown, err := conv.ConvertString(inHTML, converter.WithDomain(domain))
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

// normalizeConfluenceCodeBlocks rewrites Confluence's code-block language hints
// into a standard `language-*` class on the <pre> element, so the commonmark
// plugin renders a properly fenced code block (with language, embedded-fence
// protection and whitespace preservation) without a custom renderer.
//
// Confluence exposes the language in several places depending on edition:
//   - <pre class="brush:java; gutter:false">   (legacy / Server)
//   - <pre class="language-java">              (already standard)
//   - <pre data-syntaxhighlighter-params="brush: java; ...">  (SyntaxHighlighter)
//   - <pre class="mermaid">                    (mermaid diagram source)
func normalizeConfluenceCodeBlocks(inHTML string) string {
	doc, err := html.Parse(strings.NewReader(inHTML))
	if err != nil {
		// On any parse failure, return the original input unchanged so the
		// converter still gets a chance to produce output.
		return inHTML
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "pre" {
			if lang := preLanguage(n); lang != "" {
				n.Attr = addLanguageClass(n.Attr, lang)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	var buf bytes.Buffer
	_ = html.Render(&buf, doc)
	return buf.String()
}

// preLanguage extracts the code language for a <pre> element from the various
// Confluence representations. It returns "" when no language can be detected.
func preLanguage(n *html.Node) string {
	// SyntaxHighlighter stores the language in a single semicolon-separated attr.
	if params := attr(n, "data-syntaxhighlighter-params"); params != "" {
		for _, part := range strings.Split(params, ";") {
			kv := strings.SplitN(strings.TrimSpace(part), ":", 2)
			if len(kv) != 2 {
				continue
			}
			key, val := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
			if (key == "brush" || key == "language" || key == "lang") && val != "" {
				return val
			}
		}
	}

	for _, c := range strings.Fields(attr(n, "class")) {
		if v, ok := strings.CutPrefix(c, "brush:"); ok {
			v = strings.TrimPrefix(v, "brush:")
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

	if strings.Contains(attr(n, "class"), "mermaid") {
		return "mermaid"
	}
	return ""
}

// addLanguageClass appends a `language-<lang>` token to a node's class
// attribute (creating the attribute if necessary) without duplicating it.
func addLanguageClass(attrs []html.Attribute, lang string) []html.Attribute {
	token := "language-" + lang
	for i, a := range attrs {
		if !strings.EqualFold(a.Key, "class") {
			continue
		}
		if a.Val == "" {
			attrs[i].Val = token
		} else if !strings.Contains(" "+a.Val+" ", " "+token+" ") {
			attrs[i].Val = a.Val + " " + token
		}
		return attrs
	}
	return append(attrs, html.Attribute{Key: "class", Val: token})
}

// isConfluencePanel reports whether a <div> is a Confluence info/warning/note/
// tip/error panel. We match the specific panel-type classes (both the legacy
// `panel*` tokens and the modern `confluence-information-macro-*` tokens used by
// Confluence Cloud/Data Center) rather than the bare "panel" token, because code
// macros render as class="code panel" and must not be mistaken for panels.
func isConfluencePanel(node *html.Node) bool {
	if hasClass(node, "panelMacro") {
		return true
	}
	for _, kind := range []string{
		"panelNote", "panelInfo", "panelWarning", "panelTip", "panelError",
		"confluence-information-macro-information",
		"confluence-information-macro-note",
		"confluence-information-macro-warning",
		"confluence-information-macro-tip",
	} {
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
	label := panelLabel(node)

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

// panelLabel returns the human-readable label for a Confluence panel, or "" for
// a generic (untyped) panel.
func panelLabel(node *html.Node) string {
	cases := []struct {
		class string
		label string
	}{
		{"panelNote", "Note"},
		{"panelInfo", "Info"},
		{"panelWarning", "Warning"},
		{"panelTip", "Tip"},
		{"panelError", "Error"},
		{"confluence-information-macro-information", "Info"},
		{"confluence-information-macro-note", "Note"},
		{"confluence-information-macro-warning", "Warning"},
		{"confluence-information-macro-tip", "Tip"},
	}
	for _, c := range cases {
		if hasClass(node, c.class) {
			return c.label
		}
	}
	return ""
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

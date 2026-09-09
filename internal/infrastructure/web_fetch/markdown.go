package web_fetch

import (
	"bytes"
	"net/url"
	"strings"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/PuerkitoBio/goquery"
)

// Prefer Readability, then main/article/body, and preserve
// source structure with Markdown. All downloads still use the shared SSRF guard.
func htmlToMarkdown(source, rawURL string) (string, error) {
	pageURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(source))
	if err != nil {
		return "", newFetchError(ErrorHTMLParse, false, "HTML parse failed: %v", err)
	}
	title := strings.TrimSpace(doc.Find("title").First().Text())
	extractedArticle := false
	article, readErr := readability.FromReader(strings.NewReader(source), pageURL)
	if readErr == nil && article.Node != nil {
		var buf bytes.Buffer
		if article.RenderHTML(&buf) == nil && strings.TrimSpace(buf.String()) != "" {
			if extracted, parseErr := goquery.NewDocumentFromReader(&buf); parseErr == nil {
				doc = extracted
				extractedArticle = true
				if article.Title() != "" {
					title = article.Title()
				}
			}
		}
	}
	// The Go Readability port can retain nav nodes that Mozilla removes.
	doc.Find("script, style, noscript, nav, footer, aside, iframe, svg, form").Remove()
	if !extractedArticle {
		// Apply fallback cleanup only when Readability fails. Re-selecting a
		// nested .content element after successful extraction discards sibling sections.
		doc.Find("script, style, noscript, nav, header, footer, aside").Remove()
	}
	// Resolve links against the final redirected URL; never fetch embedded resources.
	doc.Find("a[href], img[src]").Each(func(_ int, node *goquery.Selection) {
		attr := "href"
		if goquery.NodeName(node) == "img" {
			attr = "src"
		}
		raw, _ := node.Attr(attr)
		target, parseErr := url.Parse(strings.TrimSpace(raw))
		if parseErr != nil {
			node.RemoveAttr(attr)
			return
		}
		target = pageURL.ResolveReference(target)
		if target.Scheme != "http" && target.Scheme != "https" {
			node.RemoveAttr(attr)
			return
		}
		node.SetAttr(attr, target.String())
	})
	main := doc.Find("body")
	if !extractedArticle {
		if candidate := doc.Find("main, article, [role='main'], .content, #content").First(); candidate.Length() != 0 {
			main = candidate
		}
	}
	main.Find("a").Each(func(_ int, link *goquery.Selection) {
		if strings.TrimSpace(link.Text()) == "" {
			link.Remove()
		}
	})
	html, err := main.Html()
	if err != nil {
		return "", newFetchError(ErrorHTMLParse, false, "HTML extraction failed: %v", err)
	}
	conv := converter.NewConverter(converter.WithPlugins(
		base.NewBasePlugin(), commonmark.NewCommonmarkPlugin(), table.NewTablePlugin(),
	))
	markdown, err := conv.ConvertString(html)
	if err != nil {
		return "", newFetchError(ErrorHTMLParse, false, "Markdown conversion failed: %v", err)
	}
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return "", nil
	}
	if title != "" {
		markdown = "# " + title + "\n\n" + markdown
	}
	return markdown, nil
}

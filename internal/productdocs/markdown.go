package productdocs

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// PublicBaseURL is where the documentation site serves these pages.
const PublicBaseURL = "https://weknora.weixin.qq.com/docs/"

// maxSectionLevel is the deepest heading that starts its own section; deeper
// headings stay in their section's text. The site anchors every level, and
// pages link to "####" headings, so those must be readable on their own.
const maxSectionLevel = 4

var (
	headingRE    = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*$`)
	customIDRE   = regexp.MustCompile(`\s*\{#([^}\s]+)\}$`)
	imageRE      = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	linkRE       = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)\)`)
	inlineCodeRE = regexp.MustCompile("`([^`]*)`")
	emphasisRE   = regexp.MustCompile(`\*\*([^*]+)\*\*|\*([^*\s][^*]*)\*`)
	captionRE    = regexp.MustCompile(`caption="([^"]*)"`)

	// slugSpecialRE and the steps in slugify follow VitePress's default
	// heading slugify (@mdit-vue/shared), so anchors match the published site.
	slugSpecialRE   = regexp.MustCompile("[\\s~`!@#$%^&*()\\-_+=\\[\\]{}|\\\\;:\"'\u201c\u201d\u2018\u2019<>,.?/]+")
	slugControlRE   = regexp.MustCompile(`[\x00-\x1f]`)
	slugCombiningRE = regexp.MustCompile(`[\x{0300}-\x{036f}]`)
	slugDashesRE    = regexp.MustCompile(`-{2,}`)
)

// Doc is one documentation page.
type Doc struct {
	Path     string // "03-features/14-wiki", the page's path under the site
	Category string // Sidebar group, e.g. "功能模块"
	Title    string
	Sections []*Section // In page order; the first is the text before any "##"
}

// URL is the page's public address.
func (d *Doc) URL() string { return PublicBaseURL + d.Path }

// Span returns the section at anchor followed by its subsections; "" is the
// whole page.
func (d *Doc) Span(anchor string) ([]*Section, bool) {
	if anchor == "" {
		return d.Sections, true
	}
	for i, s := range d.Sections {
		if s.Anchor != anchor {
			continue
		}
		end := i + 1
		for end < len(d.Sections) && d.Sections[end].Level > s.Level {
			end++
		}
		return d.Sections[i:end], true
	}
	return nil, false
}

// Render joins sections back into Markdown with their headings.
func Render(sections []*Section) string {
	var sb strings.Builder
	for _, s := range sections {
		if s.Level > 1 {
			sb.WriteString(strings.Repeat("#", s.Level) + " " + s.Heading + "\n\n")
		}
		if s.Content != "" {
			sb.WriteString(s.Content + "\n\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

// Section is the text under one heading of level 2 to maxSectionLevel, up to
// the next such heading.
type Section struct {
	Doc     *Doc
	Anchor  string // "" for the page's lead section
	Heading string
	Level   int      // 1 for the lead section, then 2 to maxSectionLevel
	Parents []string // Headings of the enclosing sections, outermost first
	Content string   // Cleaned Markdown shown to the model
	plain   string   // Content without link targets, for indexing and snippets
}

// URL is the section's public address.
func (s *Section) URL() string {
	if s.Anchor == "" {
		return s.Doc.URL()
	}
	return s.Doc.URL() + "#" + s.Anchor
}

// Trail is the heading path shown in search results.
func (s *Section) Trail() string {
	parts := []string{s.Doc.Title}
	parts = append(parts, s.Parents...)
	if s.Level > 1 {
		parts = append(parts, s.Heading)
	}
	return strings.Join(parts, " › ")
}

// parseDoc splits a page into sections. pagePath is its file name under the
// docs root, e.g. "03-features/14-wiki.md".
func parseDoc(pagePath, category, raw string) (*Doc, error) {
	doc := &Doc{Path: strings.TrimSuffix(pagePath, ".md"), Category: category}
	lines := strings.Split(stripFrontMatter(strings.ReplaceAll(raw, "\r\n", "\n")), "\n")
	slugs := map[string]bool{}
	current := &Section{Doc: doc, Level: 1}
	var body []string
	var stack []*Section
	flush := func() {
		current.Content = strings.TrimSpace(strings.Join(body, "\n"))
		current.plain = plainText(current.Content)
		doc.Sections = append(doc.Sections, current)
		body = nil
	}

	fence := ""
	dropFence := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "" {
				fence = ""
				if dropFence {
					dropFence = false
					continue
				}
			}
			if !dropFence {
				body = append(body, line)
			}
			continue
		}
		if marker := fenceMarker(trimmed); marker != "" {
			fence = marker
			if strings.TrimSpace(strings.TrimLeft(trimmed, marker[:1])) == "mermaid" {
				dropFence = true
				body = append(body, "（此处为流程图，略）")
				continue
			}
			body = append(body, line)
			continue
		}
		if strings.HasPrefix(trimmed, "<Screenshot") {
			block := trimmed
			for !strings.Contains(block, "/>") && i+1 < len(lines) {
				i++
				block += " " + strings.TrimSpace(lines[i])
			}
			if m := captionRE.FindStringSubmatch(block); m != nil && m[1] != "" {
				body = append(body, "（截图："+m[1]+"）")
			}
			continue
		}
		m := headingRE.FindStringSubmatch(line)
		if m == nil {
			body = append(body, rewriteLinks(doc.Path, line))
			continue
		}
		level := len(m[1])
		text, anchor := splitCustomID(m[2])
		text = plainText(text)
		anchor = uniqueSlug(slugs, anchor, text)
		switch {
		case level == 1 && doc.Title == "":
			doc.Title = text
		case level >= 2 && level <= maxSectionLevel:
			flush()
			for len(stack) > 0 && stack[len(stack)-1].Level >= level {
				stack = stack[:len(stack)-1]
			}
			parents := make([]string, 0, len(stack))
			for _, s := range stack {
				parents = append(parents, s.Heading)
			}
			current = &Section{Doc: doc, Anchor: anchor, Heading: text, Level: level, Parents: parents}
			stack = append(stack, current)
		default:
			body = append(body, rewriteLinks(doc.Path, line))
		}
	}
	if fence != "" {
		return nil, fmt.Errorf("%s: unterminated code fence", pagePath)
	}
	flush()
	if doc.Title == "" {
		return nil, fmt.Errorf("%s: no title heading", pagePath)
	}
	doc.Sections[0].Heading = doc.Title
	return doc, nil
}

func stripFrontMatter(raw string) string {
	if !strings.HasPrefix(raw, "---\n") {
		return raw
	}
	if end := strings.Index(raw[4:], "\n---\n"); end >= 0 {
		return raw[4+end+5:]
	}
	return raw
}

func fenceMarker(trimmed string) string {
	for _, ch := range []string{"`", "~"} {
		if strings.HasPrefix(trimmed, ch+ch+ch) {
			n := len(trimmed) - len(strings.TrimLeft(trimmed, ch))
			return strings.Repeat(ch, n)
		}
	}
	return ""
}

func splitCustomID(heading string) (text, id string) {
	if m := customIDRE.FindStringSubmatchIndex(heading); m != nil {
		return heading[:m[0]], heading[m[2]:m[3]]
	}
	return heading, ""
}

// uniqueSlug registers a heading's anchor the way markdown-it-anchor does:
// an explicit {#id} or the slugified text, suffixed "-1", "-2"… while taken.
func uniqueSlug(seen map[string]bool, custom, text string) string {
	base := custom
	if base == "" {
		base = slugify(text)
	}
	slug := base
	for i := 1; seen[slug]; i++ {
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	seen[slug] = true
	return slug
}

func slugify(s string) string {
	s = norm.NFKD.String(s)
	s = slugCombiningRE.ReplaceAllString(s, "")
	s = slugControlRE.ReplaceAllString(s, "")
	s = slugSpecialRE.ReplaceAllString(s, "-")
	s = slugDashesRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		s = "_" + s
	}
	return strings.ToLower(s)
}

// plainText drops Markdown syntax that is not words: link targets, image
// sources, code ticks and emphasis.
func plainText(s string) string {
	s = imageRE.ReplaceAllString(s, "$1")
	s = linkRE.ReplaceAllString(s, "$1")
	s = inlineCodeRE.ReplaceAllString(s, "$1")
	s = emphasisRE.ReplaceAllString(s, "${1}${2}")
	return strings.TrimSpace(s)
}

// rewriteLinks turns site-relative links into public URLs so the model can
// hand them to the user, and reduces images to their alt text.
func rewriteLinks(docPath, line string) string {
	line = imageRE.ReplaceAllStringFunc(line, func(m string) string {
		alt := imageRE.FindStringSubmatch(m)[1]
		if alt == "" {
			return ""
		}
		return "（图：" + alt + "）"
	})
	return linkRE.ReplaceAllStringFunc(line, func(m string) string {
		sub := linkRE.FindStringSubmatch(m)
		return "[" + sub[1] + "](" + resolveLink(docPath, sub[2]) + ")"
	})
}

func resolveLink(docPath, target string) string {
	switch {
	case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"),
		strings.HasPrefix(target, "mailto:"):
		return target
	case strings.HasPrefix(target, "#"):
		return PublicBaseURL + docPath + target
	case strings.HasPrefix(target, "/"):
		return PublicBaseURL + strings.TrimPrefix(target, "/")
	}
	file, anchor, _ := strings.Cut(target, "#")
	resolved := path.Clean(path.Join(path.Dir(docPath), strings.TrimSuffix(file, ".md")))
	if strings.HasPrefix(resolved, "..") {
		return target
	}
	if anchor != "" {
		resolved += "#" + anchor
	}
	return PublicBaseURL + resolved
}

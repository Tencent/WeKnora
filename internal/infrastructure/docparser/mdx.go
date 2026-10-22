package docparser

import (
	"regexp"
	"strings"
)

// MDX files are Markdown with JSX statements and components layered on top.
// Retrieval wants the prose, not the module wiring or the tag soup, so the
// simple reader downgrades MDX to plain Markdown before chunking:
//
//   - top-level `import` / `export` statements are dropped (module wiring,
//     never user-visible text);
//   - JSX/HTML component tags are stripped while their inner text is kept
//     (`<Callout>`attention`</Callout>` -> attention); self-closing
//     components vanish (their content lives in props, not the document);
//   - code fences and headings pass through untouched so heading-based
//     chunking keeps working.
//
// This is deliberately a text-level pass, not an MDX parser: it covers the
// shapes document sites emit (Docusaurus/Next.js pages) without pulling in a
// JS toolchain.

var (
	mdxStatementLine = regexp.MustCompile(`^(?:import|export)\s`)
	// An opening, closing or self-closing JSX/HTML tag. Attributes stay on
	// the same line (`[^>\n]*`), which is also what keeps `a < b` math from
	// being eaten: prose comparison operators have no closing `>` in tag
	// shape on the same line.
	mdxTag = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9]*(\s[^>\n]*)?>`)
)

func mdxFenceMarker(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```"
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~"
	}
	return ""
}

// DowngradeMDX reduces MDX to plain Markdown, leaving fenced code blocks
// untouched.
func DowngradeMDX(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	var fenceMarker string
	for _, line := range lines {
		if marker := mdxFenceMarker(line); marker != "" {
			if !inFence {
				inFence, fenceMarker = true, marker
			} else if marker == fenceMarker {
				inFence, fenceMarker = false, ""
			}
			out = append(out, line)
			continue
		}
		if inFence {
			out = append(out, line)
			continue
		}
		if mdxStatementLine.MatchString(strings.TrimLeft(line, " \t")) {
			continue
		}
		out = append(out, mdxTag.ReplaceAllString(line, ""))
	}
	return strings.Join(out, "\n")
}

package outline

import (
	"bytes"
	"net/url"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Rewrite only destinations selected by the Markdown parser, preserving the
// original formatting and code verbatim. Goldmark destinations are source slices,
// including reference definitions; pointer equality disambiguates identical text.
func absoluteLinks(body, source string) string {
	raw := []byte(body)
	root := goldmark.DefaultParser().Parse(text.NewReader(raw))
	base, err := url.Parse(source)
	if err != nil {
		return body
	}
	type edit struct {
		start, end  int
		replacement string
	}
	edits := map[int]edit{}
	visited := map[*byte]bool{}
	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest []byte
		switch n := node.(type) {
		case *ast.Link:
			dest = n.Destination
		case *ast.Image:
			dest = n.Destination
		default:
			return ast.WalkContinue, nil
		}
		if len(dest) == 0 || dest[0] == '#' || visited[&dest[0]] {
			return ast.WalkContinue, nil
		}
		visited[&dest[0]] = true
		u, err := url.Parse(string(util.UnescapePunctuations(dest)))
		if err != nil || u.IsAbs() {
			return ast.WalkContinue, nil
		}
		resolved := base.ResolveReference(u)
		if resolved.Scheme != "http" && resolved.Scheme != "https" {
			return ast.WalkContinue, nil
		}
		for offset := 0; offset < len(raw); {
			i := bytes.Index(raw[offset:], dest)
			if i < 0 {
				break
			}
			i += offset
			if &raw[i] == &dest[0] {
				replacement := strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(resolved.String())
				edits[i] = edit{i, i + len(dest), replacement}
				break
			}
			offset = i + 1
		}
		return ast.WalkContinue, nil
	})
	ordered := make([]edit, 0, len(edits))
	for _, e := range edits {
		ordered = append(ordered, e)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].start < ordered[j].start })
	var out strings.Builder
	offset := 0
	for _, e := range ordered {
		out.Write(raw[offset:e.start])
		out.WriteString(e.replacement)
		offset = e.end
	}
	out.Write(raw[offset:])
	return out.String()
}

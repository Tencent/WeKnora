package confluence

import (
	"strings"
	"testing"
)

// TestConvertConfluenceHTML verifies that the HTML->Markdown conversion used by
// the Confluence connector preserves content that the previous bare
// htmltomd.ConvertString dropped: tables, strikethrough rich text, inline SVG
// (drawio / rendered mermaid) and mermaid source, and that relative links are
// absolutized to the Confluence host.
func TestConvertConfluenceHTML(t *testing.T) {
	const host = "confluence.example.com"
	html := `
<h2>Overview</h2>
<p>Status: <s>draft</s> <strong>ready</strong> with a <a href="/spaces/X/pages/999">child page</a>.</p>
<table>
  <thead><tr><th>Name</th><th>Role</th></tr></thead>
  <tbody>
    <tr><td>Alice</td><td>Engineer</td></tr>
    <tr><td>Bob</td><td>Designer</td></tr>
  </tbody>
</table>
<p>drawio:</p>
<svg viewBox="0 0 10 10"><rect width="10" height="10"/></svg>
<p>mermaid:</p>
<pre class="mermaid">graph TD; A--&gt;B;</pre>
`
	out, err := convertConfluenceHTML(host, html)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	checks := map[string]string{
		"table rendered":         "| Name  | Role     |",
		"strikethrough kept":     "~~draft~~",
		"inline svg kept":        "<svg",
		"mermaid fenced block":   "```mermaid",
		"relative link resolved": "http://" + host + "/spaces/X/pages/999",
	}
	for name, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("%s: expected output to contain %q\n--- output ---\n%s", name, want, out)
		}
	}

	// Regression: the old converter flattened table cells into one line and
	// dropped strikethrough/svg. Make sure those regressions are gone.
	if strings.Contains(out, "NameRole") {
		t.Errorf("table was flattened: %s", out)
	}
}

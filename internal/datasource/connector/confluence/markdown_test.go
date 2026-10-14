package confluence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(raw)
}

// TestConvertConfluenceHTML verifies that the HTML->Markdown conversion used by
// the Confluence connector preserves content that the previous bare
// htmltomd.ConvertString dropped: tables, strikethrough rich text and mermaid
// source, and that relative links are absolutized to the Confluence base URL.
func TestConvertConfluenceHTML(t *testing.T) {
	domain := "https://confluence.example.com/wiki"
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
<p>mermaid:</p>
<pre class="mermaid">graph TD; A--&gt;B;</pre>
`
	out, err := convertConfluenceHTML(domain, html)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	checks := map[string]string{
		"table rendered":         "| Name  | Role     |",
		"strikethrough kept":     "~~draft~~",
		"mermaid fenced block":   "```mermaid",
		"relative link resolved": "https://confluence.example.com/spaces/X/pages/999",
	}
	for name, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("%s: expected output to contain %q\n--- output ---\n%s", name, want, out)
		}
	}

	// Regression: the old converter flattened table cells into one line and
	// dropped strikethrough. Make sure those regressions are gone.
	if strings.Contains(out, "NameRole") {
		t.Errorf("table was flattened: %s", out)
	}
}

// TestConvertConfluenceCodeBlocksFromView verifies that languages declared the
// way Confluence's `body.view` actually exposes them (SyntaxHighlighter
// data-syntaxhighlighter-params, legacy `brush:` classes and mermaid) are
// rendered as fenced code blocks with the correct language, reusing the
// commonmark renderer (so embedded fences and blank lines are preserved).
func TestConvertConfluenceCodeBlocksFromView(t *testing.T) {
	html := loadFixture(t, "confluence_code_blocks.html")
	out, err := convertConfluenceHTML("https://c.example.com/wiki", html)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if !strings.Contains(out, "```java\npublic class Hello") {
		t.Errorf("java code block missing/incorrect:\n%s", out)
	}
	// The python sample contains a literal triple-backtick; the fence must not
	// be closed early (i.e. a 4+ backtick fence is used).
	if !strings.Contains(out, "````") || !strings.Contains(out, "```nested fence```") {
		t.Errorf("embedded closing fence not protected:\n%s", out)
	}
	if !strings.Contains(out, "```mermaid\ngraph TD; A-->B;") {
		t.Errorf("mermaid source not preserved:\n%s", out)
	}
}

// TestConvertConfluencePanelsFromView verifies info/warning/tip panels from real
// Confluence editions (Cloud uses confluence-information-macro-*, Server uses
// panel* classes) become labeled blockquotes while preserving inner formatting.
func TestConvertConfluencePanelsFromView(t *testing.T) {
	// Each fixture is checked against the labels it actually contains.
	cases := map[string][]string{
		"confluence_panel_cloud.html":  {"Info", "Warning", "Tip"},
		"confluence_panel_server.html": {"Note", "Error"},
	}
	for fixture, labels := range cases {
		html := loadFixture(t, fixture)
		out, err := convertConfluenceHTML("https://c.example.com/wiki", html)
		if err != nil {
			t.Fatalf("convert %s: %v", fixture, err)
		}
		for _, label := range labels {
			want := "> **" + label + "**"
			if !strings.Contains(out, want) {
				t.Errorf("%s: expected panel label %q:\n%s", fixture, want, out)
			}
		}
		// Inner formatting must survive.
		if !strings.Contains(out, "> Remember to **save**") && !strings.Contains(out, "> Server edition note") {
			t.Errorf("%s: panel inner formatting lost:\n%s", fixture, out)
		}
	}
}

// TestConvertConfluenceInlineMacros verifies status lozenges and user mentions.
func TestConvertConfluenceInlineMacros(t *testing.T) {
	html := `
<p>State: <span class="status-macro aui-lozenge-success">Approved</span></p>
<p>Assigned to <a href="/display/~u1" class="user-mention user-hover">Ren Jianjun</a>.</p>`
	out, err := convertConfluenceHTML("https://c.example.com/wiki", html)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(out, "[Approved]") {
		t.Errorf("status lozenge lost:\n%s", out)
	}
	if !strings.Contains(out, "@Ren Jianjun") {
		t.Errorf("user mention lost:\n%s", out)
	}
	// The mention must not leak a profile link.
	if strings.Contains(out, "/display/~u1") {
		t.Errorf("user mention leaked a link:\n%s", out)
	}
}

// TestConvertConfluenceLinkScheme verifies the configured scheme (https) is
// preserved when absolutizing relative links — the old code forced http://.
func TestConvertConfluenceLinkScheme(t *testing.T) {
	html := `<p>See <a href="/display/SPACE/Home">home</a>.</p>`
	out, err := convertConfluenceHTML("https://confluence.example.com/wiki", html)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(out, "https://confluence.example.com/display/SPACE/Home") {
		t.Errorf("relative link not absolutized with https:\n%s", out)
	}
}

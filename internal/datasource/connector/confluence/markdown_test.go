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

// TestConvertConfluenceCodeBlock verifies fenced code blocks keep their language
// (Confluence uses class="brush:java" or a nested <code class="language-x">).
func TestConvertConfluenceCodeBlock(t *testing.T) {
	html := `<pre class="brush:java; gutter:false">public class A {}</pre>` +
		`<div class="code panel"><div class="codeContent"><pre><code class="language-python">print(1)</code></pre></div></div>`
	out, err := convertConfluenceHTML("https://c.example.com/wiki", html)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(out, "```java\npublic class A {}\n```") {
		t.Errorf("java code block missing/incorrect:\n%s", out)
	}
	if !strings.Contains(out, "```python\nprint(1)\n```") {
		t.Errorf("python code block missing/incorrect:\n%s", out)
	}
}

// TestConvertConfluencePanels verifies info/warning/note/tip/error panels become
// labeled blockquotes while preserving inner formatting.
func TestConvertConfluencePanels(t *testing.T) {
	html := `
<div class="panel panelNote">
  <div class="panelContent"><p>Remember to <strong>save</strong> often.</p></div>
</div>
<div class="panel panelWarning">
  <div class="panelContent"><ul><li>do not delete</li></ul></div>
</div>`
	out, err := convertConfluenceHTML("https://c.example.com/wiki", html)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(out, "> **Note**") || !strings.Contains(out, "> **Warning**") {
		t.Errorf("panel labels missing:\n%s", out)
	}
	if !strings.Contains(out, "> Remember to **save** often.") {
		t.Errorf("panel inner formatting lost:\n%s", out)
	}
	if !strings.Contains(out, "> - do not delete") {
		t.Errorf("panel list lost:\n%s", out)
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

package docparser

import (
	"strings"
	"testing"
)

func TestNormalizeHTMLTables_PreservesAttributeLikeCellText(t *testing.T) {
	input := `<table><tr><td colspan="2" class="layout">Set class=important and width=80; keep style="color:red".</td></tr></table>`
	got := NormalizeHTMLTables(input)
	want := `Set class=important and width=80; keep style="color:red".`
	if !strings.Contains(got, want) {
		t.Fatalf("cell text was changed: %q", got)
	}
	if strings.Contains(got, `class="layout"`) {
		t.Fatalf("layout attribute retained: %q", got)
	}
	if !strings.Contains(got, `colspan="2"`) {
		t.Fatalf("structural attribute lost: %q", got)
	}
}

func TestStripHTMLLayoutAttrs_PreservesSemanticAttributeValues(t *testing.T) {
	input := `<td colspan="2" title='example class=important' style="color:red">A &amp; B<!-- class=example --></td>`
	got := stripHTMLLayoutAttrs(input)
	for _, want := range []string{`title="example class=important"`, `colspan="2"`, `A &amp; B`, `<!-- class=example -->`} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q in %q", want, got)
		}
	}
	if strings.Contains(got, `style=`) {
		t.Errorf("layout attribute retained: %q", got)
	}
}

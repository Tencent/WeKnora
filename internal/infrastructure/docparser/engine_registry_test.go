package docparser

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestListAllEnginesBuiltinIncludesDocumentFormats(t *testing.T) {
	engines := ListAllEngines(true, nil, nil)
	for _, engine := range engines {
		if engine.Name != "builtin" {
			continue
		}
		if !engine.Available {
			t.Fatalf("builtin engine is unavailable: %s", engine.UnavailableReason)
		}

		fileTypes := make(map[string]bool, len(engine.FileTypes))
		for _, fileType := range engine.FileTypes {
			fileTypes[fileType] = true
		}
		for _, want := range []string{"html", "htm", "xmind", "ppt", "pptx"} {
			if !fileTypes[want] {
				t.Errorf("builtin engine file types do not include %q: %v", want, engine.FileTypes)
			}
		}
		return
	}

	t.Fatal("builtin engine not found")
}

func TestDefaultParserEnginePrefersAnydocWhenLinked(t *testing.T) {
	cases := []string{"pptx", "ppt", "docx"}
	if anydoc.Available() {
		for _, ft := range cases {
			if got := types.DefaultParserEngine(ft); got != AnydocEngineName {
				t.Errorf("DefaultParserEngine(%s) = %q, want anydoc when the binding is linked", ft, got)
			}
		}
		if got := types.DefaultParserEngine("csv"); got != "" {
			t.Errorf("DefaultParserEngine(csv) = %q, want empty so the Go simple reader stays default", got)
		}
		// anydoc claims pdf but cannot model one, so linking the binding must
		// not move PDFs off builtin. This branch is where that is observable.
		for _, ft := range []string{"pdf", ".PDF"} {
			if got := types.DefaultParserEngine(ft); got != "" {
				t.Errorf("DefaultParserEngine(%q) = %q, want empty so PDFs stay on builtin", ft, got)
			}
		}
		return
	}
	if got := types.DefaultParserEngine("pptx"); got != "markitdown" {
		t.Fatalf("DefaultParserEngine(pptx) = %q, want markitdown when anydoc is unavailable", got)
	}
	if got := types.DefaultParserEngine("docx"); got != "" {
		t.Fatalf("DefaultParserEngine(docx) = %q, want empty when anydoc is unavailable", got)
	}
}

// The PDF exclusion only matters because anydoc still claims the type. If a
// future anydoc drops pdf, the exclusion becomes dead code and should go with
// it — this guard runs in every build, tagged or not.
func TestPreferAnydocSkipsPDF(t *testing.T) {
	if !anydoc.Supports("pdf", "") {
		t.Fatal("anydoc no longer claims pdf; drop the explicit exclusion in preferAnydocWhenAvailable")
	}
	for _, ft := range []string{"pdf", ".PDF", " Pdf "} {
		if got := preferAnydocWhenAvailable(ft); got != "" {
			t.Errorf("preferAnydocWhenAvailable(%q) = %q, want empty so PDFs stay on builtin", ft, got)
		}
	}
}

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
	if types.DefaultParserEngine("pdf") != "" {
		t.Fatalf("DefaultParserEngine(pdf) should stay empty so builtin routing is unchanged")
	}
	got := types.DefaultParserEngine("pptx")
	if anydoc.Available() {
		if got != AnydocEngineName {
			t.Fatalf("DefaultParserEngine(pptx) = %q, want anydoc when the binding is linked", got)
		}
		if types.DefaultParserEngine("ppt") != AnydocEngineName {
			t.Fatalf("DefaultParserEngine(ppt) = %q, want anydoc", types.DefaultParserEngine("ppt"))
		}
		return
	}
	if got != "markitdown" {
		t.Fatalf("DefaultParserEngine(pptx) = %q, want markitdown when anydoc is unavailable", got)
	}
}

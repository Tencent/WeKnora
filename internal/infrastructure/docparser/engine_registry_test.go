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
	cases := []string{"pptx", "ppt", "pdf", "docx"}
	if anydoc.Available() {
		for _, ft := range cases {
			if got := types.DefaultParserEngine(ft); got != AnydocEngineName {
				t.Errorf("DefaultParserEngine(%s) = %q, want anydoc when the binding is linked", ft, got)
			}
		}
		if got := types.DefaultParserEngine("csv"); got != "" {
			t.Errorf("DefaultParserEngine(csv) = %q, want empty so the Go simple reader stays default", got)
		}
		return
	}
	if got := types.DefaultParserEngine("pptx"); got != "markitdown" {
		t.Fatalf("DefaultParserEngine(pptx) = %q, want markitdown when anydoc is unavailable", got)
	}
	if got := types.DefaultParserEngine("pdf"); got != "" {
		t.Fatalf("DefaultParserEngine(pdf) = %q, want empty when anydoc is unavailable", got)
	}
	if got := types.DefaultParserEngine("docx"); got != "" {
		t.Fatalf("DefaultParserEngine(docx) = %q, want empty when anydoc is unavailable", got)
	}
}

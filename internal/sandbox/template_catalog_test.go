package sandbox

import "testing"

func TestIsStandardTemplateRecognizesProviderScopedName(t *testing.T) {
	for _, name := range []string{"weknora", "team/weknora", "project-b89e/WeKnora"} {
		if !isStandardTemplate(name) {
			t.Fatalf("expected %q to identify the WeKnora standard template", name)
		}
	}
	if isStandardTemplate("weknora-custom") {
		t.Fatal("custom template must not be treated as the standard template")
	}
}

package modelcontext

import (
	"strings"
	"testing"
)

func TestAnnotateOmittedResults(t *testing.T) {
	t.Parallel()
	base := "<retrieval mode=\"hybrid\">\n</retrieval>"
	got := annotateOmittedResults(base, map[string]interface{}{"omitted_for_budget": 4})
	if !strings.Contains(got, `<omitted count="4" reason="output_budget">`) || !strings.HasSuffix(got, "</retrieval>") {
		t.Fatalf("annotated = %q", got)
	}
	if annotateOmittedResults(base, map[string]interface{}{}) != base {
		t.Fatal("nothing omitted must leave the output unchanged")
	}
}

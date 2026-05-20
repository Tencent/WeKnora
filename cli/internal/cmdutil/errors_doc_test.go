package cmdutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAllCodes_DocumentedInAGENTS verifies every typed code returned by
// AllCodes() surfaces in cli/AGENTS.md "Error code reference" section
// (delimited by ERROR_REFERENCE_START/END markers).
//
// Prevents drift: a contributor adding a new ErrorCode without updating
// the doc fails this test, forcing the doc to stay current.
func TestAllCodes_DocumentedInAGENTS(t *testing.T) {
	// From cli/internal/cmdutil/, go up two levels to find cli/AGENTS.md.
	docPath, err := filepath.Abs("../../AGENTS.md")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	doc := string(content)

	const startMarker = "<!-- ERROR_REFERENCE_START -->"
	const endMarker = "<!-- ERROR_REFERENCE_END -->"
	startIdx := strings.Index(doc, startMarker)
	endIdx := strings.Index(doc, endMarker)
	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		t.Fatalf("error-reference markers missing or malformed in %s:\n  start=%d end=%d", docPath, startIdx, endIdx)
	}
	refSection := doc[startIdx:endIdx]

	missing := []string{}
	for _, c := range AllCodes() {
		needle := "`" + string(c) + "`"
		if !strings.Contains(refSection, needle) {
			missing = append(missing, string(c))
		}
	}
	if len(missing) > 0 {
		t.Errorf("the following error codes are registered in AllCodes() but not listed in cli/AGENTS.md \"Error code reference\" section between the ERROR_REFERENCE markers:\n  - %s\n\nAdd a row for each missing code to keep agent-facing docs in sync.",
			strings.Join(missing, "\n  - "))
	}
}

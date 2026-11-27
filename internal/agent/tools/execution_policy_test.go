package tools

import "testing"

// The allowlist in CanRunConcurrently is the only thing standing between a
// read-only tool and the parallel-execution path: if an entry is dropped,
// the tool silently runs sequentially and nobody notices. Enumerate every
// allowed tool so a deletion fails loudly here.
func TestCanRunConcurrently(t *testing.T) {
	allowed := []string{
		ToolSearchKnowledge,
		ToolReadDocument,
		ToolListDocuments,
		ToolQueryKnowledgeGraph,
		ToolSearchConversations,
		ToolSearchMemory,
		ToolDataSchema,
		ToolWebSearch,
		ToolWebFetch,
		ToolReadFile,
		ToolListSandboxFiles,
		ToolWikiSearch,
		ToolWikiReadPage,
		ToolWikiReadIssue,
	}
	if len(allowed) != 14 {
		t.Fatalf("allowlist has %d entries, want 14; update this test when the policy changes", len(allowed))
	}
	for _, name := range allowed {
		if !CanRunConcurrently(name) {
			t.Errorf("CanRunConcurrently(%q) = false, want true", name)
		}
	}

	// Mutations, arbitrary code and unknown tools are execution barriers.
	barriers := []string{
		ToolShellExec,
		ToolShellExecAsync,
		ToolWorkWait,
		ToolWorkCancel,
		ToolWriteSandboxFile,
		ToolWriteSkillFile,
		"not_a_tool",
		"",
	}
	for _, name := range barriers {
		if CanRunConcurrently(name) {
			t.Errorf("CanRunConcurrently(%q) = true, want false", name)
		}
	}
}

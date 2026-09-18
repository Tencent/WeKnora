package tools

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeAllowedToolsMapsRetiredRetrievalTools(t *testing.T) {
	got := NormalizeAllowedTools([]string{
		ToolThinking,
		LegacyToolKnowledgeSearch,
		LegacyToolGrepChunks,
		LegacyToolListKnowledgeChunks,
		LegacyToolGetDocumentInfo,
		ToolWikiSearch,
		LegacyToolWikiReadSourceDoc,
		ToolSearchKnowledge, // duplicate after mapping
		"",
	})
	want := []string{ToolThinking, ToolSearchKnowledge, ToolReadDocument, ToolWikiSearch}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeAllowedTools() = %v, want %v", got, want)
	}
}

func TestNormalizeAllowedToolsKeepsUnknownNamesAndOrder(t *testing.T) {
	in := []string{"mcp_custom_tool", ToolReadDocument, ToolWebSearch}
	got := NormalizeAllowedTools(in)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("NormalizeAllowedTools() = %v, want unchanged %v", got, in)
	}
	if NormalizeAllowedTools(nil) != nil {
		t.Fatal("nil allowlist must stay nil")
	}
}

func TestSuccessorToolName(t *testing.T) {
	cases := map[string]string{
		LegacyToolKnowledgeSearch:     ToolSearchKnowledge,
		LegacyToolGrepChunks:          ToolSearchKnowledge,
		LegacyToolListKnowledgeChunks: ToolReadDocument,
		LegacyToolGetDocumentInfo:     ToolReadDocument,
		LegacyToolWikiReadSourceDoc:   ToolReadDocument,
		ToolSearchKnowledge:           ToolSearchKnowledge,
		ToolWikiReadPage:              ToolWikiReadPage,
	}
	for in, want := range cases {
		if got := SuccessorToolName(in); got != want {
			t.Errorf("SuccessorToolName(%q) = %q, want %q", in, got, want)
		}
	}
	if !IsLegacyRetrievalTool(LegacyToolGrepChunks) || IsLegacyRetrievalTool(ToolSearchKnowledge) {
		t.Fatal("IsLegacyRetrievalTool must flag only retired names")
	}
}

func TestRetiredRetrievalToolsExplainTheirReplacement(t *testing.T) {
	for name, want := range map[string]string{
		LegacyToolKnowledgeSearch:     ToolSearchKnowledge,
		LegacyToolGrepChunks:          "mode=\"keyword\"",
		LegacyToolListKnowledgeChunks: ToolReadDocument,
		LegacyToolGetDocumentInfo:     ToolReadDocument,
		LegacyToolWikiReadSourceDoc:   ToolReadDocument,
	} {
		msg := RetiredToolReplacement(name)
		if !strings.Contains(msg, want) {
			t.Errorf("RetiredToolReplacement(%q) = %q, want mention of %q", name, msg, want)
		}
	}
	if RetiredToolReplacement(ToolSearchKnowledge) != "" {
		t.Fatal("active tools have no retirement message")
	}
}

func TestDefaultAllowedToolsUseTheConsolidatedSurface(t *testing.T) {
	defaults := DefaultAllowedTools()
	for _, name := range []string{ToolSearchKnowledge, ToolReadDocument, ToolListDocuments} {
		found := false
		for _, d := range defaults {
			if d == name {
				found = true
			}
		}
		if !found {
			t.Errorf("default allowlist is missing %s: %v", name, defaults)
		}
	}
	for _, d := range defaults {
		if IsLegacyRetrievalTool(d) {
			t.Errorf("default allowlist still names retired tool %s", d)
		}
	}
	for _, def := range AvailableToolDefinitions() {
		if IsLegacyRetrievalTool(def.Name) {
			t.Errorf("UI tool list still exposes retired tool %s", def.Name)
		}
	}
}

package tools

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/modelcontext"
)

// Every built-in tool must declare a model-handle policy. Without an entry,
// modelcontext treats the tool's output like dynamic MCP output: durable chunk
// IDs, knowledge IDs and KB IDs are passed through to the model verbatim,
// which the source protocol prompt explicitly forbids. An empty policy is a
// valid answer ("compact known IDs, render nothing structured"); silence is
// not, because a newly added tool would opt out by accident.
func TestEveryBuiltInToolDeclaresAModelHandlePolicy(t *testing.T) {
	builtIns := []string{
		ToolThinking,
		ToolTodoWrite,
		ToolGrepChunks,
		ToolKnowledgeSearch,
		ToolListKnowledgeChunks,
		ToolQueryKnowledgeGraph,
		ToolGetDocumentInfo,
		ToolSearchConversations,
		ToolSearchMemory,
		ToolDatabaseQuery,
		ToolDataAnalysis,
		ToolDataSchema,
		ToolWebSearch,
		ToolWebFetch,
		ToolExecuteSkillScript,
		ToolReadSkill,
		ToolWikiReadPage,
		ToolWikiWritePage,
		ToolWikiReplaceText,
		ToolWikiRenamePage,
		ToolWikiDeletePage,
		ToolWikiSearch,
		ToolWikiReadSourceDoc,
		ToolWikiFlagIssue,
		ToolWikiReadIssue,
		ToolWikiUpdateIssue,
	}
	for _, name := range builtIns {
		if !modelcontext.HasToolPolicy(name) {
			t.Errorf("built-in tool %q has no entry in modelcontext.toolHandlePolicies", name)
		}
	}
}

// AvailableToolDefinitions drives the settings UI, so anything listed there is
// a built-in the Agent can register and must also carry a policy.
func TestAvailableToolDefinitionsDeclareModelHandlePolicies(t *testing.T) {
	for _, definition := range AvailableToolDefinitions() {
		if !modelcontext.HasToolPolicy(definition.Name) {
			t.Errorf("tool %q is exposed to the UI but has no model-handle policy", definition.Name)
		}
	}
}

func TestWikiSourcePreviewIsAnAllowedCapabilityButNotAnLLMTool(t *testing.T) {
	if !WikiSourcePreviewEnabled([]string{"wiki_read_page", ToolWikiSourcePreview}) {
		t.Fatalf("allowlist containing %q must enable source preview", ToolWikiSourcePreview)
	}
	if WikiSourcePreviewEnabled([]string{"wiki_read_page"}) {
		t.Fatalf("allowlist without %q must disable source preview", ToolWikiSourcePreview)
	}
	for _, definition := range AvailableToolDefinitions() {
		if definition.Name == ToolWikiSourcePreview {
			t.Fatalf("%q is a persisted UI capability and must not be exposed as a callable tool", ToolWikiSourcePreview)
		}
	}
	if req, ok := ToolCapabilityRequirements[ToolWikiSourcePreview]; !ok || len(req.AllOf) != 1 || req.AllOf[0] != CapWiki {
		t.Fatalf("%q must require a Wiki-capable knowledge base", ToolWikiSourcePreview)
	}
}

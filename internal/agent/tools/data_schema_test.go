package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDataSchemaKnowledgeIDDescriptionPointsToRuntimeContext(t *testing.T) {
	assertKnowledgeIDDescriptionPointsToRuntimeContext(t, dataSchemaTool.Parameters())
}

func TestDataAnalysisKnowledgeIDDescriptionPointsToRuntimeContext(t *testing.T) {
	assertKnowledgeIDDescriptionPointsToRuntimeContext(t, dataAnalysisTool.Parameters())
}

func assertKnowledgeIDDescriptionPointsToRuntimeContext(t *testing.T, parameters []byte) {
	t.Helper()

	var schema map[string]any
	if err := json.Unmarshal(parameters, &schema); err != nil {
		t.Fatalf("unmarshal tool parameters: %v", err)
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool parameters missing properties")
	}
	knowledgeID, ok := properties["knowledge_id"].(map[string]any)
	if !ok {
		t.Fatalf("tool parameters missing knowledge_id")
	}
	description, _ := knowledgeID["description"].(string)

	for _, want := range []string{"exact", "runtime_context", "recent_documents", "pinned_documents"} {
		if !strings.Contains(description, want) {
			t.Fatalf("knowledge_id description should mention %q, got %q", want, description)
		}
	}
}

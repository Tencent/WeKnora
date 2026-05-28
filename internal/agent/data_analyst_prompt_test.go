package agent

import (
	"os"
	"strings"
	"testing"
)

func TestDataAnalystPromptRequiresExactKnowledgeID(t *testing.T) {
	content, err := os.ReadFile("../../config/prompt_templates/agent_system_prompt.yaml")
	if err != nil {
		t.Fatalf("read agent system prompt templates: %v", err)
	}

	prompt := string(content)
	for _, want := range []string{
		"Use the exact `knowledge_id`",
		"`<runtime_context>`",
		"Do NOT invent table aliases",
		"`customer_data`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("data analyst prompt should mention %q", want)
		}
	}
}

package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseGeneratedSuggestionsFiltersAndDeduplicates(t *testing.T) {
	content := "```json\n{\"questions\":[" +
		"{\"text\":\"如何继续实施？\",\"category\":\"action\"}," +
		"{\"text\":\"如何继续实施?\",\"category\":\"action\"}," +
		"{\"text\":\"有哪些风险？\",\"category\":\"unknown\"}" +
		"]}\n```"
	items, err := parseGeneratedSuggestions(content, []string{"clarify", "action"}, 3)
	if err != nil {
		t.Fatalf("parseGeneratedSuggestions() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].Category != "action" {
		t.Fatalf("first category = %q, want action", items[0].Category)
	}
	if items[1].Category != "" {
		t.Fatalf("disallowed category = %q, want empty", items[1].Category)
	}
	for _, item := range items {
		if item.ID == "" || item.Source != "model" {
			t.Fatalf("item attribution fields are incomplete: %#v", item)
		}
	}
}

func TestMergeSuggestionItemsPreservesPriorityAndLimit(t *testing.T) {
	primary := types.SuggestionItems{{ID: "1", Text: "A?", Source: "model"}}
	fallback := types.SuggestionItems{
		{ID: "2", Text: "A？", Source: "faq"},
		{ID: "3", Text: "B?", Source: "faq"},
		{ID: "4", Text: "C?", Source: "faq"},
	}
	got := mergeSuggestionItems(primary, fallback, 2)
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "3" {
		t.Fatalf("mergeSuggestionItems() = %#v", got)
	}
}

func TestAnswerEndsWithQuestion(t *testing.T) {
	if !answerEndsWithQuestion("请补充具体时间？  ") {
		t.Fatal("Chinese question ending was not detected")
	}
	if answerEndsWithQuestion("结论已经给出。") {
		t.Fatal("statement was incorrectly detected as question")
	}
	if !answerEndsWithQuestion("需要我继续展开吗？\n<kb>1</kb>") {
		t.Fatal("question before a trailing citation was not detected")
	}
}

func TestBuildSuggestionGenerationContextUsesCompleteTurnsWithoutRawRAGContent(t *testing.T) {
	messages := []*types.Message{
		{ID: "u-old", RequestID: "old", Role: "user", Content: "old question"},
		{ID: "a-old", RequestID: "old", Role: "assistant", Content: "old answer", IsCompleted: true},
		{
			ID: "u-prev", RequestID: "prev", Role: "user",
			Content: "previous question", RenderedContent: "SECRET RAW RAG CONTEXT",
		},
		{
			ID: "a-prev", RequestID: "prev", Role: "assistant",
			Content: "<think>hidden</think>previous answer", IsCompleted: true,
		},
		{ID: "u-incomplete", RequestID: "incomplete", Role: "user", Content: "incomplete question"},
		{
			ID: "u-current", RequestID: "current", Role: "user",
			Content: "current question", RenderedContent: "CURRENT RAW RAG CONTEXT",
		},
		{ID: "a-current", RequestID: "current", Role: "assistant", Content: "current answer", IsCompleted: true},
	}
	current := messages[len(messages)-1]

	context := buildSuggestionGenerationContext(messages, current, 2)

	if context.CurrentQuery != "current question" {
		t.Fatalf("CurrentQuery = %q, want current question", context.CurrentQuery)
	}
	if !strings.Contains(context.History, "previous question") || !strings.Contains(context.History, "previous answer") {
		t.Fatalf("History does not contain the latest complete previous turn: %q", context.History)
	}
	for _, excluded := range []string{
		"old question",
		"incomplete question",
		"current question",
		"current answer",
		"hidden",
		"RAW RAG CONTEXT",
	} {
		if strings.Contains(context.History, excluded) {
			t.Fatalf("History unexpectedly contains %q: %q", excluded, context.History)
		}
	}
}

func TestBuildSuggestionGenerationContextOneTurnExcludesCurrentFromHistory(t *testing.T) {
	messages := []*types.Message{
		{ID: "u-current", RequestID: "current", Role: "user", Content: "current question"},
		{ID: "a-current", RequestID: "current", Role: "assistant", Content: "current answer", IsCompleted: true},
	}
	context := buildSuggestionGenerationContext(messages, messages[1], 1)
	if context.History != "" {
		t.Fatalf("History = %q, want empty when maxTurns includes only current turn", context.History)
	}
}

func TestBuildSuggestionEvidenceUsesTopReferencesAndDeduplicatesKnowledge(t *testing.T) {
	message := &types.Message{KnowledgeReferences: types.References{
		{ID: "low", Score: 0.2, KnowledgeID: "doc-low", KnowledgeTitle: "Low", Content: "low evidence"},
		{ID: "high", Score: 0.9, KnowledgeID: "doc-high", KnowledgeTitle: "High", Content: "high evidence"},
		{ID: "high-2", Score: 0.8, KnowledgeID: "doc-high", KnowledgeTitle: "High second", Content: "second chunk"},
	}}

	evidence, knowledgeIDs := buildSuggestionEvidence(message)
	if !strings.HasPrefix(evidence, "[1] High: high evidence") {
		t.Fatalf("Evidence was not score ordered: %q", evidence)
	}
	if len(knowledgeIDs) != 2 || knowledgeIDs[0] != "doc-high" || knowledgeIDs[1] != "doc-low" {
		t.Fatalf("knowledgeIDs = %#v, want score-ordered unique IDs", knowledgeIDs)
	}
}

func TestBuildSuggestionCapabilitiesDoesNotExposeResourceIDs(t *testing.T) {
	message := &types.Message{ExecutionContext: types.MessageExecutionContext{
		KnowledgeBaseIDs: []string{"kb-secret-id"},
		MCPServiceIDs:    []string{"mcp-secret-id"},
		SkillNames:       []string{"reporting"},
		WebSearchEnabled: true,
	}}
	capabilities := buildSuggestionCapabilities(message)
	for _, expected := range []string{"knowledge retrieval", "web search", "MCP tools", "reporting"} {
		if !strings.Contains(capabilities, expected) {
			t.Fatalf("Capabilities %q does not contain %q", capabilities, expected)
		}
	}
	for _, secret := range []string{"kb-secret-id", "mcp-secret-id"} {
		if strings.Contains(capabilities, secret) {
			t.Fatalf("Capabilities leaked resource ID %q: %q", secret, capabilities)
		}
	}
}

func TestRankKnowledgeSuggestionsPrioritizesCurrentTopic(t *testing.T) {
	candidates := []types.SuggestedQuestion{
		{Question: "How do I change the billing address?"},
		{Question: "How can I extend battery life while charging?"},
		{Question: "Where can I update my profile photo?"},
	}
	rankKnowledgeSuggestions(candidates, "The current answer explains battery charging and battery life.")
	if candidates[0].Question != "How can I extend battery life while charging?" {
		t.Fatalf("first candidate = %q, want battery-related question", candidates[0].Question)
	}
}

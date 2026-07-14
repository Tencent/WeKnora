package service

import (
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

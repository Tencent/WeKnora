package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type folderAwareKnowledgeSuggestionStub struct {
	interfaces.CustomAgentService
	called       bool
	folderScopes []types.FolderScope
}

func (s *folderAwareKnowledgeSuggestionStub) GetKnowledgeSuggestedQuestionsWithFolders(
	_ context.Context,
	_ string,
	_, _, _ []string,
	_ int,
	folderScopes []types.FolderScope,
) ([]types.SuggestedQuestion, error) {
	s.called = true
	s.folderScopes = append([]types.FolderScope(nil), folderScopes...)
	return []types.SuggestedQuestion{{Question: "Folder question?", Source: "document", KnowledgeBaseID: "kb-1"}}, nil
}

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

func TestGenerateFromKnowledgeUsesFolderAwarePath(t *testing.T) {
	stub := &folderAwareKnowledgeSuggestionStub{}
	service := &messageSuggestionService{customAgentService: stub}
	scopes := []types.FolderScope{{KnowledgeBaseID: "kb-1", FolderIDs: []string{"folder-1"}}}
	items, err := service.generateFromKnowledge(context.Background(), &types.Message{
		AgentID: "agent-1",
		ExecutionContext: types.MessageExecutionContext{
			KnowledgeBaseIDs: []string{"kb-1"},
			FolderScopes:     scopes,
		},
	}, 3)
	require.NoError(t, err)
	assert.True(t, stub.called)
	assert.Equal(t, scopes, stub.folderScopes)
	require.Len(t, items, 1)
	assert.Equal(t, "Folder question?", items[0].Text)
	assert.Equal(t, []string{"kb-1"}, items[0].KnowledgeBaseIDs)
}

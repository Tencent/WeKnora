package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCombineSuggestionScopedKnowledgeIDsIntersectsFolderAndTagPerKB(t *testing.T) {
	tags := resolvedSuggestionTagScopes{
		KnowledgeBaseIDs: []string{"kb-1"},
		KnowledgeIDsByKB: map[string][]string{"kb-1": {"doc-1", "doc-2"}},
	}
	folders := resolvedSuggestionFolderScopes{
		KnowledgeBaseIDs: []string{"kb-1"},
		KnowledgeIDsByKB: map[string][]string{"kb-1": {"doc-2", "doc-3"}},
	}

	got := combineSuggestionScopedKnowledgeIDs([]string{"explicit"}, tags, folders, nil)
	require.ElementsMatch(t, []string{"explicit", "doc-2"}, got)
}

func TestCombineSuggestionScopedKnowledgeIDsWholeKBOverridesFolder(t *testing.T) {
	folders := resolvedSuggestionFolderScopes{
		KnowledgeBaseIDs: []string{"kb-1"},
		KnowledgeIDsByKB: map[string][]string{"kb-1": {"doc-1"}},
	}

	got := combineSuggestionScopedKnowledgeIDs(nil, resolvedSuggestionTagScopes{}, folders, []string{"kb-1"})
	require.Empty(t, got)
}

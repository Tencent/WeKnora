package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectKnowledgeScopeToSearchTargetsPreservesRuntimeScope(t *testing.T) {
	t.Parallel()

	filter, err := NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	target, err := NewKnowledgeScopeTarget(
		"kb-1",
		42,
		[]string{"knowledge-2", "knowledge-1"},
		[]string{"tag-physical"},
		[]string{"tag-logical"},
		filter,
	)
	require.NoError(t, err)
	scope, err := NewKnowledgeScope([]KnowledgeScopeTarget{target})
	require.NoError(t, err)

	projected := ProjectKnowledgeScopeToSearchTargets(scope, "execution-hash")
	require.Len(t, projected, 1)
	got := projected[0]
	require.NotNil(t, got)
	assert.Equal(t, SearchTargetTypeKnowledge, got.Type)
	assert.Equal(t, "kb-1", got.KnowledgeBaseID)
	assert.Equal(t, uint64(42), got.TenantID)
	assert.Equal(t, uint64(42), got.SourceTenantID)
	assert.Equal(t, uint64(42), got.EffectiveSourceTenantID())
	assert.Equal(t, []string{"knowledge-1", "knowledge-2"}, got.KnowledgeIDs)
	assert.Equal(t, []string{"tag-physical"}, got.TagIDs)
	assert.Equal(t, []string{"tag-logical"}, got.ScopeTagIDs)
	assert.True(t, got.FolderFilter.Enabled())
	assert.Equal(
		t,
		[]string{"10000000-0000-4000-8000-000000000001"},
		got.FolderFilter.FolderIDs(),
	)
	assert.Equal(t, "execution-hash", got.ExecutionScopeHash)

	got.KnowledgeIDs[0] = "mutated-knowledge"
	got.TagIDs[0] = "mutated-physical-tag"
	got.ScopeTagIDs[0] = "mutated-logical-tag"

	reprojected := ProjectKnowledgeScopeToSearchTargets(scope, "execution-hash")
	require.Len(t, reprojected, 1)
	assert.Equal(
		t,
		[]string{"knowledge-1", "knowledge-2"},
		reprojected[0].KnowledgeIDs,
	)
	assert.Equal(t, []string{"tag-physical"}, reprojected[0].TagIDs)
	assert.Equal(t, []string{"tag-logical"}, reprojected[0].ScopeTagIDs)
}

func TestProjectKnowledgeScopeToSearchTargetsDropsFolderEnabledZeroKnowledgeTargets(
	t *testing.T,
) {
	t.Parallel()

	enabledEmpty, err := NewResolvedFolderFilter(true, nil)
	require.NoError(t, err)
	enabledNonEmpty, err := NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	legacy, err := NewKnowledgeScopeTarget(
		"kb-legacy",
		42,
		nil,
		nil,
		nil,
		ResolvedFolderFilter{},
	)
	require.NoError(t, err)
	empty, err := NewKnowledgeScopeTarget(
		"kb-enabled-empty",
		42,
		nil,
		nil,
		nil,
		enabledEmpty,
	)
	require.NoError(t, err)
	materializedZero, err := NewKnowledgeScopeTarget(
		"kb-materialized-zero",
		42,
		nil,
		nil,
		nil,
		enabledNonEmpty,
	)
	require.NoError(t, err)
	nonzero, err := NewKnowledgeScopeTarget(
		"kb-folder-nonzero",
		42,
		[]string{"knowledge-1"},
		nil,
		nil,
		enabledNonEmpty,
	)
	require.NoError(t, err)
	scope, err := NewKnowledgeScope([]KnowledgeScopeTarget{
		legacy,
		empty,
		materializedZero,
		nonzero,
	})
	require.NoError(t, err)

	projected := ProjectKnowledgeScopeToSearchTargets(scope, "execution-hash")

	require.Len(t, projected, 2)
	byKnowledgeBaseID := map[string]*SearchTarget{}
	for _, target := range projected {
		byKnowledgeBaseID[target.KnowledgeBaseID] = target
	}
	require.Contains(t, byKnowledgeBaseID, "kb-legacy")
	assert.Equal(
		t,
		SearchTargetTypeKnowledgeBase,
		byKnowledgeBaseID["kb-legacy"].Type,
	)
	require.Contains(t, byKnowledgeBaseID, "kb-folder-nonzero")
	assert.Equal(
		t,
		SearchTargetTypeKnowledge,
		byKnowledgeBaseID["kb-folder-nonzero"].Type,
	)
	assert.Equal(
		t,
		[]string{"knowledge-1"},
		byKnowledgeBaseID["kb-folder-nonzero"].KnowledgeIDs,
	)
	assert.NotContains(t, byKnowledgeBaseID, "kb-enabled-empty")
	assert.NotContains(t, byKnowledgeBaseID, "kb-materialized-zero")
}

func TestSearchTargetCloneDoesNotShareRuntimeSlices(t *testing.T) {
	t.Parallel()

	filter, err := NewResolvedFolderFilter(
		true,
		[]string{"10000000-0000-4000-8000-000000000001"},
	)
	require.NoError(t, err)
	original := &SearchTarget{
		Type:               SearchTargetTypeKnowledge,
		KnowledgeBaseID:    "kb-1",
		TenantID:           17,
		SourceTenantID:     29,
		KnowledgeIDs:       []string{"knowledge-1"},
		TagIDs:             []string{"tag-physical"},
		ScopeTagIDs:        []string{"tag-logical"},
		FolderFilter:       filter,
		ExecutionScopeHash: "execution-hash",
	}

	cloned := original.Clone()
	require.NotNil(t, cloned)
	cloned.KnowledgeIDs[0] = "mutated-knowledge"
	cloned.TagIDs[0] = "mutated-physical-tag"
	cloned.ScopeTagIDs[0] = "mutated-logical-tag"

	assert.Equal(t, []string{"knowledge-1"}, original.KnowledgeIDs)
	assert.Equal(t, []string{"tag-physical"}, original.TagIDs)
	assert.Equal(t, []string{"tag-logical"}, original.ScopeTagIDs)
	assert.Equal(t, uint64(29), cloned.SourceTenantID)
	assert.Equal(t, "execution-hash", cloned.ExecutionScopeHash)
	assert.Equal(
		t,
		[]string{"10000000-0000-4000-8000-000000000001"},
		cloned.FolderFilter.FolderIDs(),
	)
}

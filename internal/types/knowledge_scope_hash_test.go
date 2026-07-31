package types

import (
	"errors"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHashKnowledgeScopeStableAcrossTargetOrder(t *testing.T) {
	filter, err := NewResolvedFolderFilter(true, []string{scopeFolder1})
	require.NoError(t, err)
	first, err := NewKnowledgeScopeTarget(
		scopeKB1,
		1,
		[]string{"knowledge-1"},
		[]string{"tag-1"},
		[]string{"scope-tag-1"},
		filter,
	)
	require.NoError(t, err)
	second, err := NewKnowledgeScopeTarget(
		scopeKB2,
		2,
		[]string{"knowledge-2"},
		[]string{"tag-2"},
		[]string{"scope-tag-2"},
		filter,
	)
	require.NoError(t, err)

	left := &KnowledgeScope{targets: []KnowledgeScopeTarget{second, first}}
	right := &KnowledgeScope{targets: []KnowledgeScopeTarget{first, second}}

	leftHash, err := HashKnowledgeScope(left)
	require.NoError(t, err)
	rightHash, err := HashKnowledgeScope(right)
	require.NoError(t, err)
	require.Equal(t, leftHash, rightHash)
}

func TestHashKnowledgeScopeStableAcrossDuplicateInputOrder(t *testing.T) {
	left := &KnowledgeScope{targets: []KnowledgeScopeTarget{{
		knowledgeBaseID: scopeKB1,
		sourceTenantID:  1,
		knowledgeIDs:    []string{"knowledge-2", "knowledge-1", "knowledge-2"},
		tagIDs:          []string{"tag-2", "tag-1", "tag-2"},
		scopeTagIDs:     []string{"scope-tag-2", "scope-tag-1", "scope-tag-2"},
		folderFilter: ResolvedFolderFilter{
			enabled:   true,
			folderIDs: []string{scopeFolder2, scopeFolder1, scopeFolder2},
		},
	}}}
	right := &KnowledgeScope{targets: []KnowledgeScopeTarget{{
		knowledgeBaseID: scopeKB1,
		sourceTenantID:  1,
		knowledgeIDs:    []string{"knowledge-2", "knowledge-2", "knowledge-1"},
		tagIDs:          []string{"tag-1", "tag-2", "tag-2"},
		scopeTagIDs:     []string{"scope-tag-2", "scope-tag-2", "scope-tag-1"},
		folderFilter: ResolvedFolderFilter{
			enabled:   true,
			folderIDs: []string{scopeFolder1, scopeFolder2, scopeFolder2},
		},
	}}}

	leftHash, err := HashKnowledgeScope(left)
	require.NoError(t, err)
	rightHash, err := HashKnowledgeScope(right)
	require.NoError(t, err)
	require.Equal(t, leftHash, rightHash)
}

func TestHashKnowledgeScopeChangesWithSourceTenant(t *testing.T) {
	left := knowledgeScopeHashTestScope(t, 1, scopeKB1, nil, nil, nil, false, nil)
	right := knowledgeScopeHashTestScope(t, 2, scopeKB1, nil, nil, nil, false, nil)

	requireKnowledgeScopeHashesDiffer(t, left, right)
}

func TestHashKnowledgeScopeChangesWithKnowledgeBase(t *testing.T) {
	left := knowledgeScopeHashTestScope(t, 1, scopeKB1, nil, nil, nil, false, nil)
	right := knowledgeScopeHashTestScope(t, 1, scopeKB2, nil, nil, nil, false, nil)

	requireKnowledgeScopeHashesDiffer(t, left, right)
}

func TestHashKnowledgeScopeChangesWithFolderIDs(t *testing.T) {
	left := knowledgeScopeHashTestScope(
		t,
		1,
		scopeKB1,
		nil,
		nil,
		nil,
		true,
		[]string{scopeFolder1},
	)
	right := knowledgeScopeHashTestScope(
		t,
		1,
		scopeKB1,
		nil,
		nil,
		nil,
		true,
		[]string{scopeFolder2},
	)

	requireKnowledgeScopeHashesDiffer(t, left, right)
}

func TestHashKnowledgeScopeChangesWithKnowledgeIDs(t *testing.T) {
	left := knowledgeScopeHashTestScope(
		t,
		1,
		scopeKB1,
		[]string{"knowledge-1"},
		nil,
		nil,
		false,
		nil,
	)
	right := knowledgeScopeHashTestScope(
		t,
		1,
		scopeKB1,
		[]string{"knowledge-2"},
		nil,
		nil,
		false,
		nil,
	)

	requireKnowledgeScopeHashesDiffer(t, left, right)
}

func TestHashKnowledgeScopeChangesWithTags(t *testing.T) {
	t.Run("physical tags", func(t *testing.T) {
		left := knowledgeScopeHashTestScope(
			t,
			1,
			scopeKB1,
			nil,
			[]string{"tag-1"},
			nil,
			false,
			nil,
		)
		right := knowledgeScopeHashTestScope(
			t,
			1,
			scopeKB1,
			nil,
			[]string{"tag-2"},
			nil,
			false,
			nil,
		)

		requireKnowledgeScopeHashesDiffer(t, left, right)
	})

	t.Run("scope tags", func(t *testing.T) {
		left := knowledgeScopeHashTestScope(
			t,
			1,
			scopeKB1,
			nil,
			nil,
			[]string{"scope-tag-1"},
			false,
			nil,
		)
		right := knowledgeScopeHashTestScope(
			t,
			1,
			scopeKB1,
			nil,
			nil,
			[]string{"scope-tag-2"},
			false,
			nil,
		)

		requireKnowledgeScopeHashesDiffer(t, left, right)
	})
}

func TestHashKnowledgeScopeDistinguishesDisabledAndEnabledEmpty(t *testing.T) {
	disabled := knowledgeScopeHashTestScope(
		t,
		1,
		scopeKB1,
		nil,
		nil,
		nil,
		false,
		nil,
	)
	enabledEmpty := knowledgeScopeHashTestScope(
		t,
		1,
		scopeKB1,
		nil,
		nil,
		nil,
		true,
		nil,
	)

	requireKnowledgeScopeHashesDiffer(t, disabled, enabledEmpty)
}

func cloneKnowledgeScopeStringsPreservingNilForTest(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneKnowledgeScopePreservingNilForTest(scope *KnowledgeScope) *KnowledgeScope {
	if scope == nil {
		return nil
	}
	targets := make([]KnowledgeScopeTarget, len(scope.targets))
	for index, target := range scope.targets {
		targets[index] = KnowledgeScopeTarget{
			knowledgeBaseID: target.knowledgeBaseID,
			sourceTenantID:  target.sourceTenantID,
			knowledgeIDs: cloneKnowledgeScopeStringsPreservingNilForTest(
				target.knowledgeIDs,
			),
			tagIDs: cloneKnowledgeScopeStringsPreservingNilForTest(
				target.tagIDs,
			),
			scopeTagIDs: cloneKnowledgeScopeStringsPreservingNilForTest(
				target.scopeTagIDs,
			),
			folderFilter: ResolvedFolderFilter{
				enabled: target.folderFilter.enabled,
				folderIDs: cloneKnowledgeScopeStringsPreservingNilForTest(
					target.folderFilter.folderIDs,
				),
			},
		}
	}
	return &KnowledgeScope{targets: targets}
}

func TestHashKnowledgeScopeDoesNotMutateScope(t *testing.T) {
	scope := &KnowledgeScope{targets: []KnowledgeScopeTarget{
		{
			knowledgeBaseID: scopeKB2,
			sourceTenantID:  2,
			knowledgeIDs:    []string{"knowledge-2", "knowledge-1", "knowledge-2"},
			tagIDs:          []string{"tag-2", "tag-1", "tag-2"},
			scopeTagIDs:     []string{"scope-tag-2", "scope-tag-1", "scope-tag-2"},
			folderFilter: ResolvedFolderFilter{
				enabled:   true,
				folderIDs: []string{scopeFolder2, scopeFolder1, scopeFolder2},
			},
		},
		{
			knowledgeBaseID: scopeKB1,
			sourceTenantID:  1,
			folderFilter: ResolvedFolderFilter{
				folderIDs: []string{scopeFolder1},
			},
		},
	}}
	before := cloneKnowledgeScopePreservingNilForTest(scope)

	_, err := HashKnowledgeScope(scope)
	require.NoError(t, err)
	require.Equal(t, before, scope)
}

func TestHashKnowledgeScopeReturnsLowercaseSHA256(t *testing.T) {
	require.Equal(t, "knowledge_scope_execution/v1", ExecutionScopeHashVersion)
	scope := knowledgeScopeHashTestScope(
		t,
		1,
		scopeKB1,
		[]string{"knowledge-1"},
		[]string{"tag-1"},
		[]string{"scope-tag-1"},
		true,
		[]string{scopeFolder1},
	)

	hash, err := HashKnowledgeScope(scope)
	require.NoError(t, err)
	require.True(t, regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash), hash)
}

func TestHashKnowledgeScopeRejectsNil(t *testing.T) {
	hash, err := HashKnowledgeScope(nil)

	require.Empty(t, hash)
	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func knowledgeScopeHashTestScope(
	t *testing.T,
	sourceTenantID uint64,
	knowledgeBaseID string,
	knowledgeIDs []string,
	tagIDs []string,
	scopeTagIDs []string,
	folderEnabled bool,
	folderIDs []string,
) *KnowledgeScope {
	t.Helper()
	filter, err := NewResolvedFolderFilter(folderEnabled, folderIDs)
	require.NoError(t, err)
	target, err := NewKnowledgeScopeTarget(
		knowledgeBaseID,
		sourceTenantID,
		knowledgeIDs,
		tagIDs,
		scopeTagIDs,
		filter,
	)
	require.NoError(t, err)
	scope, err := NewKnowledgeScope([]KnowledgeScopeTarget{target})
	require.NoError(t, err)
	return scope
}

func requireKnowledgeScopeHashesDiffer(
	t *testing.T,
	left *KnowledgeScope,
	right *KnowledgeScope,
) {
	t.Helper()
	leftHash, err := HashKnowledgeScope(left)
	require.NoError(t, err)
	rightHash, err := HashKnowledgeScope(right)
	require.NoError(t, err)
	require.NotEqual(t, leftHash, rightHash)
}

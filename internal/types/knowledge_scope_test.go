package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	scopeKB1     = "kb-1"
	scopeKB2     = "kb-2"
	scopeFolder1 = "11111111-1111-4111-8111-111111111111"
	scopeFolder2 = "22222222-2222-4222-8222-222222222222"
	scopeFolder3 = "33333333-3333-4333-8333-333333333333"
	scopeFolder4 = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

func boolPointer(value bool) *bool {
	return &value
}

func TestNormalizeKnowledgeScopeRequestDoesNotMutateInput(t *testing.T) {
	folderScopes := []FolderScopeRequest{
		{
			KnowledgeBaseID:    scopeKB1,
			FolderIDs:          []string{scopeFolder2, scopeFolder1, scopeFolder2},
			IncludeDescendants: boolPointer(false),
		},
	}
	input := &KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{scopeKB2, scopeKB1, scopeKB2},
		KnowledgeIDs:     []string{"knowledge-2", "knowledge-1", "knowledge-2"},
		TagScopes: []TagScope{
			{KnowledgeBaseID: scopeKB1, TagIDs: []string{"tag-2", "tag-1", "tag-2"}},
		},
		FolderScopes: &folderScopes,
	}
	before, err := json.Marshal(input)
	require.NoError(t, err)

	normalized, err := NormalizeKnowledgeScopeRequest(input)
	require.NoError(t, err)
	after, err := json.Marshal(input)
	require.NoError(t, err)

	require.JSONEq(t, string(before), string(after))
	require.Equal(t, []string{scopeKB1, scopeKB2}, normalized.KnowledgeBaseIDs)
	require.Equal(t, []string{"knowledge-1", "knowledge-2"}, normalized.KnowledgeIDs)
	require.Equal(t, []string{"tag-1", "tag-2"}, normalized.TagScopes[0].TagIDs)
	require.Equal(t, []string{scopeFolder1, scopeFolder2}, (*normalized.FolderScopes)[0].FolderIDs)
}

func TestNormalizeKnowledgeScopeRequestRejectsEmptyTagScope(t *testing.T) {
	_, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{
		TagScopes: []TagScope{{
			KnowledgeBaseID: scopeKB1,
			TagIDs:          []string{},
		}},
	})

	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func TestNormalizeKnowledgeScopeRequestRejectsNilTagIDs(t *testing.T) {
	_, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{
		TagScopes: []TagScope{{
			KnowledgeBaseID: scopeKB1,
			TagIDs:          nil,
		}},
	})

	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func TestNormalizeKnowledgeScopeRequestRejectsEmptyTagIDElement(t *testing.T) {
	_, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{
		TagScopes: []TagScope{{
			KnowledgeBaseID: scopeKB1,
			TagIDs:          []string{"tag-1", ""},
		}},
	})

	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func TestNormalizeKnowledgeScopeRequestRejectsWhitespaceTagIDElement(t *testing.T) {
	tests := []struct {
		name  string
		tagID string
	}{
		{name: "whitespace only", tagID: "   "},
		{name: "surrounding whitespace", tagID: " tag-2 "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{
				TagScopes: []TagScope{{
					KnowledgeBaseID: scopeKB1,
					TagIDs:          []string{"tag-1", test.tagID},
				}},
			})

			require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
		})
	}
}

func TestNormalizeKnowledgeScopeRequestRejectsEmptyKnowledgeBaseIDElement(t *testing.T) {
	tests := []struct {
		name            string
		knowledgeBaseID string
	}{
		{name: "empty", knowledgeBaseID: ""},
		{name: "whitespace only", knowledgeBaseID: "   "},
		{name: "surrounding whitespace", knowledgeBaseID: " kb-2 "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{
				KnowledgeBaseIDs: []string{scopeKB1, test.knowledgeBaseID},
			})

			require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
		})
	}
}

func TestNormalizeKnowledgeScopeRequestRejectsWhitespaceKnowledgeIDElement(t *testing.T) {
	tests := []struct {
		name        string
		knowledgeID string
	}{
		{name: "empty", knowledgeID: ""},
		{name: "whitespace only", knowledgeID: "   "},
		{name: "surrounding whitespace", knowledgeID: " knowledge-2 "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{
				KnowledgeIDs: []string{"knowledge-1", test.knowledgeID},
			})

			require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
		})
	}
}

func TestKnowledgeScopeTargetRejectsEmptyKnowledgeIDElement(t *testing.T) {
	filter, err := NewResolvedFolderFilter(false, nil)
	require.NoError(t, err)

	tests := []struct {
		name        string
		knowledgeID string
	}{
		{name: "empty", knowledgeID: ""},
		{name: "whitespace only", knowledgeID: "   "},
		{name: "surrounding whitespace", knowledgeID: " knowledge-2 "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, targetErr := NewKnowledgeScopeTarget(
				scopeKB1,
				1,
				[]string{"knowledge-1", test.knowledgeID},
				nil,
				nil,
				filter,
			)

			require.True(t, errors.Is(targetErr, ErrInvalidKnowledgeScopeRequest))
		})
	}
}

func TestKnowledgeScopeTargetRejectsEmptyTagIDElement(t *testing.T) {
	filter, err := NewResolvedFolderFilter(false, nil)
	require.NoError(t, err)

	tests := []struct {
		name  string
		tagID string
	}{
		{name: "empty", tagID: ""},
		{name: "whitespace only", tagID: "   "},
		{name: "surrounding whitespace", tagID: " tag-2 "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, targetErr := NewKnowledgeScopeTarget(
				scopeKB1,
				1,
				nil,
				[]string{"tag-1", test.tagID},
				nil,
				filter,
			)

			require.True(t, errors.Is(targetErr, ErrInvalidKnowledgeScopeRequest))
		})
	}
}

func TestKnowledgeScopeTargetRejectsEmptyScopeTagIDElement(t *testing.T) {
	filter, err := NewResolvedFolderFilter(false, nil)
	require.NoError(t, err)

	tests := []struct {
		name       string
		scopeTagID string
	}{
		{name: "empty", scopeTagID: ""},
		{name: "whitespace only", scopeTagID: "   "},
		{name: "surrounding whitespace", scopeTagID: " scope-tag-2 "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, targetErr := NewKnowledgeScopeTarget(
				scopeKB1,
				1,
				nil,
				nil,
				[]string{"scope-tag-1", test.scopeTagID},
				filter,
			)

			require.True(t, errors.Is(targetErr, ErrInvalidKnowledgeScopeRequest))
		})
	}
}

func TestNormalizeKnowledgeScopeRequestRejectsWhitespaceRoot(t *testing.T) {
	folderScopes := []FolderScopeRequest{{
		KnowledgeBaseID: scopeKB1,
		FolderIDs:       []string{"   "},
	}}

	_, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{FolderScopes: &folderScopes})
	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func TestNormalizeKnowledgeScopeRequestAcceptsCanonicalFolderUUID(t *testing.T) {
	folderScopes := []FolderScopeRequest{{
		KnowledgeBaseID: scopeKB1,
		FolderIDs:       []string{scopeFolder4},
	}}

	normalized, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{FolderScopes: &folderScopes})
	require.NoError(t, err)
	require.Equal(t, []string{scopeFolder4}, (*normalized.FolderScopes)[0].FolderIDs)
}

func TestNormalizeKnowledgeScopeRequestRejectsUppercaseFolderUUID(t *testing.T) {
	folderScopes := []FolderScopeRequest{{
		KnowledgeBaseID: scopeKB1,
		FolderIDs:       []string{strings.ToUpper(scopeFolder4)},
	}}

	_, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{FolderScopes: &folderScopes})
	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func TestNormalizeKnowledgeScopeRequestRejectsNonCanonicalFolderUUID(t *testing.T) {
	folderScopes := []FolderScopeRequest{{
		KnowledgeBaseID: scopeKB1,
		FolderIDs:       []string{strings.ReplaceAll(scopeFolder1, "-", "")},
	}}

	_, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{FolderScopes: &folderScopes})
	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func TestNormalizeKnowledgeScopeRequestDoesNotRewriteFolderUUID(t *testing.T) {
	folderScopes := []FolderScopeRequest{{
		KnowledgeBaseID: scopeKB1,
		FolderIDs:       []string{scopeFolder4},
	}}

	normalized, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{FolderScopes: &folderScopes})
	require.NoError(t, err)
	require.Equal(t, scopeFolder4, (*normalized.FolderScopes)[0].FolderIDs[0])
}

func TestNormalizeKnowledgeScopeRequestDefaultsDescendantsToTrueSemantically(t *testing.T) {
	nilDefault := []FolderScopeRequest{{
		KnowledgeBaseID: scopeKB1,
		FolderIDs:       []string{scopeFolder1},
	}}
	explicitTrue := []FolderScopeRequest{{
		KnowledgeBaseID:    scopeKB1,
		FolderIDs:          []string{scopeFolder1},
		IncludeDescendants: boolPointer(true),
	}}

	equivalent, err := EquivalentKnowledgeScopeRequest(
		&KnowledgeScopeRequest{FolderScopes: &nilDefault},
		&KnowledgeScopeRequest{FolderScopes: &explicitTrue},
	)
	require.NoError(t, err)
	require.True(t, equivalent)
}

func TestNormalizeKnowledgeScopeRequestDeduplicatesAndSorts(t *testing.T) {
	input := &KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{scopeKB2, scopeKB1, scopeKB2},
		KnowledgeIDs:     []string{"knowledge-2", "knowledge-1", "knowledge-2"},
		TagScopes: []TagScope{
			{KnowledgeBaseID: scopeKB2, TagIDs: []string{"tag-3", "tag-2"}},
			{KnowledgeBaseID: scopeKB1, TagIDs: []string{"tag-2", "tag-1"}},
			{KnowledgeBaseID: scopeKB1, TagIDs: []string{"tag-1", "tag-3"}},
		},
	}

	normalized, err := NormalizeKnowledgeScopeRequest(input)
	require.NoError(t, err)
	require.Equal(t, []string{scopeKB1, scopeKB2}, normalized.KnowledgeBaseIDs)
	require.Equal(t, []string{"knowledge-1", "knowledge-2"}, normalized.KnowledgeIDs)
	require.Equal(t, []TagScope{
		{KnowledgeBaseID: scopeKB1, TagIDs: []string{"tag-1", "tag-2", "tag-3"}},
		{KnowledgeBaseID: scopeKB2, TagIDs: []string{"tag-2", "tag-3"}},
	}, normalized.TagScopes)
}

func TestNormalizeKnowledgeScopeRequestMergesSameKBSelectors(t *testing.T) {
	folderScopes := []FolderScopeRequest{
		{
			KnowledgeBaseID:    scopeKB1,
			FolderIDs:          []string{scopeFolder2, scopeFolder1},
			IncludeDescendants: boolPointer(false),
		},
		{
			KnowledgeBaseID:    scopeKB1,
			FolderIDs:          []string{scopeFolder3, scopeFolder2},
			IncludeDescendants: boolPointer(false),
		},
	}

	normalized, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{FolderScopes: &folderScopes})
	require.NoError(t, err)
	require.Len(t, *normalized.FolderScopes, 1)
	require.Equal(t, []string{scopeFolder1, scopeFolder2, scopeFolder3}, (*normalized.FolderScopes)[0].FolderIDs)
	require.NotNil(t, (*normalized.FolderScopes)[0].IncludeDescendants)
	require.False(t, *(*normalized.FolderScopes)[0].IncludeDescendants)
}

func TestNormalizeKnowledgeScopeRequestRootRecursiveDominatesRootDirect(t *testing.T) {
	folderScopes := []FolderScopeRequest{
		{
			KnowledgeBaseID: scopeKB1,
			FolderIDs:       []string{""},
		},
		{
			KnowledgeBaseID:    scopeKB1,
			FolderIDs:          []string{""},
			IncludeDescendants: boolPointer(false),
		},
		{
			KnowledgeBaseID:    scopeKB1,
			FolderIDs:          []string{""},
			IncludeDescendants: boolPointer(true),
		},
	}

	normalized, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{FolderScopes: &folderScopes})
	require.NoError(t, err)
	require.Equal(t, []FolderScopeRequest{{
		KnowledgeBaseID:    scopeKB1,
		FolderIDs:          []string{""},
		IncludeDescendants: boolPointer(true),
	}}, *normalized.FolderScopes)
}

func TestNormalizeKnowledgeScopeRequestRejectsRootRecursiveWithNonRootSelector(t *testing.T) {
	tests := []struct {
		name         string
		folderScopes []FolderScopeRequest
	}{
		{
			name: "direct non-root",
			folderScopes: []FolderScopeRequest{
				{
					KnowledgeBaseID: scopeKB1,
					FolderIDs:       []string{""},
				},
				{
					KnowledgeBaseID:    scopeKB1,
					FolderIDs:          []string{scopeFolder1},
					IncludeDescendants: boolPointer(false),
				},
			},
		},
		{
			name: "recursive non-root",
			folderScopes: []FolderScopeRequest{
				{
					KnowledgeBaseID: scopeKB1,
					FolderIDs:       []string{""},
				},
				{
					KnowledgeBaseID:    scopeKB1,
					FolderIDs:          []string{scopeFolder1},
					IncludeDescendants: boolPointer(true),
				},
			},
		},
		{
			name: "same selector entry",
			folderScopes: []FolderScopeRequest{{
				KnowledgeBaseID: scopeKB1,
				FolderIDs:       []string{"", scopeFolder1},
			}},
		},
		{
			name: "invalid non-root UUID",
			folderScopes: []FolderScopeRequest{
				{
					KnowledgeBaseID: scopeKB1,
					FolderIDs:       []string{""},
				},
				{
					KnowledgeBaseID: scopeKB1,
					FolderIDs:       []string{"not-a-folder-uuid"},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			folderScopes := test.folderScopes
			_, err := NormalizeKnowledgeScopeRequest(&KnowledgeScopeRequest{
				FolderScopes: &folderScopes,
			})

			require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
		})
	}
}

func TestReconcileKnowledgeScopeRequestUsesCanonicalWithoutUnion(t *testing.T) {
	canonicalFolders := []FolderScopeRequest{{
		KnowledgeBaseID:    scopeKB1,
		FolderIDs:          []string{scopeFolder1},
		IncludeDescendants: boolPointer(false),
	}}
	canonical := &KnowledgeScopeRequest{FolderScopes: &canonicalFolders}
	legacy := &KnowledgeScopeRequest{KnowledgeBaseIDs: []string{scopeKB1}}

	reconciled, err := ReconcileKnowledgeScopeRequest(canonical, legacy)
	require.NoError(t, err)
	require.Empty(t, reconciled.KnowledgeBaseIDs)
	require.Equal(t, canonicalFolders, *reconciled.FolderScopes)
}

func TestReconcileKnowledgeScopeRequestRejectsConflictingLegacyProjection(t *testing.T) {
	canonical := &KnowledgeScopeRequest{KnowledgeBaseIDs: []string{scopeKB1}}
	legacy := &KnowledgeScopeRequest{KnowledgeBaseIDs: []string{scopeKB2}}

	_, err := ReconcileKnowledgeScopeRequest(canonical, legacy)
	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func TestResolvedFolderFilterCopiesInputAndOutput(t *testing.T) {
	input := []string{scopeFolder2, scopeFolder1}
	filter, err := NewResolvedFolderFilter(true, input)
	require.NoError(t, err)

	input[0] = scopeFolder3
	first := filter.FolderIDs()
	require.Equal(t, []string{scopeFolder1, scopeFolder2}, first)
	first[0] = scopeFolder3
	require.Equal(t, []string{scopeFolder1, scopeFolder2}, filter.FolderIDs())
}

func TestResolvedFolderFilterDisabledAllowsNilIDs(t *testing.T) {
	filter, err := NewResolvedFolderFilter(false, nil)

	require.NoError(t, err)
	require.False(t, filter.Enabled())
	require.False(t, filter.Empty())
	require.Empty(t, filter.FolderIDs())
}

func TestResolvedFolderFilterDisabledAllowsEmptyIDs(t *testing.T) {
	filter, err := NewResolvedFolderFilter(false, []string{})

	require.NoError(t, err)
	require.False(t, filter.Enabled())
	require.False(t, filter.Empty())
	require.Empty(t, filter.FolderIDs())
}

func TestResolvedFolderFilterDisabledRejectsRootID(t *testing.T) {
	_, err := NewResolvedFolderFilter(false, []string{KnowledgeFolderRootID})

	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func TestResolvedFolderFilterDisabledRejectsNonRootID(t *testing.T) {
	_, err := NewResolvedFolderFilter(false, []string{scopeFolder1})

	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func TestKnowledgeScopeTargetCopiesAllSlices(t *testing.T) {
	knowledgeIDs := []string{"knowledge-2", "knowledge-1"}
	tagIDs := []string{"tag-2", "tag-1"}
	scopeTagIDs := []string{"scope-tag-2", "scope-tag-1"}
	filter, err := NewResolvedFolderFilter(true, []string{scopeFolder1})
	require.NoError(t, err)

	target, err := NewKnowledgeScopeTarget(
		scopeKB1,
		1,
		knowledgeIDs,
		tagIDs,
		scopeTagIDs,
		filter,
	)
	require.NoError(t, err)

	knowledgeIDs[0] = "changed"
	tagIDs[0] = "changed"
	scopeTagIDs[0] = "changed"
	require.Equal(t, []string{"knowledge-1", "knowledge-2"}, target.KnowledgeIDs())
	require.Equal(t, []string{"tag-1", "tag-2"}, target.TagIDs())
	require.Equal(t, []string{"scope-tag-1", "scope-tag-2"}, target.ScopeTagIDs())

	gotKnowledge := target.KnowledgeIDs()
	gotTags := target.TagIDs()
	gotScopeTags := target.ScopeTagIDs()
	gotFilter := target.FolderFilter()
	gotKnowledge[0] = "mutated"
	gotTags[0] = "mutated"
	gotScopeTags[0] = "mutated"
	gotFilterIDs := gotFilter.FolderIDs()
	gotFilterIDs[0] = scopeFolder2
	require.Equal(t, []string{"knowledge-1", "knowledge-2"}, target.KnowledgeIDs())
	require.Equal(t, []string{"tag-1", "tag-2"}, target.TagIDs())
	require.Equal(t, []string{"scope-tag-1", "scope-tag-2"}, target.ScopeTagIDs())
	require.Equal(t, []string{scopeFolder1}, target.FolderFilter().FolderIDs())
}

func TestKnowledgeScopeTargetsReturnsDeepCopy(t *testing.T) {
	filter, err := NewResolvedFolderFilter(true, []string{scopeFolder1})
	require.NoError(t, err)
	target, err := NewKnowledgeScopeTarget(
		scopeKB1,
		1,
		[]string{"knowledge-1"},
		[]string{"tag-1"},
		[]string{"scope-tag-1"},
		filter,
	)
	require.NoError(t, err)
	scope, err := NewKnowledgeScope([]KnowledgeScopeTarget{target})
	require.NoError(t, err)

	first := scope.Targets()
	require.Len(t, first, 1)
	first[0] = KnowledgeScopeTarget{}
	second := scope.Targets()
	require.Equal(t, scopeKB1, second[0].KnowledgeBaseID())

	secondKnowledge := second[0].KnowledgeIDs()
	secondKnowledge[0] = "mutated"
	require.Equal(t, []string{"knowledge-1"}, scope.Targets()[0].KnowledgeIDs())
}

func TestKnowledgeScopeRejectsDuplicateTargets(t *testing.T) {
	filter, err := NewResolvedFolderFilter(false, nil)
	require.NoError(t, err)
	target, err := NewKnowledgeScopeTarget(scopeKB1, 1, nil, nil, nil, filter)
	require.NoError(t, err)

	_, err = NewKnowledgeScope([]KnowledgeScopeTarget{target, target})
	require.True(t, errors.Is(err, ErrInvalidKnowledgeScopeRequest))
}

func TestKnowledgeScopeHasLocalKnowledge(t *testing.T) {
	disabled, err := NewResolvedFolderFilter(false, nil)
	require.NoError(t, err)
	enabledEmpty, err := NewResolvedFolderFilter(true, nil)
	require.NoError(t, err)
	enabledFolder, err := NewResolvedFolderFilter(true, []string{scopeFolder1})
	require.NoError(t, err)

	newTarget := func(tenantID uint64, kbID string, filter ResolvedFolderFilter) KnowledgeScopeTarget {
		target, targetErr := NewKnowledgeScopeTarget(kbID, tenantID, nil, nil, nil, filter)
		require.NoError(t, targetErr)
		return target
	}

	tests := []struct {
		name    string
		targets []KnowledgeScopeTarget
		want    bool
	}{
		{name: "no targets", want: false},
		{name: "all enabled empty", targets: []KnowledgeScopeTarget{
			newTarget(1, scopeKB1, enabledEmpty),
			newTarget(2, scopeKB2, enabledEmpty),
		}, want: false},
		{name: "disabled target", targets: []KnowledgeScopeTarget{
			newTarget(1, scopeKB1, enabledEmpty),
			newTarget(2, scopeKB2, disabled),
		}, want: true},
		{name: "resolved folder target", targets: []KnowledgeScopeTarget{
			newTarget(1, scopeKB1, enabledFolder),
		}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scope, scopeErr := NewKnowledgeScope(test.targets)
			require.NoError(t, scopeErr)
			require.Equal(t, test.want, scope.HasLocalKnowledge())
		})
	}

	var nilScope *KnowledgeScope
	require.False(t, nilScope.HasLocalKnowledge())
	require.True(t, nilScope.IsEmpty())
	require.Zero(t, nilScope.Len())
	require.Empty(t, nilScope.Targets())
	require.Nil(t, nilScope.Clone())
}

func TestKnowledgeScopeRequestCloneNilReceiver(t *testing.T) {
	var request *KnowledgeScopeRequest

	require.Nil(t, request.Clone())
}

func TestKnowledgeScopeRequestClonePreservesMissingFolderScopes(t *testing.T) {
	request := &KnowledgeScopeRequest{}

	cloned := request.Clone()

	require.NotNil(t, cloned)
	require.Nil(t, cloned.FolderScopes)
}

func TestKnowledgeScopeRequestClonePreservesExplicitEmptyFolderScopes(t *testing.T) {
	folderScopes := []FolderScopeRequest{}
	request := &KnowledgeScopeRequest{FolderScopes: &folderScopes}

	cloned := request.Clone()

	require.NotNil(t, cloned)
	require.NotNil(t, cloned.FolderScopes)
	require.Empty(t, *cloned.FolderScopes)
	require.NotSame(t, request.FolderScopes, cloned.FolderScopes)
}

func TestKnowledgeScopeRequestCloneDoesNotShareTopLevelSlices(t *testing.T) {
	request := &KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"kb-original"},
		KnowledgeIDs:     []string{"knowledge-original"},
		TagScopes: []TagScope{{
			KnowledgeBaseID: "tag-kb-original",
		}},
	}

	cloned := request.Clone()
	request.KnowledgeBaseIDs[0] = "kb-mutated"
	request.KnowledgeIDs[0] = "knowledge-mutated"
	request.TagScopes[0].KnowledgeBaseID = "tag-kb-mutated"
	require.Equal(t, []string{"kb-original"}, cloned.KnowledgeBaseIDs)
	require.Equal(t, []string{"knowledge-original"}, cloned.KnowledgeIDs)
	require.Equal(t, "tag-kb-original", cloned.TagScopes[0].KnowledgeBaseID)

	cloned.KnowledgeBaseIDs[0] = "clone-kb-mutated"
	cloned.KnowledgeIDs[0] = "clone-knowledge-mutated"
	cloned.TagScopes[0].KnowledgeBaseID = "clone-tag-kb-mutated"
	require.Equal(t, []string{"kb-mutated"}, request.KnowledgeBaseIDs)
	require.Equal(t, []string{"knowledge-mutated"}, request.KnowledgeIDs)
	require.Equal(t, "tag-kb-mutated", request.TagScopes[0].KnowledgeBaseID)
}

func TestKnowledgeScopeRequestCloneDoesNotShareTagScopeSlices(t *testing.T) {
	request := &KnowledgeScopeRequest{
		TagScopes: []TagScope{{
			KnowledgeBaseID: scopeKB1,
			TagIDs:          []string{"tag-original"},
		}},
	}

	cloned := request.Clone()
	request.TagScopes[0].TagIDs[0] = "tag-mutated"
	require.Equal(t, []string{"tag-original"}, cloned.TagScopes[0].TagIDs)

	cloned.TagScopes[0].TagIDs[0] = "clone-tag-mutated"
	require.Equal(t, []string{"tag-mutated"}, request.TagScopes[0].TagIDs)
}

func TestKnowledgeScopeRequestCloneDoesNotShareFolderScopeSlices(t *testing.T) {
	folderScopes := []FolderScopeRequest{{
		KnowledgeBaseID: scopeKB1,
		FolderIDs:       []string{scopeFolder1},
	}}
	request := &KnowledgeScopeRequest{FolderScopes: &folderScopes}

	cloned := request.Clone()
	(*request.FolderScopes)[0].KnowledgeBaseID = scopeKB2
	(*request.FolderScopes)[0].FolderIDs[0] = scopeFolder2
	require.Equal(t, scopeKB1, (*cloned.FolderScopes)[0].KnowledgeBaseID)
	require.Equal(t, []string{scopeFolder1}, (*cloned.FolderScopes)[0].FolderIDs)

	(*cloned.FolderScopes)[0].KnowledgeBaseID = "clone-kb"
	(*cloned.FolderScopes)[0].FolderIDs[0] = scopeFolder3
	require.Equal(t, scopeKB2, (*request.FolderScopes)[0].KnowledgeBaseID)
	require.Equal(t, []string{scopeFolder2}, (*request.FolderScopes)[0].FolderIDs)
}

func TestKnowledgeScopeRequestCloneDoesNotShareDescendantsPointers(t *testing.T) {
	folderScopes := []FolderScopeRequest{{
		KnowledgeBaseID:    scopeKB1,
		FolderIDs:          []string{scopeFolder1},
		IncludeDescendants: boolPointer(true),
	}}
	request := &KnowledgeScopeRequest{FolderScopes: &folderScopes}

	cloned := request.Clone()
	*(*request.FolderScopes)[0].IncludeDescendants = false
	require.True(t, *(*cloned.FolderScopes)[0].IncludeDescendants)

	*(*cloned.FolderScopes)[0].IncludeDescendants = false
	*(*request.FolderScopes)[0].IncludeDescendants = true
	require.False(t, *(*cloned.FolderScopes)[0].IncludeDescendants)
	require.True(t, *(*request.FolderScopes)[0].IncludeDescendants)
}

func TestResolvedFolderFilterFormattingDoesNotExposeIDs(t *testing.T) {
	filter := ResolvedFolderFilter{
		enabled:   true,
		folderIDs: []string{"secret-folder"},
	}

	for _, format := range []string{"%v", "%+v", "%#v"} {
		output := fmt.Sprintf(format, filter)
		require.NotContains(t, output, "secret-folder")
		require.Contains(t, output, "enabled=")
		require.Contains(t, output, "folder_ids=")
	}
}

func TestKnowledgeScopeTargetFormattingDoesNotExposeRuntimeFields(t *testing.T) {
	target := KnowledgeScopeTarget{
		knowledgeBaseID: "secret-kb",
		sourceTenantID:  987654321,
		knowledgeIDs:    []string{"secret-knowledge"},
		tagIDs:          []string{"secret-tag"},
		scopeTagIDs:     []string{"secret-scope-tag"},
		folderFilter: ResolvedFolderFilter{
			enabled:   true,
			folderIDs: []string{"secret-folder"},
		},
	}

	for _, format := range []string{"%v", "%+v", "%#v"} {
		output := fmt.Sprintf(format, target)
		for _, secret := range []string{
			"secret-tenant",
			"secret-kb",
			"987654321",
			"secret-knowledge",
			"secret-tag",
			"secret-scope-tag",
			"secret-folder",
		} {
			require.NotContains(t, output, secret)
		}
		require.Contains(t, output, "knowledge_ids=")
		require.Contains(t, output, "folder_ids=")
		require.Contains(t, output, "folder_enabled=")
	}
}

func TestKnowledgeScopeValueFormattingDoesNotExposeRuntimeFields(t *testing.T) {
	scope := KnowledgeScope{targets: []KnowledgeScopeTarget{{
		knowledgeBaseID: "secret-kb",
		sourceTenantID:  991122,
		knowledgeIDs:    []string{"secret-knowledge"},
		tagIDs:          []string{"secret-tag"},
		scopeTagIDs:     []string{"secret-scope-tag"},
		folderFilter: ResolvedFolderFilter{
			enabled:   true,
			folderIDs: []string{"secret-folder"},
		},
	}}}

	for _, format := range []string{"%v", "%+v", "%#v"} {
		output := fmt.Sprintf(format, scope)
		for _, secret := range []string{
			"secret-kb",
			"991122",
			"secret-knowledge",
			"secret-tag",
			"secret-scope-tag",
			"secret-folder",
		} {
			require.NotContains(t, output, secret)
		}
		require.Contains(t, output, "KnowledgeScope")
		require.Contains(t, output, "targets=")
		require.Contains(t, output, "local=")
	}
}

func TestKnowledgeScopePointerFormattingDoesNotExposeRuntimeFields(t *testing.T) {
	scope := &KnowledgeScope{targets: []KnowledgeScopeTarget{{
		knowledgeBaseID: "secret-kb",
		sourceTenantID:  991122,
		knowledgeIDs:    []string{"secret-knowledge"},
		tagIDs:          []string{"secret-tag"},
		scopeTagIDs:     []string{"secret-scope-tag"},
		folderFilter: ResolvedFolderFilter{
			enabled:   true,
			folderIDs: []string{"secret-folder"},
		},
	}}}

	for _, format := range []string{"%v", "%+v", "%#v"} {
		output := fmt.Sprintf(format, scope)
		for _, secret := range []string{
			"secret-kb",
			"991122",
			"secret-knowledge",
			"secret-tag",
			"secret-scope-tag",
			"secret-folder",
		} {
			require.NotContains(t, output, secret)
		}
		require.Contains(t, output, "KnowledgeScope")
		require.Contains(t, output, "targets=")
		require.Contains(t, output, "local=")
	}
}

func TestNilKnowledgeScopeFormattingIsSafe(t *testing.T) {
	var scope *KnowledgeScope

	for _, format := range []string{"%v", "%+v", "%#v"} {
		output := fmt.Sprintf(format, scope)
		require.NotEmpty(t, output)
		for _, secret := range []string{
			"secret-kb",
			"991122",
			"secret-knowledge",
			"secret-tag",
			"secret-scope-tag",
			"secret-folder",
		} {
			require.NotContains(t, output, secret)
		}
	}
}

func TestAuthorizedKnowledgeScopeTargetFormattingDoesNotExposeRuntimeFields(t *testing.T) {
	target := AuthorizedKnowledgeScopeTarget{
		KnowledgeBaseID: "secret-kb",
		SourceTenantID:  991122,
		KnowledgeIDs:    []string{"secret-knowledge"},
		TagIDs:          []string{"secret-tag"},
		ScopeTagIDs:     []string{"secret-scope-tag"},
	}

	for _, format := range []string{"%v", "%+v", "%#v"} {
		output := fmt.Sprintf(format, target)
		for _, secret := range []string{
			"secret-kb",
			"991122",
			"secret-knowledge",
			"secret-tag",
			"secret-scope-tag",
		} {
			require.NotContains(t, output, secret)
		}
		require.Contains(t, output, "AuthorizedKnowledgeScopeTarget")
		require.Contains(t, output, "knowledge_ids=")
		require.Contains(t, output, "tag_ids=")
		require.Contains(t, output, "scope_tag_ids=")
	}
}

func TestKnowledgeScopeResolveInputFormattingDoesNotExposeRuntimeFields(t *testing.T) {
	folderScopes := []FolderScopeRequest{{
		KnowledgeBaseID: "secret-request-kb",
		FolderIDs:       []string{"secret-folder"},
	}}
	input := KnowledgeScopeResolveInput{
		Request: &KnowledgeScopeRequest{
			KnowledgeBaseIDs: []string{"secret-request-kb"},
			KnowledgeIDs:     []string{"secret-request-knowledge"},
			FolderScopes:     &folderScopes,
		},
		AuthorizedTargets: []AuthorizedKnowledgeScopeTarget{{
			KnowledgeBaseID: "secret-kb",
			SourceTenantID:  991122,
			KnowledgeIDs:    []string{"secret-knowledge"},
			TagIDs:          []string{"secret-tag"},
		}},
	}

	for _, format := range []string{"%v", "%+v", "%#v"} {
		output := fmt.Sprintf(format, input)
		for _, secret := range []string{
			"secret-request-kb",
			"secret-request-knowledge",
			"secret-folder",
			"secret-kb",
			"991122",
			"secret-knowledge",
			"secret-tag",
		} {
			require.NotContains(t, output, secret)
		}
		require.Contains(t, output, "KnowledgeScopeResolveInput")
		require.Contains(t, output, "request_present=")
		require.Contains(t, output, "authorized_targets=")
	}
}

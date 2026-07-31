package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKnowledgeScopePreparationOwnsRequestExecutionAndHash(t *testing.T) {
	includeDescendants := true
	folderScopes := []FolderScopeRequest{{
		KnowledgeBaseID:    scopeKB1,
		FolderIDs:          []string{scopeFolder1},
		IncludeDescendants: &includeDescendants,
	}}
	request := &KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{scopeKB1},
		KnowledgeIDs:     []string{"knowledge-1"},
		TagScopes: []TagScope{{
			KnowledgeBaseID: scopeKB1,
			TagIDs:          []string{"tag-1"},
		}},
		FolderScopes: &folderScopes,
	}
	execution := newRuntimeTestKnowledgeScope(t)
	executionHash, err := HashKnowledgeScope(execution)
	require.NoError(t, err)

	preparation, err := NewKnowledgeScopePreparation(request, execution, executionHash)
	require.NoError(t, err)

	request.KnowledgeBaseIDs[0] = "mutated-kb"
	request.KnowledgeIDs[0] = "mutated-knowledge"
	request.TagScopes[0].TagIDs[0] = "mutated-tag"
	(*request.FolderScopes)[0].FolderIDs[0] = scopeFolder4
	*(*request.FolderScopes)[0].IncludeDescendants = false
	execution.targets[0].knowledgeIDs[0] = "mutated-execution-knowledge"
	execution.targets[0].folderFilter.folderIDs[0] = scopeFolder4

	preparedRequest := preparation.Request()
	require.Equal(t, []string{scopeKB1}, preparedRequest.KnowledgeBaseIDs)
	require.Equal(t, []string{"knowledge-1"}, preparedRequest.KnowledgeIDs)
	require.Equal(t, []string{"tag-1"}, preparedRequest.TagScopes[0].TagIDs)
	require.Equal(t, []string{scopeFolder1}, (*preparedRequest.FolderScopes)[0].FolderIDs)
	require.True(t, *(*preparedRequest.FolderScopes)[0].IncludeDescendants)

	preparedExecution := preparation.Execution()
	require.Equal(t, []string{"knowledge-1"}, preparedExecution.targets[0].KnowledgeIDs())
	require.Equal(t, []string{scopeFolder2}, preparedExecution.targets[0].FolderFilter().FolderIDs())
	require.Equal(t, executionHash, preparation.ExecutionScopeHash())

	preparedRequest.KnowledgeBaseIDs[0] = "mutated-returned-kb"
	preparedRequest.TagScopes[0].TagIDs[0] = "mutated-returned-tag"
	(*preparedRequest.FolderScopes)[0].FolderIDs[0] = scopeFolder4
	*(*preparedRequest.FolderScopes)[0].IncludeDescendants = false
	preparedExecution.targets[0].knowledgeIDs[0] = "mutated-returned-knowledge"
	preparedExecution.targets[0].folderFilter.folderIDs[0] = scopeFolder4

	secondRequest := preparation.Request()
	secondExecution := preparation.Execution()
	require.Equal(t, []string{scopeKB1}, secondRequest.KnowledgeBaseIDs)
	require.Equal(t, []string{"tag-1"}, secondRequest.TagScopes[0].TagIDs)
	require.Equal(t, []string{scopeFolder1}, (*secondRequest.FolderScopes)[0].FolderIDs)
	require.True(t, *(*secondRequest.FolderScopes)[0].IncludeDescendants)
	require.Equal(t, []string{"knowledge-1"}, secondExecution.targets[0].KnowledgeIDs())
	require.Equal(t, []string{scopeFolder2}, secondExecution.targets[0].FolderFilter().FolderIDs())
	require.Equal(t, executionHash, preparation.ExecutionScopeHash())
}

func TestProjectKnowledgeScopeToSearchTargetsPreservesProjectionWithoutAliasing(t *testing.T) {
	execution := newRuntimeTestKnowledgeScope(t)
	executionHash, err := HashKnowledgeScope(execution)
	require.NoError(t, err)

	first := ProjectKnowledgeScopeToSearchTargets(execution, executionHash)
	second := ProjectKnowledgeScopeToSearchTargets(execution, executionHash)
	require.Len(t, first, 1)
	require.Len(t, second, 1)

	target := first[0]
	require.Equal(t, SearchTargetTypeKnowledge, target.Type)
	require.Equal(t, scopeKB1, target.KnowledgeBaseID)
	require.Equal(t, uint64(27), target.TenantID)
	require.Equal(t, uint64(27), target.SourceTenantID)
	require.Equal(t, uint64(27), target.EffectiveSourceTenantID())
	require.Equal(t, []string{"knowledge-1"}, target.KnowledgeIDs)
	require.Equal(t, []string{"tag-1"}, target.TagIDs)
	require.Equal(t, []string{"scope-tag-1"}, target.ScopeTagIDs)
	require.True(t, target.FolderFilter.Enabled())
	require.Equal(t, []string{scopeFolder2}, target.FolderFilter.FolderIDs())
	require.Equal(t, executionHash, target.ExecutionScopeHash)
	require.True(t, target.DisableRecallThresholds)

	target.KnowledgeIDs[0] = "mutated-knowledge"
	target.TagIDs[0] = "mutated-tag"
	target.ScopeTagIDs[0] = "mutated-scope-tag"
	target.FolderFilter.folderIDs[0] = scopeFolder4

	sourceTarget := execution.Targets()[0]
	require.Equal(t, []string{"knowledge-1"}, sourceTarget.KnowledgeIDs())
	require.Equal(t, []string{"tag-1"}, sourceTarget.TagIDs())
	require.Equal(t, []string{"scope-tag-1"}, sourceTarget.ScopeTagIDs())
	require.Equal(t, []string{scopeFolder2}, sourceTarget.FolderFilter().FolderIDs())
	require.Equal(t, []string{"knowledge-1"}, second[0].KnowledgeIDs)
	require.Equal(t, []string{"tag-1"}, second[0].TagIDs)
	require.Equal(t, []string{"scope-tag-1"}, second[0].ScopeTagIDs)
	require.Equal(t, []string{scopeFolder2}, second[0].FolderFilter.FolderIDs())

	cloned := second[0].Clone()
	cloned.KnowledgeIDs[0] = "mutated-clone-knowledge"
	cloned.TagIDs[0] = "mutated-clone-tag"
	cloned.ScopeTagIDs[0] = "mutated-clone-scope-tag"
	cloned.FolderFilter.folderIDs[0] = scopeFolder4
	require.Equal(t, []string{"knowledge-1"}, second[0].KnowledgeIDs)
	require.Equal(t, []string{"tag-1"}, second[0].TagIDs)
	require.Equal(t, []string{"scope-tag-1"}, second[0].ScopeTagIDs)
	require.Equal(t, []string{scopeFolder2}, second[0].FolderFilter.FolderIDs())
}

func TestChatManageCloneCopiesExecutionScopeProjectionAndHash(t *testing.T) {
	execution := newRuntimeTestKnowledgeScope(t)
	executionHash, err := HashKnowledgeScope(execution)
	require.NoError(t, err)
	searchTargets := ProjectKnowledgeScopeToSearchTargets(execution, executionHash)
	original := &ChatManage{
		PipelineRequest: PipelineRequest{
			KnowledgeBaseIDs:         []string{scopeKB1},
			KnowledgeIDs:             []string{"knowledge-1"},
			SearchTargets:            searchTargets,
			ExecutionScope:           execution,
			ExecutionScopeHash:       executionHash,
			RetrievalExplicitlyEmpty: true,
		},
	}

	cloned := original.Clone()
	require.NotSame(t, original.ExecutionScope, cloned.ExecutionScope)
	require.NotSame(t, original.SearchTargets[0], cloned.SearchTargets[0])
	require.Equal(t, executionHash, cloned.ExecutionScopeHash)
	require.Equal(t, executionHash, cloned.SearchTargets[0].ExecutionScopeHash)
	require.True(t, cloned.RetrievalExplicitlyEmpty)
	require.Equal(t, uint64(27), cloned.SearchTargets[0].SourceTenantID)
	require.Equal(t, []string{scopeFolder2}, cloned.SearchTargets[0].FolderFilter.FolderIDs())
	require.Equal(t, []string{scopeFolder2}, cloned.ExecutionScope.targets[0].FolderFilter().FolderIDs())

	cloned.KnowledgeBaseIDs[0] = "mutated-kb"
	cloned.KnowledgeIDs[0] = "mutated-knowledge-list"
	cloned.SearchTargets[0].KnowledgeIDs[0] = "mutated-target-knowledge"
	cloned.SearchTargets[0].TagIDs[0] = "mutated-target-tag"
	cloned.SearchTargets[0].ScopeTagIDs[0] = "mutated-target-scope-tag"
	cloned.SearchTargets[0].FolderFilter.folderIDs[0] = scopeFolder4
	cloned.ExecutionScope.targets[0].knowledgeIDs[0] = "mutated-execution-knowledge"
	cloned.ExecutionScope.targets[0].folderFilter.folderIDs[0] = scopeFolder4

	require.Equal(t, []string{scopeKB1}, original.KnowledgeBaseIDs)
	require.Equal(t, []string{"knowledge-1"}, original.KnowledgeIDs)
	require.Equal(t, []string{"knowledge-1"}, original.SearchTargets[0].KnowledgeIDs)
	require.Equal(t, []string{"tag-1"}, original.SearchTargets[0].TagIDs)
	require.Equal(t, []string{"scope-tag-1"}, original.SearchTargets[0].ScopeTagIDs)
	require.Equal(t, []string{scopeFolder2}, original.SearchTargets[0].FolderFilter.FolderIDs())
	require.Equal(t, []string{"knowledge-1"}, original.ExecutionScope.targets[0].KnowledgeIDs())
	require.Equal(t, []string{scopeFolder2}, original.ExecutionScope.targets[0].FolderFilter().FolderIDs())
}

func TestPersistentRequestStateContainsOnlyRawScopeAndExecutionHash(t *testing.T) {
	includeDescendants := true
	requestedFolderScopes := []FolderScopeRequest{{
		KnowledgeBaseID:    scopeKB1,
		FolderIDs:          []string{scopeFolder1},
		IncludeDescendants: &includeDescendants,
	}}
	request := &KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{scopeKB1},
		FolderScopes:     &requestedFolderScopes,
	}
	execution := newRuntimeTestKnowledgeScope(t)
	executionHash, err := HashKnowledgeScope(execution)
	require.NoError(t, err)
	preparation, err := NewKnowledgeScopePreparation(request, execution, executionHash)
	require.NoError(t, err)

	lastRequestState := SessionLastRequestState{
		RequestScope: preparation.Request(),
	}
	messageContext := MessageExecutionContext{
		RequestScope:       preparation.Request(),
		ExecutionScopeHash: preparation.ExecutionScopeHash(),
	}

	request.KnowledgeBaseIDs[0] = "mutated-kb"
	(*request.FolderScopes)[0].FolderIDs[0] = scopeFolder4
	*(*request.FolderScopes)[0].IncludeDescendants = false

	stateJSON, err := json.Marshal(lastRequestState)
	require.NoError(t, err)
	messageJSON, err := json.Marshal(messageContext)
	require.NoError(t, err)

	requireJSONHasOnlyPersistentKnowledgeScope(
		t,
		stateJSON,
		false,
		executionHash,
	)
	requireJSONHasOnlyPersistentKnowledgeScope(
		t,
		messageJSON,
		true,
		executionHash,
	)

	require.Equal(t, []string{scopeKB1}, lastRequestState.RequestScope.KnowledgeBaseIDs)
	require.Equal(
		t,
		[]string{scopeFolder1},
		(*lastRequestState.RequestScope.FolderScopes)[0].FolderIDs,
	)
	require.True(
		t,
		*(*lastRequestState.RequestScope.FolderScopes)[0].IncludeDescendants,
	)
	require.Equal(t, []string{scopeKB1}, messageContext.RequestScope.KnowledgeBaseIDs)
	require.Equal(
		t,
		[]string{scopeFolder1},
		(*messageContext.RequestScope.FolderScopes)[0].FolderIDs,
	)
	require.True(
		t,
		*(*messageContext.RequestScope.FolderScopes)[0].IncludeDescendants,
	)
}

func newRuntimeTestKnowledgeScope(t *testing.T) *KnowledgeScope {
	t.Helper()
	folderFilter, err := NewResolvedFolderFilter(true, []string{scopeFolder2})
	require.NoError(t, err)
	target, err := NewKnowledgeScopeTarget(
		scopeKB1,
		27,
		[]string{"knowledge-1"},
		[]string{"tag-1"},
		[]string{"scope-tag-1"},
		folderFilter,
	)
	require.NoError(t, err)
	execution, err := NewKnowledgeScope([]KnowledgeScopeTarget{target})
	require.NoError(t, err)
	return execution
}

func requireJSONHasOnlyPersistentKnowledgeScope(
	t *testing.T,
	encoded []byte,
	wantHash bool,
	executionHash string,
) {
	t.Helper()
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.Contains(t, payload, "knowledge_scope")
	require.NotContains(t, payload, "execution_scope")
	require.NotContains(t, payload, "authorized_targets")
	require.NotContains(t, payload, "source_tenant_id")
	require.NotContains(t, payload, "resolved_folder_ids")
	require.Contains(t, string(encoded), scopeFolder1)
	require.NotContains(t, string(encoded), scopeFolder2)
	require.NotContains(t, string(encoded), `"source_tenant_id"`)
	if wantHash {
		require.JSONEq(t, `"`+executionHash+`"`, string(payload["execution_scope_hash"]))
		return
	}
	require.NotContains(t, payload, "execution_scope_hash")
	require.NotContains(t, string(encoded), executionHash)
}

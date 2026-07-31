package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNewKnowledgeFolderResponseMapsWhitelistWithoutMutation(t *testing.T) {
	createdAt := time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	folder := &types.KnowledgeFolder{
		ID:              "folder-1",
		TenantID:        41,
		KnowledgeBaseID: "kb-1",
		ParentID:        "parent-1",
		Name:            "Reports",
		Path:            "/parent-1/folder-1/",
		Depth:           2,
		SortOrder:       7,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
	before := *folder

	response := NewKnowledgeFolderResponse(folder)

	require.Equal(t, &KnowledgeFolderResponse{
		ID:        "folder-1",
		ParentID:  "parent-1",
		Name:      "Reports",
		Depth:     2,
		SortOrder: 7,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, response)
	require.Equal(t, before, *folder)
	requireKnowledgeFolderExactKeys(t, response, knowledgeFolderKeySet(
		"id",
		"parent_id",
		"name",
		"depth",
		"sort_order",
		"created_at",
		"updated_at",
	))
}

func TestNewKnowledgeFolderWithStatsResponsePreservesZeroValuesAndExactKeys(t *testing.T) {
	createdAt := time.Date(2026, time.July, 20, 11, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(2 * time.Hour)
	folder := &types.KnowledgeFolderWithStats{
		KnowledgeFolder: types.KnowledgeFolder{
			ID:              "folder-root-child",
			TenantID:        42,
			KnowledgeBaseID: "kb-2",
			ParentID:        "",
			Name:            "Root child",
			Path:            "/folder-root-child/",
			Depth:           1,
			SortOrder:       0,
			CreatedAt:       createdAt,
			UpdatedAt:       updatedAt,
		},
		KnowledgeCount: 0,
		HasChildren:    false,
	}
	before := *folder

	response := NewKnowledgeFolderWithStatsResponse(folder)

	require.Equal(t, &KnowledgeFolderWithStatsResponse{
		ID:             "folder-root-child",
		ParentID:       "",
		Name:           "Root child",
		Depth:          1,
		SortOrder:      0,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		KnowledgeCount: 0,
		HasChildren:    false,
	}, response)
	require.Equal(t, before, *folder)
	requireKnowledgeFolderExactKeys(t, response, knowledgeFolderKeySet(
		"id",
		"parent_id",
		"name",
		"depth",
		"sort_order",
		"created_at",
		"updated_at",
		"knowledge_count",
		"has_children",
	))
}

func TestNewKnowledgeFolderBreadcrumbItemResponsesPreserveOrderAndExactKeys(t *testing.T) {
	folders := []*types.KnowledgeFolder{
		{
			ID:              "folder-parent",
			TenantID:        43,
			KnowledgeBaseID: "kb-3",
			ParentID:        "",
			Name:            "Parent",
			Path:            "/folder-parent/",
			Depth:           1,
			SortOrder:       4,
		},
		{
			ID:              "folder-child",
			TenantID:        43,
			KnowledgeBaseID: "kb-3",
			ParentID:        "folder-parent",
			Name:            "Child",
			Path:            "/folder-parent/folder-child/",
			Depth:           2,
			SortOrder:       8,
		},
	}
	beforeParent := *folders[0]
	beforeChild := *folders[1]

	responses := NewKnowledgeFolderBreadcrumbItemResponses(folders)

	require.Equal(t, []*KnowledgeFolderBreadcrumbItemResponse{
		{ID: "folder-parent", ParentID: "", Name: "Parent", Depth: 1},
		{ID: "folder-child", ParentID: "folder-parent", Name: "Child", Depth: 2},
	}, responses)
	require.Equal(t, beforeParent, *folders[0])
	require.Equal(t, beforeChild, *folders[1])
	expectedKeys := knowledgeFolderKeySet("id", "parent_id", "name", "depth")
	for _, response := range responses {
		requireKnowledgeFolderExactKeys(t, response, expectedKeys)
	}
}

func TestKnowledgeFolderResponseMappersHandleNilAndEmptySlices(t *testing.T) {
	require.NotPanics(t, func() {
		require.Nil(t, NewKnowledgeFolderResponse(nil))
		require.Nil(t, NewKnowledgeFolderWithStatsResponse(nil))
		require.Nil(t, NewKnowledgeFolderBreadcrumbItemResponse(nil))
	})

	statsInputs := [][]*types.KnowledgeFolderWithStats{nil, {}}
	for _, input := range statsInputs {
		responses := NewKnowledgeFolderWithStatsResponses(input)
		require.NotNil(t, responses)
		require.Empty(t, responses)
		requireKnowledgeFolderJSONArray(t, responses)
	}

	breadcrumbInputs := [][]*types.KnowledgeFolder{nil, {}}
	for _, input := range breadcrumbInputs {
		responses := NewKnowledgeFolderBreadcrumbItemResponses(input)
		require.NotNil(t, responses)
		require.Empty(t, responses)
		requireKnowledgeFolderJSONArray(t, responses)
	}
}

func knowledgeFolderKeySet(keys ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

func requireKnowledgeFolderExactKeys(t *testing.T, value interface{}, expected map[string]struct{}) {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &raw))
	actual := make(map[string]struct{}, len(raw))
	for key := range raw {
		actual[key] = struct{}{}
	}
	require.Equal(t, expected, actual)
	for _, internalKey := range []string{"tenant_id", "knowledge_base_id", "path", "deleted_at"} {
		require.NotContains(t, raw, internalKey)
	}
}

func requireKnowledgeFolderJSONArray(t *testing.T, value interface{}) {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	require.Equal(t, "[]", string(body))
}

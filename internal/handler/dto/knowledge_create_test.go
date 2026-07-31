package dto

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCreateURLKnowledgeRequestHasExactJSONKeys(t *testing.T) {
	enabled := true
	request := CreateURLKnowledgeRequest{
		URL:              "https://example.com/document.pdf",
		FileName:         "document.pdf",
		FileType:         "pdf",
		EnableMultimodel: &enabled,
		Title:            "Document",
		TagIDs:           []string{"tag-1", "tag-2"},
		Channel:          "api",
		ProcessConfig: &types.KnowledgeProcessOverrides{
			EnableMultimodel: &enabled,
		},
		FolderID: "10000000-0000-4000-8000-000000000001",
	}

	expectedKeys := knowledgeCreateKeySet(
		"url",
		"file_name",
		"file_type",
		"enable_multimodel",
		"title",
		"tag_ids",
		"channel",
		"process_config",
		"folder_id",
	)
	requireKnowledgeCreateDTOExactFields(t, CreateURLKnowledgeRequest{}, expectedKeys)
	require.Equal(t, expectedKeys, knowledgeCreateJSONKeys(t, request))
}

func TestCreateManualKnowledgeRequestHasExactJSONKeys(t *testing.T) {
	enabled := true
	request := CreateManualKnowledgeRequest{
		Title:   "Manual title",
		Content: "# Manual content",
		Status:  types.ManualKnowledgeStatusPublish,
		TagIDs:  []string{"tag-1", "tag-2"},
		Channel: "api",
		ProcessConfig: &types.KnowledgeProcessOverrides{
			EnableMultimodel: &enabled,
		},
		FolderID: "10000000-0000-4000-8000-000000000001",
	}

	expectedKeys := knowledgeCreateKeySet(
		"title",
		"content",
		"status",
		"tag_ids",
		"channel",
		"process_config",
		"folder_id",
	)
	requireKnowledgeCreateDTOExactFields(t, CreateManualKnowledgeRequest{}, expectedKeys)
	require.Equal(t, expectedKeys, knowledgeCreateJSONKeys(t, request))
}

func TestKnowledgeCreateRequestsPreserveFolderIDBinding(t *testing.T) {
	const folderID = "10000000-0000-4000-8000-000000000001"
	tests := []struct {
		name         string
		urlBody      string
		manualBody   string
		wantFolderID string
	}{
		{
			name:         "omitted folder is root",
			urlBody:      `{"url":"https://example.com"}`,
			manualBody:   `{"title":"Manual"}`,
			wantFolderID: "",
		},
		{
			name:         "explicit empty folder is root",
			urlBody:      `{"url":"https://example.com","folder_id":""}`,
			manualBody:   `{"title":"Manual","folder_id":""}`,
			wantFolderID: "",
		},
		{
			name:         "non-root folder is preserved",
			urlBody:      `{"url":"https://example.com","folder_id":"` + folderID + `"}`,
			manualBody:   `{"title":"Manual","folder_id":"` + folderID + `"}`,
			wantFolderID: folderID,
		},
		{
			name:         "folder whitespace is preserved for service validation",
			urlBody:      `{"url":"https://example.com","folder_id":" ` + folderID + ` "}`,
			manualBody:   `{"title":"Manual","folder_id":" ` + folderID + ` "}`,
			wantFolderID: " " + folderID + " ",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var urlRequest CreateURLKnowledgeRequest
			require.NoError(t, json.Unmarshal([]byte(test.urlBody), &urlRequest))
			require.Equal(t, test.wantFolderID, urlRequest.FolderID)

			var manualRequest CreateManualKnowledgeRequest
			require.NoError(t, json.Unmarshal([]byte(test.manualBody), &manualRequest))
			require.Equal(t, test.wantFolderID, manualRequest.FolderID)
		})
	}
}

func TestKnowledgeCreateRequestsDoNotAcceptFolderVersions(t *testing.T) {
	const body = `{
		"url":"https://example.com",
		"title":"Manual",
		"folder_id":"10000000-0000-4000-8000-000000000001",
		"folder_version":99,
		"folder_indexed_version":98
	}`

	var urlRequest CreateURLKnowledgeRequest
	require.NoError(t, json.Unmarshal([]byte(body), &urlRequest))
	urlKeys := knowledgeCreateJSONKeys(t, urlRequest)
	require.NotContains(t, urlKeys, "folder_version")
	require.NotContains(t, urlKeys, "folder_indexed_version")

	var manualRequest CreateManualKnowledgeRequest
	require.NoError(t, json.Unmarshal([]byte(body), &manualRequest))
	manualKeys := knowledgeCreateJSONKeys(t, manualRequest)
	require.NotContains(t, manualKeys, "folder_version")
	require.NotContains(t, manualKeys, "folder_indexed_version")
}

func TestCreateManualKnowledgeRequestMapperCopiesEveryPayloadFieldWithoutMutation(t *testing.T) {
	enabled := true
	processConfig := &types.KnowledgeProcessOverrides{
		EnableMultimodel: &enabled,
		ParserEngineOverrides: map[string]string{
			"pdf_force_scanned": "true",
		},
	}
	request := CreateManualKnowledgeRequest{
		Title:         "Manual title",
		Content:       "# Manual content",
		Status:        types.ManualKnowledgeStatusPublish,
		TagIDs:        []string{"tag-1", "tag-2"},
		Channel:       "api",
		ProcessConfig: processConfig,
		FolderID:      "10000000-0000-4000-8000-000000000001",
	}
	before, err := json.Marshal(request)
	require.NoError(t, err)

	payload := request.ToManualKnowledgePayload()

	require.Equal(t, &types.ManualKnowledgePayload{
		Title:         request.Title,
		Content:       request.Content,
		Status:        request.Status,
		TagIDs:        []string{"tag-1", "tag-2"},
		Channel:       request.Channel,
		ProcessConfig: processConfig,
	}, payload)
	require.Same(t, processConfig, payload.ProcessConfig)

	after, err := json.Marshal(request)
	require.NoError(t, err)
	require.Equal(t, before, after)

	payload.TagIDs[0] = "changed-after-mapping"
	require.Equal(t, []string{"tag-1", "tag-2"}, request.TagIDs)

	payloadKeys := knowledgeCreateJSONKeys(t, payload)
	require.NotContains(t, payloadKeys, "folder_id")
	require.NotContains(t, payloadKeys, "folder_version")
	require.NotContains(t, payloadKeys, "folder_indexed_version")
}

func requireKnowledgeCreateDTOExactFields(
	t *testing.T,
	value interface{},
	expectedKeys map[string]struct{},
) {
	t.Helper()
	valueType := reflect.TypeOf(value)
	require.Equal(t, len(expectedKeys), valueType.NumField())

	actualKeys := make(map[string]struct{}, valueType.NumField())
	for index := 0; index < valueType.NumField(); index++ {
		field := valueType.Field(index)
		require.False(t, field.Anonymous)
		jsonKey := strings.Split(field.Tag.Get("json"), ",")[0]
		require.NotEmpty(t, jsonKey)
		require.NotEqual(t, "-", jsonKey)
		actualKeys[jsonKey] = struct{}{}
	}
	require.Equal(t, expectedKeys, actualKeys)
}

func knowledgeCreateJSONKeys(t *testing.T, value interface{}) map[string]struct{} {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &object))

	keys := make(map[string]struct{}, len(object))
	for key := range object {
		keys[key] = struct{}{}
	}
	return keys
}

func knowledgeCreateKeySet(keys ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		result[key] = struct{}{}
	}
	return result
}

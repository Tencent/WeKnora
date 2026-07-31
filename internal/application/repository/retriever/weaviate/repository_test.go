package weaviate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	weaviateclient "github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate/entities/models"
)

func TestClassHasProperty(t *testing.T) {
	class := &models.Class{
		Properties: []*models.Property{
			{Name: fieldKnowledgeBaseID},
			{Name: fieldFolderID},
		},
	}

	assert.True(t, classHasProperty(class, fieldFolderID))
	assert.False(t, classHasProperty(class, "missing"))
	assert.False(t, classHasProperty(nil, fieldFolderID))
}

func TestFolderProperty(t *testing.T) {
	property := folderProperty(true)

	assert.Equal(t, fieldFolderID, property.Name)
	assert.Equal(t, []string{"text"}, property.DataType)
	assert.Equal(t, models.PropertyTokenizationField, property.Tokenization)
	if assert.NotNil(t, property.IndexFilterable) {
		assert.True(t, *property.IndexFilterable)
	}
}

func TestValidateFolderProperty(t *testing.T) {
	filterable := true
	notFilterable := false

	tests := []struct {
		name     string
		property *models.Property
		wantErr  string
	}{
		{
			name: "valid",
			property: &models.Property{
				Name:            fieldFolderID,
				DataType:        []string{"text"},
				IndexFilterable: &filterable,
				Tokenization:    models.PropertyTokenizationField,
			},
		},
		{
			name:     "missing",
			property: nil,
			wantErr:  "missing required folder_id property",
		},
		{
			name:     "wrong data type",
			property: &models.Property{Name: fieldFolderID, DataType: []string{"uuid"}, IndexFilterable: &filterable},
			wantErr:  "incompatible folder_id data type",
		},
		{
			name:     "filterable flag missing",
			property: &models.Property{Name: fieldFolderID, DataType: []string{"text"}},
			wantErr:  "non-filterable folder_id property",
		},
		{
			name:     "not filterable",
			property: &models.Property{Name: fieldFolderID, DataType: []string{"text"}, IndexFilterable: &notFilterable},
			wantErr:  "non-filterable folder_id property",
		},
		{
			name: "wrong tokenization",
			property: &models.Property{
				Name:            fieldFolderID,
				DataType:        []string{"text"},
				IndexFilterable: &filterable,
				Tokenization:    models.PropertyTokenizationWord,
			},
			wantErr: "incompatible folder_id tokenization",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFolderProperty("Weknora_embeddings_1024", tt.property)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestPropertyAlreadyExistsErrorDetection(t *testing.T) {
	assert.True(t, isPropertyAlreadyExistsErr(errors.New("property already exists")))
	assert.True(t, isPropertyAlreadyExistsErr(errors.New("Property Already Exist")))
	assert.False(t, isPropertyAlreadyExistsErr(errors.New("permission denied")))
	assert.False(t, isPropertyAlreadyExistsErr(nil))
}

func TestBatchUpdateChunkFolderIDUsesStoredObjectIDAcrossDimensionCollections(t *testing.T) {
	chunkID := "8f7503df-a4f8-48f2-81e8-8e241090d0f6"
	objectID := "73b7696a-556d-4614-853f-355f5009343e"
	mutations := make([]string, 0, 1)
	mutationBodies := make([]string, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/schema":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"classes":[{"class":"Weknora_embeddings_1024"},{"class":"Weknora_embeddings_2048"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/meta":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"1.37.3"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/graphql":
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(string(body), "Weknora_embeddings_1024") {
				_, _ = w.Write([]byte(`{"data":{"Get":{"Weknora_embeddings_1024":[{"chunk_id":"` + chunkID + `","_additional":{"id":"` + objectID + `"}}]}}}`))
				return
			}
			if strings.Contains(string(body), "Weknora_embeddings_2048") {
				_, _ = w.Write([]byte(`{"data":{"Get":{"Weknora_embeddings_2048":[]}}}`))
				return
			}
			http.Error(w, "unexpected GraphQL class", http.StatusBadRequest)
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, objectID):
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			mutations = append(mutations, r.Method+" "+r.URL.Path)
			mutationBodies = append(mutationBodies, string(body))
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := weaviateclient.NewClient(weaviateclient.Config{
		Host:             strings.TrimPrefix(server.URL, "http://"),
		Scheme:           "http",
		ConnectionClient: server.Client(),
	})
	require.NoError(t, err)
	repository := &weaviateRepository{
		client:             client,
		collectionBaseName: defaultCollectionName,
	}

	err = repository.BatchUpdateChunkFolderID(context.Background(), map[string]string{
		chunkID: "",
	})

	require.NoError(t, err)
	require.Equal(t, 1, len(mutations))
	assert.Equal(t, http.MethodPatch+" /v1/objects/Weknora_embeddings_1024/"+objectID, mutations[0])
	require.Len(t, mutationBodies, 1)
	assert.JSONEq(t, `{
		"class":"Weknora_embeddings_1024",
		"id":"`+objectID+`",
		"properties":{"folder_id":""}
	}`, mutationBodies[0])
}

func TestCopyQueryObjectsPropagatesGraphQLErrors(t *testing.T) {
	result := &models.GraphQLResponse{
		Errors: []*models.GraphQLError{
			{Message: "source collection is unavailable"},
			{Message: "query timed out"},
		},
	}

	_, err := copyQueryObjects(result, "Weknora_embeddings_1024")

	assert.ErrorContains(t, err, "source collection is unavailable; query timed out")
}

func TestCopyQueryObjectsRejectsMalformedData(t *testing.T) {
	tests := []struct {
		name    string
		result  *models.GraphQLResponse
		wantErr string
	}{
		{name: "nil response", wantErr: "returned no response"},
		{
			name:    "missing Get",
			result:  &models.GraphQLResponse{Data: map[string]models.JSONObject{}},
			wantErr: "returned no Get data",
		},
		{
			name: "wrong Get type",
			result: &models.GraphQLResponse{Data: map[string]models.JSONObject{
				"Get": "invalid",
			}},
			wantErr: "malformed Get data",
		},
		{
			name: "wrong collection type",
			result: &models.GraphQLResponse{Data: map[string]models.JSONObject{
				"Get": map[string]interface{}{"Weknora_embeddings_1024": "invalid"},
			}},
			wantErr: "malformed collection data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := copyQueryObjects(tt.result, "Weknora_embeddings_1024")
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

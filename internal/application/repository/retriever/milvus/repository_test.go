package milvus

import (
	"context"
	"errors"
	"testing"

	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/stretchr/testify/require"
)

func TestUpdateChunkEnabledStatusInCollectionSkipsEmptyChunkIDs(t *testing.T) {
	repo := &milvusRepository{}

	require.NoError(t, repo.updateChunkEnabledStatusInCollection(
		context.Background(),
		"weknora_embeddings_1024",
		nil,
		false,
	))
	require.NoError(t, repo.updateChunkEnabledStatusInCollection(
		context.Background(),
		"weknora_embeddings_1024",
		[]string{},
		true,
	))
}

func TestUpdateChunkEnabledStatusInCollectionsPropagatesFailure(t *testing.T) {
	wantErr := errors.New("upsert failed")
	err := updateChunkEnabledStatusInCollections(
		context.Background(),
		[]string{"other_collection", "weknora_embeddings_1024"},
		"weknora_embeddings",
		nil,
		[]string{"chunk-1"},
		func(_ context.Context, collection string, _ []string, enabled bool) error {
			if collection == "weknora_embeddings_1024" && !enabled {
				return wantErr
			}
			return nil
		},
	)
	require.ErrorIs(t, err, wantErr)
}

func TestSchemaHasField(t *testing.T) {
	schema := entity.NewSchema().
		WithField(entity.NewField().WithName(fieldKnowledgeID)).
		WithField(entity.NewField().WithName(fieldFolderID))

	require.True(t, schemaHasField(schema, fieldFolderID))
	require.False(t, schemaHasField(schema, "missing"))
	require.False(t, schemaHasField(nil, fieldFolderID))
}

func TestNewFolderFieldAllowsWritesFromOlderWorkers(t *testing.T) {
	field := newFolderField()

	require.True(t, field.Nullable)
	require.NotNil(t, field.DefaultValue)
	require.Equal(t, "", field.DefaultValue.GetStringData())
	require.NoError(t, validateFolderField("weknora_embeddings_1024", field))
}

func TestValidateFolderField(t *testing.T) {
	tests := []struct {
		name    string
		field   *entity.Field
		wantErr string
	}{
		{
			name: "new collection field",
			field: entity.NewField().
				WithName(fieldFolderID).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(36),
			wantErr: "must be nullable",
		},
		{
			name: "migrated collection field",
			field: entity.NewField().
				WithName(fieldFolderID).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(36).
				WithNullable(true).
				WithDefaultValueString(""),
		},
		{
			name:    "missing field",
			wantErr: "missing required folder_id field",
		},
		{
			name: "wrong type",
			field: entity.NewField().
				WithName(fieldFolderID).
				WithDataType(entity.FieldTypeInt64),
			wantErr: "incompatible folder_id data type",
		},
		{
			name: "too short",
			field: entity.NewField().
				WithName(fieldFolderID).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(8),
			wantErr: "expected at least 36",
		},
		{
			name: "non-nullable with root default",
			field: entity.NewField().
				WithName(fieldFolderID).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(36).
				WithDefaultValueString(""),
			wantErr: "must be nullable",
		},
		{
			name: "nullable without root default",
			field: entity.NewField().
				WithName(fieldFolderID).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(36).
				WithNullable(true),
			wantErr: "without an empty-string default",
		},
		{
			name: "wrong default",
			field: entity.NewField().
				WithName(fieldFolderID).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(36).
				WithDefaultValueString("legacy"),
			wantErr: "incompatible folder_id default",
		},
		{
			name: "wrong default type",
			field: entity.NewField().
				WithName(fieldFolderID).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(36).
				WithDefaultValueInt(0),
			wantErr: "incompatible folder_id default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFolderField("weknora_embeddings_1024", tt.field)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestIsScalarFilterIndex(t *testing.T) {
	for _, indexType := range []index.IndexType{
		index.AUTOINDEX,
		index.Trie,
		index.Sorted,
		index.Inverted,
		index.BITMAP,
	} {
		require.True(t, isScalarFilterIndex(indexType), indexType)
	}
	require.False(t, isScalarFilterIndex(index.HNSW))
}

package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestApplyKBStorageBindingPreservesOmittedStorage(t *testing.T) {
	for _, bound := range []bool{false, true} {
		for _, body := range []string{
			`{"llmModelId":"new-model"}`,
			`{"llmModelId":"new-model","storageProvider":"","storageBackendId":""}`,
			`{"llmModelId":"new-model","storageProvider":"  ","storageBackendId":"  "}`,
		} {
			name := "legacy/" + body
			if bound {
				name = "bound/" + body
			}
			t.Run(name, func(t *testing.T) {
				// Even if local is disallowed, a model-only update must succeed.
				t.Setenv("STORAGE_ALLOW_LIST", "oss")
				kb := &types.KnowledgeBase{ID: "kb-1"}
				kb.SetStorageProvider("oss")
				if bound {
					backendID := "oss-backend"
					kb.StorageBackendID = &backendID
				}
				before, err := json.Marshal(kb)
				require.NoError(t, err)
				var req KBModelConfigRequest
				require.NoError(t, json.Unmarshal([]byte(body), &req))
				// Nil services ensure this no-op never resolves or queries storage.
				h := &InitializationHandler{}
				require.NoError(t, h.applyKBStorageBinding(context.Background(), kb, kb.ID, &req))
				after, err := json.Marshal(kb)
				require.NoError(t, err)
				require.JSONEq(t, string(before), string(after))
			})
		}
	}
}

type partialStorageResolver struct {
	interfaces.StorageBackendResolver
	called bool
}

func (r *partialStorageResolver) ResolveBackend(context.Context, *types.Tenant, string, string) (*types.StorageBackend, error) {
	r.called = true
	return &types.StorageBackend{ID: "oss-backend", Provider: "oss"}, nil
}

func TestApplyKBStorageBindingStillHandlesExplicitStorage(t *testing.T) {
	t.Setenv("STORAGE_ALLOW_LIST", "oss,local")
	t.Run("explicit provider", func(t *testing.T) {
		kb := &types.KnowledgeBase{ID: "kb-1"}
		kb.SetStorageProvider("oss")
		h := &InitializationHandler{}
		require.NoError(t, h.applyKBStorageBinding(context.Background(), kb, kb.ID,
			&KBModelConfigRequest{StorageProvider: " OSS "}))
		require.Equal(t, "oss", kb.StorageProviderConfig.Provider)
	})
	t.Run("disallowed provider", func(t *testing.T) {
		h := &InitializationHandler{}
		require.Error(t, h.applyKBStorageBinding(context.Background(), &types.KnowledgeBase{}, "kb-1",
			&KBModelConfigRequest{StorageProvider: "minio"}))
	})
	t.Run("explicit backend derives provider", func(t *testing.T) {
		kb := &types.KnowledgeBase{ID: "kb-1"}
		kb.SetStorageProvider("oss")
		resolver := &partialStorageResolver{}
		h := &InitializationHandler{storageResolver: resolver}
		require.NoError(t, h.applyKBStorageBinding(context.Background(), kb, kb.ID,
			&KBModelConfigRequest{StorageBackendID: "oss-backend"}))
		require.True(t, resolver.called)
		require.NotNil(t, kb.StorageBackendID)
		require.Equal(t, "oss-backend", *kb.StorageBackendID)
		require.Equal(t, "oss", kb.GetStorageProvider())
	})
}

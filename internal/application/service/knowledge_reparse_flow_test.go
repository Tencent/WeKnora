package service

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reparseGraphRepository struct {
	interfaces.RetrieveGraphRepository
	fullDeletes    int
	preciseDeletes [][]string
	fencedAttempts []int
	calls          []string
	fenceErr       error
	recoveryErr    error
}

func (r *reparseGraphRepository) FenceGraphAttempt(
	_ context.Context,
	_ types.NameSpace,
	attempt int,
) error {
	r.calls = append(r.calls, "fence")
	r.fencedAttempts = append(r.fencedAttempts, attempt)
	return r.fenceErr
}

func (r *reparseGraphRepository) RecoverGraphNamespace(
	_ context.Context,
	_ types.NameSpace,
) error {
	r.calls = append(r.calls, "recover")
	return r.recoveryErr
}

func (r *reparseGraphRepository) DelGraph(context.Context, []types.NameSpace) error {
	r.calls = append(r.calls, "full-delete")
	r.fullDeletes++
	return nil
}

func (r *reparseGraphRepository) DelGraphChunks(
	_ context.Context,
	_ types.NameSpace,
	ids []string,
) error {
	r.calls = append(r.calls, "precise-delete")
	r.preciseDeletes = append(r.preciseDeletes, append([]string(nil), ids...))
	return nil
}

func TestCleanupReparseGraphSelectsPreciseOrFullRetraction(t *testing.T) {
	namespace := types.NameSpace{KnowledgeBase: "kb-1", Knowledge: "knowledge-1"}
	t.Run("enabled with stale chunks", func(t *testing.T) {
		repository := &reparseGraphRepository{}
		require.NoError(t, cleanupReparseGraph(
			context.Background(), repository, namespace, 7, true, []string{"chunk-1"},
		))
		assert.Equal(t, []int{7}, repository.fencedAttempts)
		assert.Equal(t, []string{"fence", "recover", "precise-delete"}, repository.calls)
		assert.Zero(t, repository.fullDeletes)
		assert.Equal(t, [][]string{{"chunk-1"}}, repository.preciseDeletes)
	})
	t.Run("enabled without stale chunks", func(t *testing.T) {
		repository := &reparseGraphRepository{}
		require.NoError(t, cleanupReparseGraph(
			context.Background(), repository, namespace, 8, true, nil,
		))
		assert.Equal(t, []int{8}, repository.fencedAttempts)
		assert.Equal(t, []string{"fence", "recover"}, repository.calls)
		assert.Zero(t, repository.fullDeletes)
		assert.Empty(t, repository.preciseDeletes)
	})
	t.Run("disabled", func(t *testing.T) {
		repository := &reparseGraphRepository{}
		require.NoError(t, cleanupReparseGraph(
			context.Background(), repository, namespace, 9, false, nil,
		))
		assert.Equal(t, []int{9}, repository.fencedAttempts)
		assert.Equal(t, []string{"fence", "full-delete"}, repository.calls)
		assert.Equal(t, 1, repository.fullDeletes)
		assert.Empty(t, repository.preciseDeletes)
	})
	t.Run("fence failure prevents cleanup", func(t *testing.T) {
		want := errors.New("fence unavailable")
		repository := &reparseGraphRepository{fenceErr: want}

		err := cleanupReparseGraph(
			context.Background(), repository, namespace, 10, true, []string{"chunk-1"},
		)

		require.ErrorIs(t, err, want)
		assert.Equal(t, []string{"fence"}, repository.calls)
		assert.Zero(t, repository.fullDeletes)
		assert.Empty(t, repository.preciseDeletes)
	})
	t.Run("recovery failure prevents cleanup", func(t *testing.T) {
		want := errors.New("recovery failed")
		repository := &reparseGraphRepository{recoveryErr: want}

		err := cleanupReparseGraph(
			context.Background(), repository, namespace, 11, true, []string{"chunk-1"},
		)

		require.ErrorIs(t, err, want)
		assert.Equal(t, []string{"fence", "recover"}, repository.calls)
		assert.Zero(t, repository.fullDeletes)
		assert.Empty(t, repository.preciseDeletes)
	})
}

func TestEffectiveKnowledgeGraphEnabledUsesProcessOverrides(t *testing.T) {
	t.Run("override disables enabled KB graph", func(t *testing.T) {
		disabled := false
		knowledge := &types.Knowledge{}
		require.NoError(t, knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{
			GraphEnabled: &disabled,
		}))
		kb := &types.KnowledgeBase{
			IndexingStrategy: types.IndexingStrategy{GraphEnabled: true},
			ExtractConfig:    &types.ExtractConfig{Enabled: true},
		}

		assert.False(t, effectiveKnowledgeGraphEnabled(kb, knowledge))
	})

	t.Run("override enables disabled KB graph", func(t *testing.T) {
		enabled := true
		knowledge := &types.Knowledge{}
		require.NoError(t, knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{
			GraphEnabled: &enabled,
			ExtractConfig: &types.ExtractConfig{
				Enabled: true,
			},
		}))
		kb := &types.KnowledgeBase{
			ExtractConfig: &types.ExtractConfig{},
		}

		assert.True(t, effectiveKnowledgeGraphEnabled(kb, knowledge))
	})
}

func TestExecuteChunkReconciliation(t *testing.T) {
	t.Run("unchanged indexed chunk skips indexing and reports reuse", func(t *testing.T) {
		desired := []*types.Chunk{{ID: "unchanged", ChunkType: types.ChunkTypeText}}
		require.NoError(t, setDesiredEmbeddingFingerprints(desired, "model-1", 1536, "Document"))

		existing := []*types.Chunk{cloneChunk(desired[0])}
		existing[0].Status = int(types.ChunkStatusIndexed)
		plan, err := planChunkReuse(desired, existing, true)
		require.NoError(t, err)
		require.Len(t, plan.Reuse, 1)

		indexed := -1
		err = executeChunkReconciliation(context.Background(), plan, chunkReconcileOps{
			Create: func(context.Context, []*types.Chunk) error { return nil },
			Index: func(_ context.Context, chunks []*types.Chunk) error {
				indexed = len(chunks)
				return nil
			},
			Update:       func(context.Context, []*types.Chunk) error { return nil },
			DeleteVector: func(context.Context, []string) error { return nil },
			HardDelete:   func(context.Context, []string) error { return nil },
		})

		require.NoError(t, err)
		assert.Zero(t, indexed)
	})

	t.Run("changed fingerprint indexes only the changed chunk", func(t *testing.T) {
		desired := []*types.Chunk{{ID: "changed", ChunkType: types.ChunkTypeText}}
		require.NoError(t, setDesiredEmbeddingFingerprints(desired, "model-new", 1536, "Document"))

		existing := []*types.Chunk{cloneChunk(desired[0])}
		existing[0].Status = int(types.ChunkStatusIndexed)
		require.NoError(t, setDesiredEmbeddingFingerprints(existing, "model-old", 1536, "Document"))
		plan, err := planChunkReuse(desired, existing, true)
		require.NoError(t, err)

		var indexed []string
		err = executeChunkReconciliation(context.Background(), plan, chunkReconcileOps{
			Create: func(context.Context, []*types.Chunk) error { return nil },
			Index: func(_ context.Context, chunks []*types.Chunk) error {
				indexed = chunkIDs(chunks)
				return nil
			},
			Update:       func(context.Context, []*types.Chunk) error { return nil },
			DeleteVector: func(context.Context, []string) error { return nil },
			HardDelete:   func(context.Context, []string) error { return nil },
		})

		require.NoError(t, err)
		assert.Equal(t, []string{"changed"}, indexed)
	})

	t.Run("index failure rolls back only rows created by the attempt", func(t *testing.T) {
		indexErr := errors.New("index failed")
		plan := &chunkReusePlan{
			Create: []*types.Chunk{{ID: "created"}},
			Index:  []*types.Chunk{{ID: "created"}},
			Delete: []*types.Chunk{{ID: "stale-existing"}},
		}
		var vectorDeletes, hardDeletes []string

		err := executeChunkReconciliation(context.Background(), plan, chunkReconcileOps{
			Create: func(context.Context, []*types.Chunk) error { return nil },
			Index:  func(context.Context, []*types.Chunk) error { return indexErr },
			Update: func(context.Context, []*types.Chunk) error {
				t.Fatal("update must not run after failed indexing")
				return nil
			},
			DeleteVector: func(_ context.Context, ids []string) error {
				vectorDeletes = append(vectorDeletes, ids...)
				return nil
			},
			HardDelete: func(_ context.Context, ids []string) error {
				hardDeletes = append(hardDeletes, ids...)
				return nil
			},
		})

		require.ErrorIs(t, err, indexErr)
		assert.Equal(t, []string{"created"}, vectorDeletes)
		assert.Equal(t, []string{"created"}, hardDeletes)
		assert.NotContains(t, hardDeletes, "stale-existing")
	})

	t.Run("success promotes desired rows before stale cleanup", func(t *testing.T) {
		plan := &chunkReusePlan{
			Create:          []*types.Chunk{{ID: "created"}},
			Index:           []*types.Chunk{{ID: "created"}},
			Update:          []*types.Chunk{{ID: "existing"}},
			Delete:          []*types.Chunk{{ID: "stale", ChunkType: types.ChunkTypeText}},
			DeleteGenerated: []*types.Chunk{{ID: "generated", ChunkType: types.ChunkTypeSummary}},
		}
		var calls []string

		err := executeChunkReconciliation(context.Background(), plan, chunkReconcileOps{
			Create: func(context.Context, []*types.Chunk) error {
				calls = append(calls, "create")
				return nil
			},
			Index: func(context.Context, []*types.Chunk) error {
				calls = append(calls, "index")
				return nil
			},
			Update: func(context.Context, []*types.Chunk) error {
				calls = append(calls, "update")
				return nil
			},
			DeleteVector: func(_ context.Context, ids []string) error {
				calls = append(calls, "delete-vector")
				assert.Equal(t, []string{"stale", "generated"}, ids)
				return nil
			},
			DeleteImages: func(context.Context) error {
				calls = append(calls, "delete-images")
				return nil
			},
			DeleteGraph: func(_ context.Context, ids []string) error {
				calls = append(calls, "delete-graph")
				assert.Equal(t, []string{"stale"}, ids)
				return nil
			},
			HardDelete: func(_ context.Context, ids []string) error {
				calls = append(calls, "hard-delete")
				assert.Equal(t, []string{"stale", "generated"}, ids)
				return nil
			},
		})

		require.NoError(t, err)
		assert.Equal(t, []string{
			"create", "index", "update", "delete-vector", "delete-images", "delete-graph", "hard-delete",
		}, calls)
	})

	t.Run("graph deletion failure preserves stale rows for retry", func(t *testing.T) {
		graphErr := errors.New("graph unavailable")
		plan := &chunkReusePlan{Delete: []*types.Chunk{
			{ID: "stale-text", ChunkType: types.ChunkTypeText},
			{ID: "stale-parent", ChunkType: types.ChunkTypeParentText},
		}}
		hardDeleteCalls := 0
		err := executeChunkReconciliation(context.Background(), plan, chunkReconcileOps{
			Create:       func(context.Context, []*types.Chunk) error { return nil },
			Index:        func(context.Context, []*types.Chunk) error { return nil },
			Update:       func(context.Context, []*types.Chunk) error { return nil },
			DeleteVector: func(context.Context, []string) error { return nil },
			DeleteGraph: func(_ context.Context, ids []string) error {
				assert.Equal(t, []string{"stale-text"}, ids)
				return graphErr
			},
			HardDelete: func(context.Context, []string) error {
				hardDeleteCalls++
				return nil
			},
		})

		require.ErrorIs(t, err, graphErr)
		assert.Zero(t, hardDeleteCalls)
	})

	t.Run("image deletion failure preserves rows for retry", func(t *testing.T) {
		imageErr := errors.New("object storage unavailable")
		plan := &chunkReusePlan{Delete: []*types.Chunk{{ID: "stale-image-row"}}}
		hardDeleteCalls := 0
		imageDeleteCalls := 0
		failImages := true
		ops := chunkReconcileOps{
			Create:       func(context.Context, []*types.Chunk) error { return nil },
			Index:        func(context.Context, []*types.Chunk) error { return nil },
			Update:       func(context.Context, []*types.Chunk) error { return nil },
			DeleteVector: func(context.Context, []string) error { return nil },
			DeleteImages: func(context.Context) error {
				imageDeleteCalls++
				if failImages {
					return imageErr
				}
				return nil
			},
			HardDelete: func(context.Context, []string) error {
				hardDeleteCalls++
				return nil
			},
		}

		err := executeChunkReconciliation(context.Background(), plan, ops)
		require.ErrorIs(t, err, imageErr)
		assert.Zero(t, hardDeleteCalls, "source rows must remain as durable retry state")

		failImages = false
		require.NoError(t, executeChunkReconciliation(context.Background(), plan, ops))
		assert.Equal(t, 2, imageDeleteCalls)
		assert.Equal(t, 1, hardDeleteCalls)
	})
}

func TestReparseStorageDelta(t *testing.T) {
	assert.EqualValues(t, 300, reparseStorageDelta(800, 500))
	assert.EqualValues(t, -200, reparseStorageDelta(300, 500))
}

func TestExecuteKnowledgeCommitStopsAfterPersistFailure(t *testing.T) {
	persistErr := errors.New("persist failed")
	enqueued := false
	finalized := false

	err := executeKnowledgeCommit(knowledgeCommitOps{
		Persist: func() error { return persistErr },
		Enqueue: func() {
			enqueued = true
		},
		Finalize: func() {
			finalized = true
		},
	})

	require.ErrorIs(t, err, persistErr)
	assert.False(t, enqueued)
	assert.False(t, finalized)
}

func TestReparseStaleImageURLsExcludeCurrentServingURLs(t *testing.T) {
	plan := &chunkReusePlan{
		Delete: []*types.Chunk{{
			ID:        "stale",
			ImageInfo: `[{"url":"local://images/reused.png"},{"url":"local://images/stale.png"}]`,
		}},
		DeleteGenerated: []*types.Chunk{{
			ID:        "generated",
			ImageInfo: `[{"url":"local://images/generated.png"}]`,
		}},
	}
	stored := []docparser.StoredImage{{ServingURL: "local://images/reused.png"}}

	urls := staleExtractedImageURLs(context.Background(), plan, stored)

	assert.ElementsMatch(t, []string{
		"local://images/stale.png",
		"local://images/generated.png",
	}, urls)
}

func TestDeleteExtractedImagesStrictTreatsMissingObjectAsDeleted(t *testing.T) {
	fileSvc := &countingFileService{deleteErr: fs.ErrNotExist}

	require.NoError(t, deleteExtractedImagesStrict(context.Background(), fileSvc, []string{"local://images/stale.png"}))
	assert.Equal(t, 1, fileSvc.deleteCalls)
}

package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func cloneImageHash(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func cloneRollbackStorage(t *testing.T, f *documentWriteFixture) (interfaces.ResourceCatalog, interfaces.FileService) {
	t.Helper()
	catalog, _ := newResourceCatalogForTest(t)
	physical := filesvc.NewLocalFileService(t.TempDir(), "")
	f.svc.resourceCatalog = catalog
	f.svc.fileSvc = filesvc.NewResourceCatalogFileService(physical, catalog)
	return catalog, physical
}

func requireCloneImageReadable(ctx context.Context, t *testing.T, files interfaces.FileService, ref string) {
	t.Helper()
	r, err := files.GetFile(ctx, ref)
	require.NoError(t, err, "image %s must survive a failed clone", ref)
	defer func() { require.NoError(t, r.Close()) }()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NotEmpty(t, data)
}

func TestCloneRollbackRespectsResourceOwnership(t *testing.T) {
	for _, failDuringCopy := range []bool{false, true} {
		t.Run(fmt.Sprintf("image copy failure=%t", failDuringCopy), func(t *testing.T) {
			t.Setenv("RETRIEVE_DRIVER", "postgres")
			f := transferFixture(t, access.KBTransferClone)
			catalog, physical := cloneRollbackStorage(t, f)
			shared, err := f.svc.fileSvc.SaveBytes(f.ctx, []byte("shared image"), 7, "shared.png", false)
			require.NoError(t, err)
			require.NoError(t, catalog.Bind(
				f.ctx, shared, types.ResourceOwnerKnowledge, "doc", types.ResourceRelationExtractedImage))
			legacy, err := physical.SaveBytes(f.ctx, []byte("legacy image"), 7, "legacy.png", false)
			require.NoError(t, err)
			images := []types.ImageInfo{{URL: shared}, {URL: legacy}}
			if failDuringCopy {
				images = append(images, types.ImageInfo{URL: "local://7/exports/missing.png"})
			}
			require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").
				Update("image_info", mustImageInfoJSON(t, images)).Error)
			f.svc.retrieveEngine = parentChildRetrieveRegistry{engine: &partialCloneEngine{vectors: map[string]int{}}}
			f.svc.modelService = parentChildModelService{embedder: parentChildEmbedder{}}
			f.kbs.values["other"].EmbeddingModelID = "model"
			src, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			err = f.svc.cloneKnowledge(f.ctx, src, f.kbs.values["other"])
			if failDuringCopy {
				require.ErrorContains(t, err, "failed to copy chunk image")
			} else {
				require.ErrorContains(t, err, "partial vector copy")
			}
			requireCloneImageReadable(f.ctx, t, f.svc.fileSvc, shared)
			requireCloneImageReadable(f.ctx, t, physical, legacy)
			refs, err := catalog.ListReferencesByOwner(f.ctx, types.ResourceOwnerKnowledge, "doc")
			require.NoError(t, err)
			require.Equal(t, []string{shared}, refs)
			rows, err := f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
			require.NoError(t, err)
			for _, dst := range rows {
				if dst.ID == "other-doc" {
					continue
				}
				refs, err := catalog.ListReferencesByOwner(f.ctx, types.ResourceOwnerKnowledge, dst.ID)
				require.NoError(t, err)
				require.Empty(t, refs, "failed destination must release its claims")
			}
			// The legacy image needed a new catalog resource. Only the shared
			// source should remain eligible for content-hash reuse after rollback.
			ref, reused, err := catalog.ReuseByContentHash(f.ctx, 7, cloneImageHash([]byte("legacy image")))
			require.NoError(t, err)
			require.False(t, reused, "new destination-only resource must be reclaimed: %s", ref)
		})
	}
}

type cloneCreateFailureRepo struct{ interfaces.KnowledgeRepository }

func (cloneCreateFailureRepo) CreateKnowledge(context.Context, *types.Knowledge) error {
	return errors.New("create clone failed")
}

func TestCloneRollbackPreservesImageSourceFile(t *testing.T) {
	for _, failedCreate := range []bool{false, true} {
		t.Run(fmt.Sprintf("create failure=%t", failedCreate), func(t *testing.T) {
			f := transferFixture(t, access.KBTransferClone)
			catalog, _ := cloneRollbackStorage(t, f)
			ref, err := f.svc.fileSvc.SaveBytes(f.ctx, []byte("image source file"), 7, "source.png", false)
			require.NoError(t, err)
			require.NoError(t, catalog.Bind(
				f.ctx, ref, types.ResourceOwnerKnowledge, "doc", types.ResourceRelationSourceFile))
			require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("file_path", ref).Error)
			images := []types.ImageInfo{{URL: ref}, {URL: "local://7/exports/missing.png"}}
			require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").
				Update("image_info", mustImageInfoJSON(t, images)).Error)
			if failedCreate {
				f.svc.repo = cloneCreateFailureRepo{f.repo}
			}
			src, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			require.Error(t, f.svc.cloneKnowledge(f.ctx, src, f.kbs.values["other"]))
			requireCloneImageReadable(f.ctx, t, f.svc.fileSvc, ref)
			rows, err := f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
			require.NoError(t, err)
			for _, dst := range rows {
				if dst.ID == "other-doc" {
					continue
				}
				refs, err := catalog.ListReferencesByOwner(f.ctx, types.ResourceOwnerKnowledge, dst.ID)
				require.NoError(t, err)
				require.Equal(t, []string{ref}, refs, "persisted destination still owns its source file")
			}
		})
	}
}

// The real repositories and storage run normally; only the selected CreateChunks
// acknowledgement is injected, so the second FAQ batch has deterministic failure.
type faqCloneRollbackChunks struct {
	interfaces.ChunkRepository
	ids        []string
	creates    int
	failCreate int
	commit     bool
	failRead   bool
}

func (r *faqCloneRollbackChunks) ListAllChunksByKnowledgeID(
	ctx context.Context, tenant uint64, id string,
) ([]*types.Chunk, error) {
	if r.failRead && r.creates > 0 {
		return nil, errors.New("read saved FAQ chunks failed")
	}
	return r.ChunkRepository.ListAllChunksByKnowledgeID(ctx, tenant, id)
}

type cloneRollbackCatalogFault struct {
	interfaces.ResourceCatalog
	failList bool
}

func (c cloneRollbackCatalogFault) ListReferencesByOwner(
	ctx context.Context, owner string, ids ...string,
) ([]string, error) {
	if c.failList {
		return nil, errors.New("read resource claims failed")
	}
	return c.ResourceCatalog.ListReferencesByOwner(ctx, owner, ids...)
}

func (cloneRollbackCatalogFault) Release(context.Context, string, string, string) (int64, error) {
	return 0, errors.New("release resource failed")
}

func (r *faqCloneRollbackChunks) FAQChunkDiff(
	context.Context, uint64, string, uint64, string,
) (*types.FAQChunkDiffResult, error) {
	return &types.FAQChunkDiffResult{ChunksToAdd: r.ids}, nil
}

func (r *faqCloneRollbackChunks) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	r.creates++
	if r.creates == r.failCreate {
		if r.commit {
			if err := r.ChunkRepository.CreateChunks(ctx, chunks); err != nil {
				return err
			}
		}
		return errors.New("FAQ batch write failed")
	}
	return r.ChunkRepository.CreateChunks(ctx, chunks)
}

func TestFAQCloneRollbackKeepsSavedImagesAndReclaimsPartialCopies(t *testing.T) {
	for _, scenario := range []string{
		"partial image copy", "saved batch", "existing destination", "lost acknowledgement",
		"uncertain saved rows", "release fails", "owner lookup fails",
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("RETRIEVE_DRIVER", "postgres")
			f := transferFixture(t, access.KBTransferClone)
			catalog, physical := cloneRollbackStorage(t, f)
			for _, kb := range f.kbs.values {
				kb.Type = types.KnowledgeBaseTypeFAQ
			}
			require.NoError(t, f.db.Model(&types.Knowledge{}).Where("tenant_id = ?", 7).
				Update("type", types.KnowledgeTypeFAQ).Error)
			require.NoError(t, f.db.Where("tenant_id = ?", 7).Delete(&types.Chunk{}).Error)
			f.svc.retrieveEngine = parentChildRetrieveRegistry{engine: &cloneCopyEngine{}}
			f.svc.modelService = parentChildModelService{embedder: parentChildEmbedder{}}
			first, err := physical.SaveBytes(f.ctx, []byte("saved image"), 7, "first.png", false)
			require.NoError(t, err)
			alias, err := physical.SaveBytes(f.ctx, []byte("saved image"), 7, "alias.png", false)
			require.NoError(t, err)
			exclusive, err := physical.SaveBytes(f.ctx, []byte("pending image"), 7, "pending.png", false)
			require.NoError(t, err)
			var saved string
			if scenario == "existing destination" {
				saved, err = f.svc.fileSvc.SaveBytes(f.ctx, []byte("saved image"), 7, "existing.png", false)
				require.NoError(t, err)
				require.NoError(t, catalog.Bind(
					f.ctx, saved, types.ResourceOwnerKnowledge, "other-doc", types.ResourceRelationAttachment))
			}
			fault := &faqCloneRollbackChunks{
				ChunkRepository: f.chunkRepo, failCreate: 1, commit: scenario == "lost acknowledgement",
				failRead: scenario == "uncertain saved rows",
			}
			if scenario == "release fails" || scenario == "owner lookup fails" {
				f.svc.resourceCatalog = cloneRollbackCatalogFault{catalog, scenario == "owner lookup fails"}
			}
			if scenario == "saved batch" {
				fault.failCreate = 2
				for i := 0; i < 50; i++ {
					id := fmt.Sprintf("saved-%02d", i)
					fault.ids = append(fault.ids, id)
					require.NoError(t, f.db.Create(&types.Chunk{
						ID: id, TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb", ChunkType: types.ChunkTypeFAQ,
						ImageInfo: mustImageInfoJSON(t, []types.ImageInfo{{URL: first}}),
						Metadata:  types.JSON(`{"standard_question":"question","answers":["answer"]}`),
					}).Error)
				}
			}
			images := []types.ImageInfo{{URL: alias}, {URL: exclusive}}
			if scenario == "partial image copy" {
				images = append(images, types.ImageInfo{URL: "local://7/exports/missing.png"})
			}
			fault.ids = append(fault.ids, "pending")
			require.NoError(t, f.db.Create(&types.Chunk{
				ID: "pending", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb", ChunkType: types.ChunkTypeFAQ,
				ImageInfo: mustImageInfoJSON(t, images),
				Metadata:  types.JSON(`{"standard_question":"question","answers":["answer"]}`),
			}).Error)
			f.svc.chunkRepo = fault
			handledError := false
			err = f.svc.cloneFAQKnowledgeBase(
				f.ctx, f.kbs.values["kb"], f.kbs.values["other"], &types.KBCloneProgress{TaskID: "clone"},
				func(*types.KBCloneProgress, error, string) { handledError = true },
			)
			require.Error(t, err)
			require.True(t, handledError, "clone progress must report the failure")
			if scenario == "owner lookup fails" {
				require.ErrorContains(t, err, "read resource claims failed")
				require.Zero(t, fault.creates, "do not copy or write without knowing existing claims")
				_, reused, err := catalog.ReuseByContentHash(f.ctx, 7, cloneImageHash([]byte("pending image")))
				require.NoError(t, err)
				require.False(t, reused)
				return
			}
			if scenario == "partial image copy" {
				require.ErrorContains(t, err, "failed to copy chunk image")
			} else {
				require.ErrorContains(t, err, "FAQ batch write failed")
			}
			chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(f.ctx, 7, "other-doc")
			require.NoError(t, err)
			for _, chunk := range chunks {
				for _, ref := range collectImageURLs(f.ctx, []string{chunk.ImageInfo}) {
					requireCloneImageReadable(f.ctx, t, f.svc.fileSvc, ref)
				}
			}
			if saved != "" {
				requireCloneImageReadable(f.ctx, t, f.svc.fileSvc, saved)
				refs, err := catalog.ListReferencesByOwner(f.ctx, types.ResourceOwnerKnowledge, "other-doc")
				require.NoError(t, err)
				require.Equal(t, []string{saved}, refs)
			}
			_, reused, err := catalog.ReuseByContentHash(f.ctx, 7, cloneImageHash([]byte("pending image")))
			require.NoError(t, err)
			keep := scenario == "lost acknowledgement" || scenario == "uncertain saved rows" ||
				scenario == "release fails"
			require.Equal(t, keep, reused, "keep saved images and fail closed when ownership is uncertain")
		})
	}
}

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSaveImageRevisionKeepsCopiesAndGalleryConsistent(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		require.NoError(t, db.AutoMigrate(&types.ChunkRevision{}))
		fx := newImageFixture(t, db)
		original := fixtureImage{URL: "local://chart.png", Caption: "old caption", OCRText: "old OCR"}
		parent := fx.add("text", 0, original)
		caption := fx.add("image_caption", 1, original)
		ocr := fx.add("image_ocr", 2, original)
		vector := fx.add("image_vector", 3, original)
		other := fx.add("image_caption", 4, fixtureImage{URL: "local://other.png", Caption: "unrelated"})
		otherDocument := fx.add("image_caption", 5, original)
		require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", otherDocument.ID).
			UpdateColumn("knowledge_id", "other-document").Error)
		repo := NewChunkRepository(db)
		ctx := context.Background()
		// Both edits start from the old image snapshot. Saving the OCR next
		// must merge only OCR, preserving the caption that was already edited.
		for _, edit := range []struct {
			chunk   *types.Chunk
			content string
		}{{caption, "corrected caption"}, {ocr, "corrected OCR"}} {
			old := *edit.chunk
			edit.chunk.Content = edit.content
			edit.chunk.ContentRevision = 1
			edit.chunk.UpdatedAt = time.Now()
			require.NoError(t, repo.SaveChunkRevision(ctx, edit.chunk, &types.ChunkRevision{
				ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: fx.kbID, KnowledgeID: old.KnowledgeID,
				ChunkID: old.ID, Revision: 0, Content: old.Content,
			}, 0))
		}
		for _, row := range []*types.Chunk{parent, caption, ocr, vector} {
			stored, err := repo.GetChunkByID(ctx, 1, row.ID)
			require.NoError(t, err)
			var info []types.ImageInfo
			require.NoError(t, json.Unmarshal([]byte(stored.ImageInfo), &info))
			require.Equal(t, "corrected caption", info[0].Caption)
			require.Equal(t, "corrected OCR", info[0].OCRText)
			if row == parent {
				require.Equal(t, "x", stored.Content, "image edits must not rewrite source text")
			}
			if row == vector {
				require.Equal(t, "corrected caption", stored.Content,
					"rerank evidence follows caption, not a new visual vector")
			}
		}
		stored, err := repo.GetChunkByID(ctx, 1, other.ID)
		require.NoError(t, err)
		require.Equal(t, other.ImageInfo, stored.ImageInfo)
		stored, err = repo.GetChunkByID(ctx, 1, otherDocument.ID)
		require.NoError(t, err)
		require.Equal(t, otherDocument.ImageInfo, stored.ImageInfo, "another document is edited independently")
		// A later update of an older copy must not resurrect stale gallery text.
		require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", parent.ID).
			Update("updated_at", time.Now().Add(time.Minute)).Error)
		rows, _ := fx.list(types.ImageAssetQuery{})
		for _, row := range rows {
			if row.URL == original.URL {
				require.Equal(t, "corrected caption", row.Caption)
				require.Equal(t, "corrected OCR", row.OCRText)
			}
		}
	})
}

func TestSaveImageRevisionRollsBackCopiesWhenSnapshotFails(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		require.NoError(t, db.AutoMigrate(&types.ChunkRevision{}))
		fx := newImageFixture(t, db)
		image := fixtureImage{URL: "local://chart.png", Caption: "old", OCRText: "OCR"}
		parent := fx.add("text", 0, image)
		caption := fx.add("image_caption", 1, image)
		repo := NewChunkRepository(db)
		snapshot := &types.ChunkRevision{
			ID: uuid.NewString(), ChunkID: caption.ID,
			TenantID: 1, KnowledgeID: caption.KnowledgeID, KnowledgeBaseID: fx.kbID,
		}
		require.NoError(t, db.Create(snapshot).Error)
		caption.Content = "rejected edit"
		caption.ContentRevision = 1
		require.Error(t, repo.SaveChunkRevision(context.Background(), caption, snapshot, 0))
		for _, id := range []string{parent.ID, caption.ID} {
			stored, err := repo.GetChunkByID(context.Background(), 1, id)
			require.NoError(t, err)
			var images []types.ImageInfo
			require.NoError(t, json.Unmarshal([]byte(stored.ImageInfo), &images))
			require.Equal(t, "old", images[0].Caption)
			require.Equal(t, 0, stored.ContentRevision)
		}
	})
}

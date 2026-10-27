package repository

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEmptyImageMigrationRemovesOnlyProvenEmptyDerivedChunks(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		fx := newImageFixture(t, db)
		empty := fmt.Sprintf(`{"url":"local://empty.png","sha256":"%x","caption":"请提供图片"}`, sha256.Sum256(nil))
		valid := `{"url":"local://valid.png","sha256":"nonempty","caption":"请提供图片"}`
		var removed, kept []*types.Chunk
		for _, kind := range []string{"image_caption", "image_ocr", "image_vector"} {
			removed = append(removed, fx.addRaw(kind, 0, "["+empty+"]"))
			kept = append(kept, fx.addRaw(kind, 0, "["+valid+"]"))
		}
		kept = append(kept, fx.addRaw("text", 0, "["+empty+"]"))
		for _, raw := range []string{
			"[" + empty + "," + valid + "]", // never delete a mixed chunk
			`[{"url":"local://unknown.png","caption":"请提供图片"}]`,
			`[{"url":"local://unknown.png","sha256":""}]`,
			`[`, `[]`, `{}`, `null`, `"scalar"`,
		} {
			kept = append(kept, fx.addRaw("image_caption", 0, raw))
		}

		path := "../../../migrations/sqlite/000042_remove_empty_image_chunks.up.sql"
		if db.Name() == "postgres" {
			path = "../../../migrations/versioned/000125_remove_empty_image_chunks.up.sql"
		}
		sql, err := os.ReadFile(path)
		require.NoError(t, err)
		for range 2 { // safe to run again; no extra records disappear
			require.NoError(t, db.Exec(string(sql)).Error)
			for _, chunk := range removed {
				var stored types.Chunk
				require.NoError(t, db.Unscoped().First(&stored, "id = ?", chunk.ID).Error)
				require.True(t, stored.DeletedAt.Valid)
				require.Equal(t, chunk.Content, stored.Content, "retain the original row for recovery")
				var count int64
				require.NoError(t, db.Table("chunk_images").Where("chunk_id = ?", chunk.ID).Count(&count).Error)
				require.Zero(t, count, "gallery projection must disappear with the source chunk")
			}
			for _, chunk := range kept {
				var count int64
				require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", chunk.ID).Count(&count).Error)
				require.EqualValues(t, 1, count, "must preserve %s: %s", chunk.ChunkType, chunk.ImageInfo)
			}
		}
	})
}

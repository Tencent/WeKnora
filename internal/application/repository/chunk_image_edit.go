package repository

import (
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Image metadata is copied onto source, OCR, caption and visual-vector chunks.
// Lock in a stable order before updating any row so concurrent OCR/caption
// edits merge their fields rather than restoring each other's old snapshots.
func syncEditedImageCopies(tx *gorm.DB, edited *types.Chunk, expectedRevision int) error {
	var rows []*types.Chunk
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ? AND image_info <> ''",
			edited.TenantID, edited.KnowledgeBaseID, edited.KnowledgeID).
		Order("id").Find(&rows).Error; err != nil {
		return err
	}
	var key string
	for _, row := range rows {
		if row.ID != edited.ID {
			continue
		}
		if row.ContentRevision != expectedRevision {
			return ErrChunkRevisionConflict
		}
		var images []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(row.ImageInfo), &images); err != nil || len(images) != 1 {
			return fmt.Errorf("image chunk must describe exactly one image")
		}
		key = editedImageKey(images[0])
	}
	if key == "" {
		return fmt.Errorf("image chunk has no image reference")
	}
	field := "caption"
	if edited.ChunkType == types.ChunkTypeImageOCR {
		field = "ocr_text"
	}
	value, _ := json.Marshal(edited.Content)
	for _, row := range rows {
		var images []map[string]json.RawMessage
		if json.Unmarshal([]byte(row.ImageInfo), &images) != nil {
			continue // An unrelated legacy image entry must not prevent this edit.
		}
		changed := false
		fields := map[string]interface{}{}
		for _, image := range images {
			if editedImageKey(image) != key {
				continue
			}
			image[field] = value
			changed = true
			if row.ChunkType == types.ChunkTypeImageVector {
				var content string
				_ = json.Unmarshal(image["caption"], &content)
				if content == "" {
					_ = json.Unmarshal(image["ocr_text"], &content)
				}
				fields["content"] = content // Preserve the indexed visual vector.
			}
		}
		if !changed {
			continue
		}
		info, err := json.Marshal(images)
		if err != nil {
			return err
		}
		fields["image_info"] = string(info)
		if err := tx.Model(&types.Chunk{}).Where("id = ? AND tenant_id = ?", row.ID, edited.TenantID).
			UpdateColumns(fields).Error; err != nil {
			return err
		}
		if row.ID == edited.ID {
			edited.ImageInfo = string(info)
		}
	}
	return nil
}

func editedImageKey(image map[string]json.RawMessage) string {
	var key string
	_ = json.Unmarshal(image["url"], &key)
	if key == "" {
		_ = json.Unmarshal(image["original_url"], &key)
	}
	return key
}

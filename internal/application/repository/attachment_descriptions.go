package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/application/attachment"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

const (
	descriptionMetadataKey   = types.TemporaryDocumentImageDescriptionsKey
	maxDocumentDescriptions  = 64
	maxDescriptionCacheBytes = 1 << 20
)

// Descriptions live with the document, inherit its retention/deletion policy,
// and do not depend on a sandbox or introduce a second document lifecycle.
type attachmentDescriptionRepository struct {
	db       *gorm.DB
	tenantID uint64
}

// NewAttachmentDescriptionRepository binds the observation cache to its owning tenant.
func NewAttachmentDescriptionRepository(db *gorm.DB, tenantID uint64) attachment.DescriptionCache {
	return &attachmentDescriptionRepository{db: db, tenantID: tenantID}
}

func (r *attachmentDescriptionRepository) loadMetadata(
	ctx context.Context, id string,
) (*types.TemporaryDocument, error) {
	var doc types.TemporaryDocument
	err := r.db.WithContext(ctx).Select("metadata").Where("tenant_id = ? AND id = ?", r.tenantID, id).First(&doc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &doc, err
}

func (r *attachmentDescriptionRepository) Get(ctx context.Context, id, key string) (*attachment.Description, error) {
	doc, err := r.loadMetadata(ctx, id)
	if err != nil || doc == nil {
		return nil, err
	}
	_, entries, err := decodeDescriptions(doc.Metadata)
	if err != nil {
		return nil, err
	}
	value, ok := entries[key]
	if !ok {
		return nil, nil
	}
	return &value, nil
}

func decodeDescriptions(raw types.JSON) (map[string]json.RawMessage, map[string]attachment.Description, error) {
	metadata := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return nil, nil, err
		}
	}
	if metadata == nil {
		metadata = map[string]json.RawMessage{}
	}
	entries := map[string]attachment.Description{}
	if value := metadata[descriptionMetadataKey]; len(value) > 0 {
		if err := json.Unmarshal(value, &entries); err != nil {
			return nil, nil, err
		}
	}
	if entries == nil {
		entries = map[string]attachment.Description{}
	}
	return metadata, entries, nil
}

func (r *attachmentDescriptionRepository) Put(ctx context.Context, id, key string, value attachment.Description) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > maxDescriptionCacheBytes/4 {
		return fmt.Errorf("image observation exceeds cache size limit")
	}
	// Compare-and-swap preserves parser metadata and concurrent image writes on
	// SQLite, PostgreSQL and MySQL without JSON dialect-specific mutation SQL.
	for attempt := 0; attempt < 8; attempt++ {
		doc, err := r.loadMetadata(ctx, id)
		if err != nil {
			return err
		}
		if doc == nil {
			return fmt.Errorf("attachment no longer exists")
		}
		metadata, entries, err := decodeDescriptions(doc.Metadata)
		if err != nil {
			return err
		}
		entries[key] = value
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return entries[keys[i]].CreatedAt.Before(entries[keys[j]].CreatedAt) })
		for {
			encoded, err = json.Marshal(entries)
			if err != nil {
				return err
			}
			if len(entries) <= maxDocumentDescriptions && len(encoded) <= maxDescriptionCacheBytes {
				break
			}
			delete(entries, keys[0])
			keys = keys[1:]
		}
		metadata[descriptionMetadataKey] = encoded
		updated, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		cast := "CAST(metadata AS TEXT)"
		if r.db.Name() == "mysql" {
			cast = "CAST(metadata AS CHAR)"
		}
		query := r.db.WithContext(ctx).Model(&types.TemporaryDocument{}).
			Where("tenant_id = ? AND id = ?", r.tenantID, id)
		if len(doc.Metadata) == 0 {
			query = query.Where("metadata IS NULL")
		} else {
			query = query.Where(cast+" = ?", string(doc.Metadata))
		}
		result := query.UpdateColumn("metadata", types.JSON(updated))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return nil
		}
	}
	return fmt.Errorf("attachment description changed concurrently; retry the read")
}

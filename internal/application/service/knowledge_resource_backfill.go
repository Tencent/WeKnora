package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

const knowledgeResourceBindingBackfillBatch = 200

type knowledgeResourceBackfillRow struct {
	ID          string
	TenantID    uint64
	KnowledgeID string
	Content     string
	ImageInfo   string
}

// BackfillKnowledgeResourceBindings claims resource:// handles already stored
// in live chunk text / ImageInfo. Binding-based KB file authorization landed
// without the document parser writing those rows, so existing installs 403
// every extracted image until this repair runs.
//
// Idempotent: Bind uses ON CONFLICT DO NOTHING, and a data_patches row skips
// the chunk scan after a successful pass.
func BackfillKnowledgeResourceBindings(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if !db.Migrator().HasTable("data_patches") ||
		!db.Migrator().HasTable("chunks") ||
		!db.Migrator().HasTable("resources") ||
		!db.Migrator().HasTable("resource_bindings") {
		return nil
	}

	var applied int64
	if err := db.WithContext(ctx).Model(&types.DataPatch{}).
		Where("id = ?", types.DataPatchKnowledgeResourceBindingsV1).
		Count(&applied).Error; err != nil {
		return fmt.Errorf("check knowledge resource binding backfill: %w", err)
	}
	if applied > 0 {
		return nil
	}

	catalog := NewResourceCatalog(repository.NewResourceRepository(db))
	svc := &knowledgeService{resourceCatalog: catalog}

	lastID := ""
	for {
		var rows []knowledgeResourceBackfillRow
		q := db.WithContext(ctx).Model(&types.Chunk{}).
			Select("id", "tenant_id", "knowledge_id", "content", "image_info").
			Where("content LIKE ? OR image_info LIKE ?", "%"+types.ResourceScheme+"%", "%"+types.ResourceScheme+"%").
			Order("id").
			Limit(knowledgeResourceBindingBackfillBatch)
		if lastID != "" {
			q = q.Where("id > ?", lastID)
		}
		if err := q.Find(&rows).Error; err != nil {
			return fmt.Errorf("scan chunks for resource bindings: %w", err)
		}
		if len(rows) == 0 {
			break
		}

		liveTenants, err := liveKnowledgeTenants(ctx, db, rows)
		if err != nil {
			return err
		}
		for _, row := range rows {
			lastID = row.ID
			tenantID, ok := liveTenants[row.KnowledgeID]
			if !ok || tenantID != row.TenantID {
				continue
			}
			svc.bindContentResources(ctx, tenantID, row.KnowledgeID, row.Content+"\n"+row.ImageInfo)
		}
	}

	if err := db.WithContext(ctx).Create(&types.DataPatch{
		ID:        types.DataPatchKnowledgeResourceBindingsV1,
		AppliedAt: time.Now().UTC(),
	}).Error; err != nil {
		return fmt.Errorf("record knowledge resource binding backfill: %w", err)
	}
	logger.Infof(ctx, "Backfilled knowledge resource bindings from chunk content")
	return nil
}

func liveKnowledgeTenants(
	ctx context.Context, db *gorm.DB, rows []knowledgeResourceBackfillRow,
) (map[string]uint64, error) {
	ids := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.KnowledgeID]; ok || row.KnowledgeID == "" {
			continue
		}
		seen[row.KnowledgeID] = struct{}{}
		ids = append(ids, row.KnowledgeID)
	}
	if len(ids) == 0 {
		return map[string]uint64{}, nil
	}
	var knowledges []types.Knowledge
	if err := db.WithContext(ctx).Select("id", "tenant_id").
		Where("id IN ?", ids).
		Find(&knowledges).Error; err != nil {
		return nil, fmt.Errorf("load knowledges for resource binding backfill: %w", err)
	}
	out := make(map[string]uint64, len(knowledges))
	for _, knowledge := range knowledges {
		out[knowledge.ID] = knowledge.TenantID
	}
	return out, nil
}

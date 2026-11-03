package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// ErrKnowledgeFolderChanged indicates a stale folder or document placement snapshot.
var ErrKnowledgeFolderChanged = errors.New("knowledge folder or document placement changed; refresh and retry")

// snapshotKnowledgeFolderPath must run under the KB mutation lock. An explicit
// client anchor is required to exist; a legacy import captures its deepest
// existing ancestor and may create new relative levels beneath it.
func snapshotKnowledgeFolderPath(
	ctx context.Context, tx *gorm.DB, tenant uint64, kb, path string,
) (*types.KnowledgeFolderPathSnapshot, error) {
	if path != "" {
		if err := validateKnowledgeFolderRewrite(path); err != nil {
			return nil, err
		}
	}
	snapshot := &types.KnowledgeFolderPathSnapshot{TenantID: tenant, KnowledgeBaseID: kb, Path: path}
	anchor, explicit := types.KnowledgeFolderAnchorFromContext(ctx)
	if explicit {
		if anchor != "" {
			if err := validateKnowledgeFolderRewrite(anchor); err != nil {
				return nil, err
			}
			if path != anchor && !strings.HasPrefix(path, anchor+"/") {
				return nil, ErrKnowledgeFolderInvalidPath
			}
		}
	} else {
		anchor = path
	}
	for anchor != "" {
		var marker types.KnowledgeFolder
		err := knowledgeFolderScope(tx, tenant, kb, anchor).Order("path").Take(&marker).Error
		if err == nil {
			snapshot.Anchor, snapshot.MarkerPath, snapshot.MarkerCreatedAt = anchor, marker.Path, marker.CreatedAt
			return snapshot, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		var row types.Knowledge
		err = tx.Select("id", "folder_path").
			Where("tenant_id = ? AND knowledge_base_id = ? AND parse_status <> ?",
				tenant, kb, types.ParseStatusDeleting).
			Where("(folder_path = ? OR folder_path LIKE ? ESCAPE ?)",
				anchor, escapeLikeKeyword(anchor)+"/%", likeEscapeChar).
			Order("id").Take(&row).Error
		if err == nil {
			snapshot.Anchor, snapshot.KnowledgeID, snapshot.KnowledgePath = anchor, row.ID, row.FolderPath
			return snapshot, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if explicit {
			return nil, ErrKnowledgeFolderChanged
		}
		anchor = types.KnowledgeFolderParent(anchor)
	}
	return snapshot, nil
}

func validateKnowledgeFolderSnapshot(tx *gorm.DB, snapshot *types.KnowledgeFolderPathSnapshot) error {
	if snapshot == nil {
		return ErrKnowledgeFolderChanged
	}
	if snapshot.Anchor == "" {
		return nil
	}
	var count int64
	var err error
	if snapshot.MarkerPath != "" {
		err = tx.Model(&types.KnowledgeFolder{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND path = ? AND created_at = ?",
				snapshot.TenantID, snapshot.KnowledgeBaseID, snapshot.MarkerPath, snapshot.MarkerCreatedAt).
			Count(&count).Error
	} else if snapshot.KnowledgeID != "" {
		err = tx.Model(&types.Knowledge{}).
			Where("tenant_id = ? AND knowledge_base_id = ? AND id = ? AND folder_path = ? AND parse_status <> ?",
				snapshot.TenantID, snapshot.KnowledgeBaseID, snapshot.KnowledgeID, snapshot.KnowledgePath,
				types.ParseStatusDeleting).
			Count(&count).Error
	}
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrKnowledgeFolderChanged
	}
	return nil
}

func (r *knowledgeRepository) SnapshotKnowledgeFolderPath(
	ctx context.Context, tenant uint64, kb, path string,
) (*types.KnowledgeFolderPathSnapshot, error) {
	var snapshot *types.KnowledgeFolderPathSnapshot
	err := r.withKnowledgeFolderMutation(ctx, tenant, kb, func(tx *gorm.DB) (err error) {
		snapshot, err = snapshotKnowledgeFolderPath(ctx, tx, tenant, kb, path)
		return err
	})
	return snapshot, err
}

func (r *knowledgeRepository) CreateKnowledgeWithFolderSnapshot(
	ctx context.Context, knowledge *types.Knowledge, snapshot *types.KnowledgeFolderPathSnapshot,
) error {
	if snapshot == nil || knowledge == nil || snapshot.TenantID != knowledge.TenantID ||
		snapshot.KnowledgeBaseID != knowledge.KnowledgeBaseID || snapshot.Path != knowledge.FolderPath {
		return ErrKnowledgeFolderChanged
	}
	knowledge.ErrorMessage = common.CleanInvalidUTF8(knowledge.ErrorMessage)
	return r.withKnowledgeFolderMutation(ctx, knowledge.TenantID, knowledge.KnowledgeBaseID, func(tx *gorm.DB) error {
		if err := validateKnowledgeFolderSnapshot(tx, snapshot); err != nil {
			return err
		}
		return tx.Create(knowledge).Error
	})
}

// knowledgeReparseToken reads only the server-reserved ownership identity.
func knowledgeReparseToken(metadata types.JSON) (string, error) {
	if len(metadata) == 0 {
		return "", nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &fields); err != nil {
		return "", err
	}
	raw, present := fields[types.KnowledgeReparseMetadataKey]
	if !present {
		return "", nil
	}
	var token string
	if err := json.Unmarshal(raw, &token); err != nil {
		return "", err
	}
	return token, nil
}

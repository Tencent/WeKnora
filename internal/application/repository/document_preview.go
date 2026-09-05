package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrPreviewGone indicates that the source no longer owns the preview operation.
var ErrPreviewGone = errors.New("preview source no longer active")

// DocumentPreviewRepository persists preview state and artifact cleanup intents.
type DocumentPreviewRepository struct{ db *gorm.DB }

// NewDocumentPreviewRepository creates a persistent preview repository.
func NewDocumentPreviewRepository(db *gorm.DB) *DocumentPreviewRepository {
	return &DocumentPreviewRepository{db: db}
}

func isPreviewSource(k *types.Knowledge) bool {
	return k.FilePath != "" && strings.EqualFold(filepath.Ext(k.FileName), ".doc")
}

func previewStateFor(k *types.Knowledge) types.DocumentPreviewState {
	return types.DocumentPreviewState{
		KnowledgeID: k.ID, KnowledgeBaseID: k.KnowledgeBaseID, SourcePath: k.FilePath,
		SourceHash: k.FileHash, Version: types.DocumentPreviewVersion, Status: "pending", Token: uuid.NewString(),
	}
}

func previewPayload(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func newPreviewOp(k *types.Knowledge) *types.TaskPendingOp {
	return &types.TaskPendingOp{
		TenantID: k.TenantID, TaskType: types.TypeDocumentPreview, Scope: types.TaskScopeKnowledge,
		ScopeID: k.ID, Op: "state", Payload: previewPayload(previewStateFor(k)), EnqueuedAt: time.Now(),
	}
}

// DecodeDocumentPreview decodes the durable state payload of a preview operation.
func DecodeDocumentPreview(op *types.TaskPendingOp) (types.DocumentPreviewState, error) {
	var s types.DocumentPreviewState
	err := json.Unmarshal(op.Payload, &s)
	return s, err
}

func sourceMatches(s types.DocumentPreviewState, k *types.Knowledge) bool {
	return isPreviewSource(k) && s.KnowledgeID == k.ID && s.KnowledgeBaseID == k.KnowledgeBaseID &&
		s.SourcePath == k.FilePath && s.SourceHash == k.FileHash && s.Version == types.DocumentPreviewVersion
}

// UPDATE acquires the same row lock used by deletes, on PostgreSQL/MySQL and
// SQLite alike. Lock KB before knowledge; re-read after locking to reject moves.
func lockPreviewSource(tx *gorm.DB, tenant uint64, id string) (*types.Knowledge, error) {
	var k types.Knowledge
	if err := tx.Where("tenant_id = ? AND id = ?", tenant, id).First(&k).Error; err != nil {
		return nil, err
	}
	kbID := k.KnowledgeBaseID
	res := tx.Model(&types.KnowledgeBase{}).
		Where("tenant_id = ? AND id = ?", tenant, kbID).
		UpdateColumn("id", gorm.Expr("id"))
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrPreviewGone
	}
	res = tx.Model(&types.Knowledge{}).
		Where(
			"tenant_id = ? AND id = ? AND knowledge_base_id = ? AND parse_status <> ?",
			tenant, id, kbID, types.ParseStatusDeleting,
		).
		UpdateColumn("id", gorm.Expr("id"))
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrPreviewGone
	}
	k = types.Knowledge{}
	if err := tx.Where("tenant_id = ? AND id = ?", tenant, id).First(&k).Error; err != nil {
		return nil, err
	}
	if !isPreviewSource(&k) {
		return nil, ErrPreviewGone
	}
	return &k, nil
}

// Ensure creates or refreshes the preview state for the current source version.
func (r *DocumentPreviewRepository) Ensure(
	ctx context.Context,
	tenant uint64,
	id string,
	retry bool,
) (*types.TaskPendingOp, error) {
	var op types.TaskPendingOp
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		k, err := lockPreviewSource(tx, tenant, id)
		if err != nil {
			return err
		}
		err = tx.Where(
			"tenant_id = ? AND task_type = ? AND scope_id = ? AND op = ?",
			tenant, types.TypeDocumentPreview, id, "state",
		).First(&op).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			op = *newPreviewOp(k)
			return tx.Create(&op).Error
		}
		if err != nil {
			return err
		}
		state, err := DecodeDocumentPreview(&op)
		if err != nil {
			return err
		}
		missingResource := false
		if state.Status == "ready" {
			handle, valid := types.ParseResourcePath(state.ResourceRef)
			var count int64
			if valid {
				if err := tx.Model(&types.StoredResource{}).
					Where(
						"tenant_id = ? AND handle = ? AND state = ?",
						tenant, handle, types.ResourceStateActive,
					).
					Count(&count).Error; err != nil {
					return err
				}
			}
			missingResource = count == 0
		}
		retryFailed := retry && state.Status == "failed" && !time.Now().Before(state.NextAttempt)
		if !sourceMatches(state, k) || missingResource || retryFailed {
			state = previewStateFor(k)
			op.Payload = previewPayload(state)
			op.ClaimedAt = nil
			op.FailCount = 0
			return tx.Model(&op).Updates(map[string]any{
				"payload": op.Payload, "claimed_at": nil, "fail_count": 0,
			}).Error
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrPreviewGone
	}
	return &op, err
}

// Load returns one durable preview operation by identifier.
func (r *DocumentPreviewRepository) Load(ctx context.Context, id int64) (*types.TaskPendingOp, error) {
	var op types.TaskPendingOp
	err := r.db.WithContext(ctx).Where("id = ? AND task_type = ?", id, types.TypeDocumentPreview).First(&op).Error
	return &op, err
}

// List returns unfinished or due work. Ready artifacts are checked hourly, or
// promptly after their owner/version disappears, without enqueueing every DOC.
func (r *DocumentPreviewRepository) List(ctx context.Context, after int64) ([]types.TaskPendingOp, error) {
	const stateSource = `EXISTS (SELECT 1 FROM knowledges k
 JOIN knowledge_bases kb ON kb.id=k.knowledge_base_id AND kb.tenant_id=k.tenant_id
 WHERE k.id=task_pending_ops.scope_id AND k.tenant_id=task_pending_ops.tenant_id
 AND k.deleted_at IS NULL AND kb.deleted_at IS NULL AND k.parse_status <> 'deleting'
 AND LOWER(k.file_name) LIKE '%.doc'
 AND k.file_path=(task_pending_ops.payload->>'source_path')
 AND COALESCE(k.file_hash,'')=(task_pending_ops.payload->>'source_hash')
 AND k.knowledge_base_id=(task_pending_ops.payload->>'knowledge_base_id'))`
	const artifactSource = `EXISTS (SELECT 1 FROM task_pending_ops s
 JOIN knowledges k ON k.id=s.scope_id AND k.tenant_id=s.tenant_id
 JOIN knowledge_bases kb ON kb.id=k.knowledge_base_id AND kb.tenant_id=k.tenant_id
 WHERE s.id=CAST(task_pending_ops.payload->>'state_id' AS BIGINT) AND s.task_type='document:preview' AND s.op='state'
 AND s.tenant_id=task_pending_ops.tenant_id AND s.payload->>'token'=task_pending_ops.payload->>'token'
 AND k.deleted_at IS NULL AND kb.deleted_at IS NULL AND k.parse_status <> 'deleting'
 AND k.file_path=s.payload->>'source_path' AND COALESCE(k.file_hash,'')=s.payload->>'source_hash'
 AND k.knowledge_base_id=s.payload->>'knowledge_base_id')`
	now := time.Now().UTC()
	due := `(op='state' AND (
 NOT ` + stateSource + ` OR payload->>'version' <> ? OR
 (payload->>'status'='pending' AND (payload->>'next_attempt' IS NULL OR payload->>'next_attempt' <= ?)
 AND (claimed_at IS NULL OR claimed_at < ?))))
 OR (op='artifact' AND payload->>'not_before' <= ? AND
 (payload->>'next_check' IS NULL OR payload->>'next_check' <= ? OR NOT ` + artifactSource + `))`
	var ops []types.TaskPendingOp
	err := r.db.WithContext(ctx).Where("task_type = ? AND id > ?", types.TypeDocumentPreview, after).
		Where(
			due,
			types.DocumentPreviewVersion,
			now.Format(time.RFC3339Nano),
			now.Add(-types.DocumentPreviewLease),
			now.Format(time.RFC3339Nano),
			now.Format(time.RFC3339Nano),
		).
		Order("id").Limit(100).Find(&ops).Error
	return ops, err
}

// Claim atomically leases a pending preview operation.
func (r *DocumentPreviewRepository) Claim(ctx context.Context, op *types.TaskPendingOp) (bool, error) {
	state, err := DecodeDocumentPreview(op)
	if err != nil {
		return false, err
	}
	if state.Status != "pending" || time.Now().Before(state.NextAttempt) {
		return false, nil
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if op.FailCount >= 3 {
		state.Status = "failed"
		state.NextAttempt = now.Add(time.Minute)
		res := r.db.WithContext(ctx).Model(&types.TaskPendingOp{}).
			Where(
				"id = ? AND payload = ? AND (claimed_at IS NULL OR claimed_at < ?)",
				op.ID, op.Payload, now.Add(-types.DocumentPreviewLease),
			).
			Updates(map[string]any{"payload": previewPayload(state), "claimed_at": nil})
		return false, res.Error
	}
	res := r.db.WithContext(ctx).Model(&types.TaskPendingOp{}).
		Where(
			"id = ? AND payload = ? AND (claimed_at IS NULL OR claimed_at < ?)",
			op.ID, op.Payload, now.Add(-types.DocumentPreviewLease),
		).
		Updates(map[string]any{"claimed_at": now, "fail_count": gorm.Expr("fail_count + 1")})
	if res.RowsAffected > 0 {
		op.ClaimedAt = &now
		op.FailCount++
	}
	return res.RowsAffected > 0, res.Error
}

// Track persists a cleanup intent before preview bytes are written.
func (r *DocumentPreviewRepository) Track(ctx context.Context, op *types.TaskPendingOp, path string) error {
	s, err := DecodeDocumentPreview(op)
	if err != nil {
		return err
	}
	artifact := types.DocumentPreviewArtifact{
		StateID: op.ID, Token: s.Token, KnowledgeID: s.KnowledgeID, PhysicalPath: path,
		NotBefore: time.Now().UTC().Add(types.DocumentPreviewLease),
		NextCheck: time.Now().UTC().Add(types.DocumentPreviewLease),
	}
	return r.db.WithContext(ctx).Create(&types.TaskPendingOp{
		TenantID: op.TenantID, TaskType: types.TypeDocumentPreview, Scope: types.TaskScopeKnowledge,
		ScopeID: s.KnowledgeID, Op: "artifact", Payload: previewPayload(artifact), EnqueuedAt: time.Now(),
	}).Error
}

// Publish binds the generated resource and marks the matching preview ready.
func (r *DocumentPreviewRepository) Publish(
	ctx context.Context,
	op *types.TaskPendingOp,
	ref string,
	sourceHash string,
) error {
	s, err := DecodeDocumentPreview(op)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		k, err := lockPreviewSource(tx, op.TenantID, s.KnowledgeID)
		if err != nil {
			return err
		}
		if !sourceMatches(s, k) {
			return ErrPreviewGone
		}
		var live types.TaskPendingOp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&live, "id = ?", op.ID).Error; err != nil {
			return err
		}
		state, err := DecodeDocumentPreview(&live)
		if err != nil {
			return err
		}
		claimExpired := live.ClaimedAt != nil && time.Since(*live.ClaimedAt) >= types.DocumentPreviewLease
		if state.Token != s.Token || state.Status != "pending" || live.ClaimedAt == nil || op.ClaimedAt == nil ||
			!live.ClaimedAt.Equal(*op.ClaimedAt) || claimExpired {
			return ErrPreviewGone
		}
		handle, ok := types.ParseResourcePath(ref)
		if !ok {
			return errors.New("preview must be a registered resource")
		}
		var resource types.StoredResource
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				"tenant_id = ? AND handle = ? AND state = ?",
				op.TenantID, handle, types.ResourceStateActive,
			).
			First(&resource).Error; err != nil {
			return err
		}
		if err := tx.Create(&types.ResourceBinding{
			TenantID: op.TenantID, ResourceID: resource.ID, OwnerType: types.ResourceOwnerKnowledge,
			OwnerID: s.KnowledgeID, Relation: types.ResourceRelationPreviewFile,
		}).Error; err != nil {
			return err
		}
		if k.FileHash != "" && k.FileHash != sourceHash {
			return ErrPreviewGone
		}
		if k.FileHash == "" {
			if err := tx.Model(k).UpdateColumn("file_hash", sourceHash).Error; err != nil {
				return err
			}
			state.SourceHash = sourceHash
		}
		state.Status = "ready"
		state.ResourceRef = ref
		return tx.Model(&live).Updates(map[string]any{"payload": previewPayload(state), "claimed_at": nil}).Error
	})
}

// Fail releases the claim and schedules the next bounded retry.
func (r *DocumentPreviewRepository) Fail(ctx context.Context, op *types.TaskPendingOp) error {
	s, err := DecodeDocumentPreview(op)
	if err != nil {
		return err
	}
	s.NextAttempt = time.Now().UTC().Add(time.Duration(op.FailCount) * 15 * time.Second)
	if op.FailCount >= 3 {
		s.Status = "failed"
		s.NextAttempt = time.Now().UTC().Add(time.Minute)
	}
	return r.db.WithContext(ctx).Model(&types.TaskPendingOp{}).
		Where("id = ? AND payload = ? AND claimed_at = ?", op.ID, op.Payload, op.ClaimedAt).
		Updates(map[string]any{"payload": previewPayload(s), "claimed_at": nil}).Error
}

// Invalidate resets a preview after its registered resource disappears.
func (r *DocumentPreviewRepository) Invalidate(ctx context.Context, op *types.TaskPendingOp) error {
	s, err := DecodeDocumentPreview(op)
	if err != nil {
		return err
	}
	s.Status = "pending"
	s.ResourceRef = ""
	s.Token = uuid.NewString()
	s.NextAttempt = time.Time{}
	return r.db.WithContext(ctx).Model(&types.TaskPendingOp{}).
		Where("id = ? AND payload = ?", op.ID, op.Payload).
		Updates(map[string]any{"payload": previewPayload(s), "claimed_at": nil, "fail_count": 0}).Error
}

// DeleteOp removes a retired preview operation.
func (r *DocumentPreviewRepository) DeleteOp(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND task_type = ?", id, types.TypeDocumentPreview).
		Delete(&types.TaskPendingOp{}).Error
}

// RetireArtifact only retires our binding. Other owners keep the blob alive.
// The intent survives retirement and physical deletion failures. A missing owner
// is safe: the intent carries everything needed after soft or hard deletion.
func (r *DocumentPreviewRepository) RetireArtifact(ctx context.Context, op *types.TaskPendingOp) (string, error) {
	var a types.DocumentPreviewArtifact
	if err := json.Unmarshal(op.Payload, &a); err != nil {
		return "", err
	}
	if time.Now().Before(a.NotBefore) {
		return "", nil
	}
	path := ""
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Read owner under the same locks as Publish. Missing/deleting is expected.
		k, err := lockPreviewSource(tx, op.TenantID, a.KnowledgeID)
		if err != nil && !errors.Is(err, ErrPreviewGone) && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var stateOp types.TaskPendingOp
		stateErr := tx.First(&stateOp, "id = ? AND task_type = ?", a.StateID, types.TypeDocumentPreview).Error
		if stateErr != nil && !errors.Is(stateErr, gorm.ErrRecordNotFound) {
			return stateErr
		}
		var s types.DocumentPreviewState
		if stateErr == nil {
			if err := json.Unmarshal(stateOp.Payload, &s); err != nil {
				return err
			}
		}
		hash := sha256.Sum256([]byte(a.PhysicalPath))
		var resource types.StoredResource
		resourceErr := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND location_hash = ?", op.TenantID, hex.EncodeToString(hash[:])).
			Order("created_at DESC").First(&resource).Error
		if resourceErr != nil && !errors.Is(resourceErr, gorm.ErrRecordNotFound) {
			return resourceErr
		}
		validSource := k != nil && err == nil && sourceMatches(s, k)
		if validSource && s.Token == a.Token {
			claimActive := stateOp.ClaimedAt != nil && time.Since(*stateOp.ClaimedAt) < types.DocumentPreviewLease
			if s.Status == "pending" && claimActive {
				return nil
			}
			readyResource := resourceErr == nil && resource.State == types.ResourceStateActive &&
				s.ResourceRef == types.BuildResourcePath(resource.Handle)
			if s.Status == "ready" && readyResource {
				a.NextCheck = time.Now().UTC().Add(time.Hour)
				return tx.Model(op).Update("payload", previewPayload(a)).Error
			}
		}
		if resourceErr == nil {
			if err := tx.Where(
				"resource_id = ? AND tenant_id = ? AND owner_type = ? AND owner_id = ? AND relation = ?",
				resource.ID, op.TenantID, types.ResourceOwnerKnowledge,
				a.KnowledgeID, types.ResourceRelationPreviewFile,
			).Delete(&types.ResourceBinding{}).Error; err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&types.ResourceBinding{}).
				Where("resource_id = ?", resource.ID).
				Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return tx.Delete(&types.TaskPendingOp{}, op.ID).Error
			}
			if err := tx.Unscoped().Model(&resource).Updates(map[string]any{
				"state": types.ResourceStateDeleted, "deleted_at": time.Now(),
			}).Error; err != nil {
				return err
			}
		}
		path = a.PhysicalPath
		return nil
	})
	return path, err
}

// DeferBusy releases a contended claim without using
// the three-attempt budget; the persistent due row will wake after backoff.
func (r *DocumentPreviewRepository) DeferBusy(ctx context.Context, op *types.TaskPendingOp) error {
	s, err := DecodeDocumentPreview(op)
	if err != nil {
		return err
	}
	s.NextAttempt = time.Now().UTC().Add(15 * time.Second)
	return r.db.WithContext(ctx).Model(&types.TaskPendingOp{}).
		Where("id = ? AND payload = ? AND claimed_at = ?", op.ID, op.Payload, op.ClaimedAt).
		Updates(map[string]any{
			"payload": previewPayload(s), "claimed_at": nil,
			"fail_count": gorm.Expr("CASE WHEN fail_count > 0 THEN fail_count - 1 ELSE 0 END"),
		}).Error
}

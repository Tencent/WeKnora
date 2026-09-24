package repository

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ErrIntentVerdictNotFound 是按 ID/条件操作不到目标记录时的哨兵错误。
var ErrIntentVerdictNotFound = errors.New("intent verdict record not found")

// IntentVerdictRepository implements interfaces.IntentVerdictRepository.
//
// 所有查询都带 tenant_id 谓词：intent_verdicts 是多租户判定日志，
// 跨租户读取/修改一律视为 not found（与 intent_policy 的租户隔离对齐，
// 设计 §14 场景 4）。
type IntentVerdictRepository struct {
	db *gorm.DB
}

// NewIntentVerdictRepository creates a repository backed by GORM.
func NewIntentVerdictRepository(db *gorm.DB) interfaces.IntentVerdictRepository {
	return &IntentVerdictRepository{db: db}
}

// Create 写入一条判定记录。最低字段校验在这里兜底：缺 session/verdict
// 的行无法回链会话与对账，属于脏数据，拒绝写入（校验枚举值是
// types.NewVerdictRecord 的职责，此处不重复）。
func (r *IntentVerdictRepository) Create(ctx context.Context, rec *types.VerdictRecord) error {
	if rec == nil {
		return errors.New("intentgate: verdict record is nil")
	}
	if rec.SessionID == "" {
		return errors.New("intentgate: session_id is required")
	}
	if rec.ToolName == "" {
		return errors.New("intentgate: tool_name is required")
	}
	if rec.Layer == "" || rec.Verdict == "" {
		return errors.New("intentgate: layer and verdict are required")
	}
	if err := r.db.WithContext(ctx).Create(rec).Error; err != nil {
		return fmt.Errorf("create intent verdict: %w", err)
	}
	return nil
}

// GetByID 按主键读取一条记录。
func (r *IntentVerdictRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.VerdictRecord, error) {
	var rec types.VerdictRecord
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIntentVerdictNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get intent verdict: %w", err)
	}
	return &rec, nil
}

// ListBySession 按会话查询判定记录，created_at 升序（id 决胜），
// 与会话日志的时间序读法一致。
func (r *IntentVerdictRepository) ListBySession(ctx context.Context, tenantID uint64, sessionID string, limit int) ([]*types.VerdictRecord, error) {
	q := r.db.WithContext(ctx).
		Where("tenant_id = ? AND session_id = ?", tenantID, sessionID).
		Order("created_at ASC, id ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []*types.VerdictRecord
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list intent verdicts by session: %w", err)
	}
	return rows, nil
}

// ListByPolicy 按策略查询判定记录（跨 version，供策略生命周期报表
// 对比 v1/v2 的判定分布，设计 §3.2），created_at 升序。
func (r *IntentVerdictRepository) ListByPolicy(ctx context.Context, tenantID uint64, policyID string, limit int) ([]*types.VerdictRecord, error) {
	q := r.db.WithContext(ctx).
		Where("tenant_id = ? AND policy_id = ?", tenantID, policyID).
		Order("created_at ASC, id ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var rows []*types.VerdictRecord
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list intent verdicts by policy: %w", err)
	}
	return rows, nil
}

// ListByTenant 全租户最近 limit 条，created_at 降序。
func (r *IntentVerdictRepository) ListByTenant(ctx context.Context, tenantID uint64, limit int) ([]*types.VerdictRecord, error) {
	q := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC")
	if limit > 0 {
		if limit > 1000 {
			limit = 1000
		}
		q = q.Limit(limit)
	}
	var rows []*types.VerdictRecord
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListByJudgeModel 按 judge 模型过滤（T61 语料导出，issue #24）。
// judgeModel 为空串时返回规则层/baseline 判定（无 judge 模型归属的行）；
// limit<=0 表示不限（同样封顶 1000 防失控）。created_at 升序——导出
// 语料的稳定顺序（按时间先后训练/评测切分）。
func (r *IntentVerdictRepository) ListByJudgeModel(ctx context.Context, tenantID uint64, judgeModel string, limit int) ([]*types.VerdictRecord, error) {
	q := r.db.WithContext(ctx).
		Where("tenant_id = ? AND judge_model = ?", tenantID, judgeModel).
		Order("created_at ASC, id ASC")
	if limit > 0 {
		if limit > 1000 {
			limit = 1000
		}
		q = q.Limit(limit)
	}
	var rows []*types.VerdictRecord
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list intent verdicts by judge model: %w", err)
	}
	return rows, nil
}

// CountByVerdictGrouped 按 verdict 分组计数（T51）。policyID 为空时
// 统计租户全部（含 policy_id NULL 的 baseline 行）。
func (r *IntentVerdictRepository) CountByVerdictGrouped(ctx context.Context, tenantID uint64, policyID string) (map[string]int64, error) {
	var rows []struct {
		Verdict string
		Cnt     int64
	}
	q := r.db.WithContext(ctx).Model(&types.VerdictRecord{}).
		Select("verdict, count(*) AS cnt").
		Where("tenant_id = ?", tenantID)
	if policyID != "" {
		q = q.Where("policy_id = ?", policyID)
	}
	if err := q.Group("verdict").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Verdict] = row.Cnt
	}
	return out, nil
}

// UpdateHumanOverrideByToolCallID 按 tool_call_id 回写（T60）。
func (r *IntentVerdictRepository) UpdateHumanOverrideByToolCallID(ctx context.Context, tenantID uint64, toolCallID, override string) error {
	q := r.db.WithContext(ctx).Model(&types.VerdictRecord{}).
		Where("tool_call_id = ?", toolCallID)
	if tenantID != 0 {
		q = q.Where("tenant_id = ?", tenantID)
	}
	res := q.Order("created_at DESC").Limit(1).
		Update("human_override", override)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrIntentVerdictNotFound
	}
	return nil
}

// UpdateHumanOverride 更新人工后续动作（飞轮关键字段：审批改参数、
// 人工修正都会回写这里，设计 §3.3）。override 必须是合法枚举值。
func (r *IntentVerdictRepository) UpdateHumanOverride(ctx context.Context, tenantID uint64, id string, override string) error {
	if !types.ValidHumanOverride(override) {
		return fmt.Errorf("intentgate: unknown human_override %q", override)
	}
	res := r.db.WithContext(ctx).Model(&types.VerdictRecord{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Update("human_override", override)
	if res.Error != nil {
		return fmt.Errorf("update intent verdict human_override: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrIntentVerdictNotFound
	}
	return nil
}

// Delete 删除一条判定记录。verdict 日志原则上只增不删，此方法为
// 测试与租户级数据清理预留。
func (r *IntentVerdictRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&types.VerdictRecord{})
	if res.Error != nil {
		return fmt.Errorf("delete intent verdict: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrIntentVerdictNotFound
	}
	return nil
}

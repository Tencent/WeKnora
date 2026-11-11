package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ModelTokenUsageRepository stores and aggregates chat model calls across tenants.
type ModelTokenUsageRepository struct {
	db *gorm.DB
}

// NewModelTokenUsageRepository uses the application's database for the usage ledger.
func NewModelTokenUsageRepository(db *gorm.DB) *ModelTokenUsageRepository {
	return &ModelTokenUsageRepository{db: db}
}

// Record inserts a call once, even if the same call ID is submitted again.
func (r *ModelTokenUsageRepository) Record(ctx context.Context, entry types.ModelTokenUsage) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}}, DoNothing: true,
	}).Create(&entry).Error
}

// ModelTokenUsageSummary reports usage and call counts for one tenant, model, and period.
type ModelTokenUsageSummary struct {
	Period          string `json:"period"`
	TenantID        uint64 `json:"tenant_id"`
	ModelID         string `json:"model_id"`
	ModelName       string `json:"model_name"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	Calls           int64  `json:"calls"`
	UnknownCalls    int64  `json:"unknown_calls"`
	IncompleteCalls int64  `json:"incomplete_calls"`
}

// Summarize aggregates calls by UTC calendar day or month over [from, to).
// A zero tenant ID selects all tenants and must only be used by platform admins.
func (r *ModelTokenUsageRepository) Summarize(
	ctx context.Context, tenantID uint64, from, to, modelID string, monthly bool,
) ([]ModelTokenUsageSummary, error) {
	period := "usage_date"
	if monthly {
		period = "substr(usage_date, 1, 7)"
	}
	query := r.db.WithContext(ctx).Model(&types.ModelTokenUsage{}).
		Where("usage_date >= ? AND usage_date < ?", from, to)
	if tenantID != 0 {
		query = query.Where("tenant_id = ?", tenantID)
	}
	if modelID != "" {
		query = query.Where("model_id = ?", modelID)
	}
	rows := []ModelTokenUsageSummary{}
	err := query.Select(period + ` AS period, tenant_id, model_id, model_name,
		SUM(input_tokens) AS input_tokens, SUM(output_tokens) AS output_tokens,
		SUM(total_tokens) AS total_tokens, COUNT(*) AS calls,
		SUM(CASE WHEN usage_known THEN 0 ELSE 1 END) AS unknown_calls,
		SUM(CASE WHEN completed THEN 0 ELSE 1 END) AS incomplete_calls`).
		Group(period + ", tenant_id, model_id, model_name").
		Order("period, tenant_id, model_id, model_name").
		Scan(&rows).Error
	return rows, err
}

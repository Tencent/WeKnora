package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func BenchmarkKnowledgeMetadataRepositoryResolveDocumentScope10000(b *testing.B) {
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:metadata-scope-benchmark-%d?mode=memory&cache=shared", time.Now().UnixNano())),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		b.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		b.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.AutoMigrate(
		&types.Knowledge{}, &types.MetadataDefinition{}, &types.MetadataOption{},
		&types.MetadataValue{}, &types.MetadataValueOption{}, &types.MetadataAutoRule{},
	); err != nil {
		b.Fatal(err)
	}
	repo := NewKnowledgeMetadataRepository(db)
	ctx := b.Context()

	definitions := make([]*types.MetadataDefinition, 5)
	for index := range definitions {
		definitions[index] = &types.MetadataDefinition{
			TenantID: 100, KnowledgeBaseID: "kb-a", Name: fmt.Sprintf("score_%d", index),
			NormalizedName: fmt.Sprintf("score_%d", index), ValueType: types.MetadataValueTypeNumber,
			Filterable: true, Status: types.MetadataStatusActive,
		}
		if err := repo.CreateDefinition(ctx, definitions[index]); err != nil {
			b.Fatal(err)
		}
	}

	knowledges := make([]types.Knowledge, 10000)
	values := make([]types.MetadataValue, 0, len(knowledges)*len(definitions))
	for index := range knowledges {
		knowledgeID := fmt.Sprintf("doc-%05d", index)
		knowledges[index] = types.Knowledge{
			ID: knowledgeID, TenantID: 100, KnowledgeBaseID: "kb-a",
			Type: types.KnowledgeTypeManual, Title: knowledgeID, Source: "benchmark",
		}
		for _, definition := range definitions {
			number := float64(index)
			values = append(values, types.MetadataValue{
				TenantID: 100, KnowledgeBaseID: "kb-a", KnowledgeID: knowledgeID,
				MetadataDefinitionID: definition.ID, NumberValue: &number,
				Source:       types.MetadataValueSourceManual,
				ReviewStatus: types.MetadataReviewStatusConfirmed, Version: 1,
			})
		}
	}
	if err := db.CreateInBatches(&knowledges, 500).Error; err != nil {
		b.Fatal(err)
	}
	if err := db.CreateInBatches(&values, 500).Error; err != nil {
		b.Fatal(err)
	}

	conditions := make([]types.MetadataCondition, 0, len(definitions))
	for _, definition := range definitions {
		conditions = append(conditions, types.MetadataCondition{
			MetadataDefinitionID: definition.ID,
			Operator:             types.MetadataOperatorGTE, Values: []any{5000},
		})
	}
	b.ResetTimer()
	for range b.N {
		scope, err := repo.ResolveDocumentScope(ctx, types.MetadataScopeQuery{
			TenantID: 100, KnowledgeBaseID: "kb-a", Conditions: conditions,
		})
		if err != nil {
			b.Fatal(err)
		}
		if scope.Mode != types.DocumentScopeModeIDs || len(scope.IDs) != 5000 {
			b.Fatalf("unexpected scope: mode=%s ids=%d", scope.Mode, len(scope.IDs))
		}
	}
}

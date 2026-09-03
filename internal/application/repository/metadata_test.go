package repository

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupMetadataTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("PRAGMA foreign_keys = ON").Error)
	require.NoError(t, db.AutoMigrate(
		&types.MetadataDefinition{},
		&types.MetadataOption{},
		&types.MetadataValue{},
		&types.MetadataValueOption{},
		&types.MetadataAutoRule{},
	))

	return db
}

func TestKnowledgeMetadataRepository_TextValueRoundTrip(t *testing.T) {
	db := setupMetadataTestDB(t)
	repo := NewKnowledgeMetadataRepository(db)
	ctx := t.Context()

	definition := &types.MetadataDefinition{
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Name:            "audience",
		NormalizedName:  "audience",
		ValueType:       types.MetadataValueTypeText,
		Status:          types.MetadataStatusActive,
	}
	require.NoError(t, repo.CreateDefinition(ctx, definition))

	value := "engineers"
	require.NoError(t, repo.CreateDocumentValue(ctx, &types.MetadataValue{
		TenantID:             100,
		KnowledgeBaseID:      "kb-a",
		KnowledgeID:          "doc-a",
		MetadataDefinitionID: definition.ID,
		TextValue:            &value,
		Source:               types.MetadataValueSourceManual,
		ReviewStatus:         types.MetadataReviewStatusConfirmed,
		Version:              1,
	}))

	values, err := repo.ListDocumentValues(ctx, 100, "kb-a", []string{"doc-a"})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.NotNil(t, values[0].TextValue)
	require.Equal(t, "engineers", *values[0].TextValue)
	require.Equal(t, definition.ID, values[0].MetadataDefinitionID)
}

func TestKnowledgeMetadataRepository_SelectOptionRoundTrip(t *testing.T) {
	db := setupMetadataTestDB(t)
	repo := NewKnowledgeMetadataRepository(db)
	ctx := t.Context()

	definition := &types.MetadataDefinition{
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Name:            "document_type",
		NormalizedName:  "document_type",
		ValueType:       types.MetadataValueTypeSingleSelect,
		Status:          types.MetadataStatusActive,
		Options: []types.MetadataOption{
			{Label: "API", NormalizedLabel: "api", Status: types.MetadataStatusActive},
		},
	}
	require.NoError(t, repo.CreateDefinition(ctx, definition))
	require.Len(t, definition.Options, 1)
	require.NotEmpty(t, definition.Options[0].ID)

	require.NoError(t, repo.CreateDocumentValue(ctx, &types.MetadataValue{
		TenantID:             100,
		KnowledgeBaseID:      "kb-a",
		KnowledgeID:          "doc-a",
		MetadataDefinitionID: definition.ID,
		OptionIDs:            []string{definition.Options[0].ID},
		Source:               types.MetadataValueSourceManual,
		ReviewStatus:         types.MetadataReviewStatusConfirmed,
	}))

	values, err := repo.ListDocumentValues(ctx, 100, "kb-a", []string{"doc-a"})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, []string{definition.Options[0].ID}, values[0].OptionIDs)
}

func TestKnowledgeMetadataRepository_RejectsOptionFromAnotherDefinition(t *testing.T) {
	db := setupMetadataTestDB(t)
	repo := NewKnowledgeMetadataRepository(db)
	ctx := t.Context()

	first := &types.MetadataDefinition{
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Name:            "document_type",
		NormalizedName:  "document_type",
		ValueType:       types.MetadataValueTypeSingleSelect,
		Status:          types.MetadataStatusActive,
		Options: []types.MetadataOption{
			{Label: "API", NormalizedLabel: "api", Status: types.MetadataStatusActive},
		},
	}
	second := &types.MetadataDefinition{
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Name:            "product",
		NormalizedName:  "product",
		ValueType:       types.MetadataValueTypeSingleSelect,
		Status:          types.MetadataStatusActive,
		Options: []types.MetadataOption{
			{Label: "Console", NormalizedLabel: "console", Status: types.MetadataStatusActive},
		},
	}
	require.NoError(t, repo.CreateDefinition(ctx, first))
	require.NoError(t, repo.CreateDefinition(ctx, second))

	err := repo.CreateDocumentValue(ctx, &types.MetadataValue{
		TenantID:             100,
		KnowledgeBaseID:      "kb-a",
		KnowledgeID:          "doc-a",
		MetadataDefinitionID: first.ID,
		OptionIDs:            []string{second.Options[0].ID},
		Source:               types.MetadataValueSourceManual,
		ReviewStatus:         types.MetadataReviewStatusConfirmed,
	})
	require.ErrorIs(t, err, ErrMetadataOptionNotInDefinition)

	values, listErr := repo.ListDocumentValues(ctx, 100, "kb-a", []string{"doc-a"})
	require.NoError(t, listErr)
	require.Empty(t, values)
}

func TestKnowledgeMetadataRepository_MultiSelectPreservesOrder(t *testing.T) {
	db := setupMetadataTestDB(t)
	repo := NewKnowledgeMetadataRepository(db)
	ctx := t.Context()

	definition := &types.MetadataDefinition{
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Name:            "versions",
		NormalizedName:  "versions",
		ValueType:       types.MetadataValueTypeMultiSelect,
		Status:          types.MetadataStatusActive,
		Options: []types.MetadataOption{
			{Label: "v2", NormalizedLabel: "v2", Status: types.MetadataStatusActive},
			{Label: "v3", NormalizedLabel: "v3", Status: types.MetadataStatusActive},
		},
	}
	require.NoError(t, repo.CreateDefinition(ctx, definition))
	optionIDs := []string{definition.Options[1].ID, definition.Options[0].ID}

	require.NoError(t, repo.CreateDocumentValue(ctx, &types.MetadataValue{
		TenantID:             100,
		KnowledgeBaseID:      "kb-a",
		KnowledgeID:          "doc-a",
		MetadataDefinitionID: definition.ID,
		OptionIDs:            optionIDs,
		Source:               types.MetadataValueSourceManual,
		ReviewStatus:         types.MetadataReviewStatusConfirmed,
	}))

	values, err := repo.ListDocumentValues(ctx, 100, "kb-a", []string{"doc-a"})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, optionIDs, values[0].OptionIDs)
}

func TestKnowledgeMetadataRepository_ScalarValueRoundTrip(t *testing.T) {
	testCases := []struct {
		name      string
		valueType types.MetadataValueType
		apply     func(*types.MetadataValue)
		assert    func(*testing.T, *types.MetadataValue)
	}{
		{
			name:      "number",
			valueType: types.MetadataValueTypeNumber,
			apply: func(value *types.MetadataValue) {
				number := 42.5
				value.NumberValue = &number
			},
			assert: func(t *testing.T, value *types.MetadataValue) {
				require.NotNil(t, value.NumberValue)
				require.Equal(t, 42.5, *value.NumberValue)
			},
		},
		{
			name:      "date",
			valueType: types.MetadataValueTypeDate,
			apply: func(value *types.MetadataValue) {
				date := time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)
				value.DateValue = &date
			},
			assert: func(t *testing.T, value *types.MetadataValue) {
				require.NotNil(t, value.DateValue)
				require.Equal(t, "2026-08-21", value.DateValue.Format(time.DateOnly))
			},
		},
		{
			name:      "boolean false",
			valueType: types.MetadataValueTypeBoolean,
			apply: func(value *types.MetadataValue) {
				boolean := false
				value.BoolValue = &boolean
			},
			assert: func(t *testing.T, value *types.MetadataValue) {
				require.NotNil(t, value.BoolValue)
				require.False(t, *value.BoolValue)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := setupMetadataTestDB(t)
			repo := NewKnowledgeMetadataRepository(db)
			ctx := t.Context()
			definition := &types.MetadataDefinition{
				TenantID:        100,
				KnowledgeBaseID: "kb-a",
				Name:            testCase.name,
				NormalizedName:  testCase.name,
				ValueType:       testCase.valueType,
				Status:          types.MetadataStatusActive,
			}
			require.NoError(t, repo.CreateDefinition(ctx, definition))

			value := &types.MetadataValue{
				TenantID:             100,
				KnowledgeBaseID:      "kb-a",
				KnowledgeID:          "doc-a",
				MetadataDefinitionID: definition.ID,
				Source:               types.MetadataValueSourceManual,
				ReviewStatus:         types.MetadataReviewStatusConfirmed,
			}
			testCase.apply(value)
			require.NoError(t, repo.CreateDocumentValue(ctx, value))

			values, err := repo.ListDocumentValues(ctx, 100, "kb-a", []string{"doc-a"})
			require.NoError(t, err)
			require.Len(t, values, 1)
			testCase.assert(t, values[0])
		})
	}
}

func TestKnowledgeMetadataRepository_DeleteDocumentMetadataIsTenantScoped(t *testing.T) {
	db := setupMetadataTestDB(t)
	repo := NewKnowledgeMetadataRepository(db)
	ctx := t.Context()

	for _, tenantID := range []uint64{100, 200} {
		definition := &types.MetadataDefinition{
			TenantID: tenantID, KnowledgeBaseID: "kb-a", Name: "category",
			NormalizedName: "category", ValueType: types.MetadataValueTypeSingleSelect,
			Status:  types.MetadataStatusActive,
			Options: []types.MetadataOption{{Label: "API", NormalizedLabel: "api", Status: types.MetadataStatusActive}},
		}
		require.NoError(t, repo.CreateDefinition(ctx, definition))
		require.NoError(t, repo.CreateDocumentValue(ctx, &types.MetadataValue{
			TenantID: tenantID, KnowledgeBaseID: "kb-a", KnowledgeID: "doc-a",
			MetadataDefinitionID: definition.ID, OptionIDs: []string{definition.Options[0].ID},
			Source: types.MetadataValueSourceManual, ReviewStatus: types.MetadataReviewStatusConfirmed,
		}))
	}

	require.NoError(t, repo.DeleteDocumentMetadata(ctx, 100, []string{"doc-a"}))
	values, err := repo.ListDocumentValues(ctx, 100, "kb-a", []string{"doc-a"})
	require.NoError(t, err)
	require.Empty(t, values)
	values, err = repo.ListDocumentValues(ctx, 200, "kb-a", []string{"doc-a"})
	require.NoError(t, err)
	require.Len(t, values, 1)
}

func TestKnowledgeMetadataRepository_DeleteKnowledgeBaseMetadataRemovesAllRelations(t *testing.T) {
	db := setupMetadataTestDB(t)
	repo := NewKnowledgeMetadataRepository(db)
	ctx := t.Context()

	definition := &types.MetadataDefinition{
		TenantID: 100, KnowledgeBaseID: "kb-a", Name: "category", NormalizedName: "category",
		ValueType: types.MetadataValueTypeSingleSelect, Status: types.MetadataStatusActive,
		Options: []types.MetadataOption{{Label: "API", NormalizedLabel: "api", Status: types.MetadataStatusActive}},
	}
	require.NoError(t, repo.CreateDefinition(ctx, definition))
	rule := &types.MetadataAutoRule{
		TenantID: 100, KnowledgeBaseID: "kb-a", MetadataDefinitionID: definition.ID,
		Strategy: types.MetadataRuleStrategySourceMapping, Config: types.JSONMap{"source_key": "category"},
		Revision: 1, Enabled: true,
	}
	require.NoError(t, repo.SaveAutoRule(ctx, rule))
	require.NoError(t, repo.CreateDocumentValue(ctx, &types.MetadataValue{
		TenantID: 100, KnowledgeBaseID: "kb-a", KnowledgeID: "doc-a",
		MetadataDefinitionID: definition.ID, OptionIDs: []string{definition.Options[0].ID},
		Source: types.MetadataValueSourceAutomatic, ReviewStatus: types.MetadataReviewStatusPending,
	}))

	require.NoError(t, repo.DeleteKnowledgeBaseMetadata(ctx, 100, "kb-a"))
	for _, model := range []any{
		&types.MetadataDefinition{}, &types.MetadataOption{}, &types.MetadataValue{},
		&types.MetadataValueOption{}, &types.MetadataAutoRule{},
	} {
		var count int64
		require.NoError(t, db.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestKnowledgeMetadataRepository_ListDocumentValuesIsScoped(t *testing.T) {
	db := setupMetadataTestDB(t)
	repo := NewKnowledgeMetadataRepository(db)
	ctx := t.Context()

	createValue := func(tenantID uint64, kbID, name, docID string) {
		definition := &types.MetadataDefinition{
			TenantID:        tenantID,
			KnowledgeBaseID: kbID,
			Name:            name,
			NormalizedName:  name,
			ValueType:       types.MetadataValueTypeText,
			Status:          types.MetadataStatusActive,
		}
		require.NoError(t, repo.CreateDefinition(ctx, definition))
		text := name
		require.NoError(t, repo.CreateDocumentValue(ctx, &types.MetadataValue{
			TenantID:             tenantID,
			KnowledgeBaseID:      kbID,
			KnowledgeID:          docID,
			MetadataDefinitionID: definition.ID,
			TextValue:            &text,
			Source:               types.MetadataValueSourceManual,
			ReviewStatus:         types.MetadataReviewStatusConfirmed,
		}))
	}

	createValue(100, "kb-a", "visible", "doc-shared")
	createValue(200, "kb-a", "other-tenant", "doc-shared")
	createValue(100, "kb-b", "other-kb", "doc-shared")

	values, err := repo.ListDocumentValues(ctx, 100, "kb-a", []string{"doc-shared"})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.NotNil(t, values[0].TextValue)
	require.Equal(t, "visible", *values[0].TextValue)
}

func TestKnowledgeMetadataRepository_ListDefinitionsIsOrderedAndScoped(t *testing.T) {
	db := setupMetadataTestDB(t)
	repo := NewKnowledgeMetadataRepository(db)
	ctx := t.Context()

	definitions := []*types.MetadataDefinition{
		{
			TenantID:        100,
			KnowledgeBaseID: "kb-a",
			Name:            "second",
			NormalizedName:  "second",
			ValueType:       types.MetadataValueTypeText,
			Status:          types.MetadataStatusActive,
			SortOrder:       20,
		},
		{
			TenantID:        100,
			KnowledgeBaseID: "kb-a",
			Name:            "first",
			NormalizedName:  "first",
			ValueType:       types.MetadataValueTypeSingleSelect,
			Status:          types.MetadataStatusActive,
			SortOrder:       10,
			Options: []types.MetadataOption{
				{Label: "B", NormalizedLabel: "b", Status: types.MetadataStatusActive, SortOrder: 20},
				{Label: "A", NormalizedLabel: "a", Status: types.MetadataStatusActive, SortOrder: 10},
			},
		},
		{
			TenantID:        100,
			KnowledgeBaseID: "kb-a",
			Name:            "archived",
			NormalizedName:  "archived",
			ValueType:       types.MetadataValueTypeText,
			Status:          types.MetadataStatusArchived,
			SortOrder:       0,
		},
		{
			TenantID:        200,
			KnowledgeBaseID: "kb-a",
			Name:            "other tenant",
			NormalizedName:  "other tenant",
			ValueType:       types.MetadataValueTypeText,
			Status:          types.MetadataStatusActive,
		},
		{
			TenantID:        100,
			KnowledgeBaseID: "kb-b",
			Name:            "other kb",
			NormalizedName:  "other kb",
			ValueType:       types.MetadataValueTypeText,
			Status:          types.MetadataStatusActive,
		},
	}
	for _, definition := range definitions {
		require.NoError(t, repo.CreateDefinition(ctx, definition))
	}

	active, err := repo.ListDefinitions(ctx, 100, "kb-a", false)
	require.NoError(t, err)
	require.Len(t, active, 2)
	require.Equal(t, []string{"first", "second"}, []string{active[0].Name, active[1].Name})
	require.Equal(t, []string{"A", "B"}, []string{active[0].Options[0].Label, active[0].Options[1].Label})

	all, err := repo.ListDefinitions(ctx, 100, "kb-a", true)
	require.NoError(t, err)
	require.Len(t, all, 3)
	require.Equal(t, "archived", all[0].Name)
}

func TestKnowledgeMetadataRepository_DefinitionUpdateAndArchivePreserveIdentity(t *testing.T) {
	db := setupMetadataTestDB(t)
	repo := NewKnowledgeMetadataRepository(db)
	ctx := t.Context()

	definition := &types.MetadataDefinition{
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Name:            "audience",
		NormalizedName:  "audience",
		Description:     "before",
		ValueType:       types.MetadataValueTypeText,
		Status:          types.MetadataStatusActive,
	}
	require.NoError(t, repo.CreateDefinition(ctx, definition))
	originalID := definition.ID

	definition.Name = "target_audience"
	definition.NormalizedName = "target_audience"
	definition.Description = "after"
	definition.Required = true
	definition.Filterable = true
	definition.SortOrder = 30
	require.NoError(t, repo.UpdateDefinition(ctx, definition))

	updated, err := repo.GetDefinition(ctx, 100, "kb-a", originalID)
	require.NoError(t, err)
	require.Equal(t, originalID, updated.ID)
	require.Equal(t, uint64(100), updated.TenantID)
	require.Equal(t, "kb-a", updated.KnowledgeBaseID)
	require.Equal(t, "target_audience", updated.Name)
	require.Equal(t, "after", updated.Description)
	require.True(t, updated.Required)
	require.True(t, updated.Filterable)
	require.Equal(t, 30, updated.SortOrder)

	_, err = repo.GetDefinition(ctx, 200, "kb-a", originalID)
	require.ErrorIs(t, err, ErrMetadataDefinitionNotFound)
	require.NoError(t, repo.ArchiveDefinition(ctx, 100, "kb-a", originalID))

	active, err := repo.ListDefinitions(ctx, 100, "kb-a", false)
	require.NoError(t, err)
	require.Empty(t, active)
	archived, err := repo.GetDefinition(ctx, 100, "kb-a", originalID)
	require.NoError(t, err)
	require.Equal(t, types.MetadataStatusArchived, archived.Status)
}

func TestKnowledgeMetadataRepository_ResolveDocumentScopeDistinguishesAllNoneAndIDs(t *testing.T) {
	db := setupMetadataTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	repo := NewKnowledgeMetadataRepository(db)
	ctx := t.Context()

	definition := &types.MetadataDefinition{
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Name:            "versions",
		NormalizedName:  "versions",
		ValueType:       types.MetadataValueTypeMultiSelect,
		Filterable:      true,
		Status:          types.MetadataStatusActive,
		Options: []types.MetadataOption{
			{Label: "v2", NormalizedLabel: "v2", Status: types.MetadataStatusActive},
			{Label: "v3", NormalizedLabel: "v3", Status: types.MetadataStatusActive},
		},
	}
	require.NoError(t, repo.CreateDefinition(ctx, definition))

	for _, knowledgeID := range []string{"doc-a", "doc-b"} {
		require.NoError(t, db.Create(&types.Knowledge{
			ID:              knowledgeID,
			TenantID:        100,
			KnowledgeBaseID: "kb-a",
			Type:            "file",
			Title:           knowledgeID,
			Source:          "test",
		}).Error)
	}
	require.NoError(t, repo.CreateDocumentValue(ctx, &types.MetadataValue{
		TenantID:             100,
		KnowledgeBaseID:      "kb-a",
		KnowledgeID:          "doc-a",
		MetadataDefinitionID: definition.ID,
		OptionIDs:            []string{definition.Options[0].ID, definition.Options[1].ID},
		Source:               types.MetadataValueSourceManual,
		ReviewStatus:         types.MetadataReviewStatusConfirmed,
	}))

	all, err := repo.ResolveDocumentScope(ctx, types.MetadataScopeQuery{
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
	})
	require.NoError(t, err)
	require.Equal(t, types.DocumentScope{Mode: types.DocumentScopeModeAll}, all)

	none, err := repo.ResolveDocumentScope(ctx, types.MetadataScopeQuery{
		TenantID:             100,
		KnowledgeBaseID:      "kb-a",
		ExplicitKnowledgeIDs: []string{},
	})
	require.NoError(t, err)
	require.Equal(t, types.DocumentScope{Mode: types.DocumentScopeModeNone}, none)

	matching, err := repo.ResolveDocumentScope(ctx, types.MetadataScopeQuery{
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Conditions: []types.MetadataCondition{
			{
				MetadataDefinitionID: definition.ID,
				Operator:             types.MetadataOperatorContainsAll,
				Values:               []any{definition.Options[1].ID, definition.Options[0].ID},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, types.DocumentScope{Mode: types.DocumentScopeModeIDs, IDs: []string{"doc-a"}}, matching)

	zeroMatch, err := repo.ResolveDocumentScope(ctx, types.MetadataScopeQuery{
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Conditions: []types.MetadataCondition{
			{
				MetadataDefinitionID: definition.ID,
				Operator:             types.MetadataOperatorContainsAny,
				Values:               []any{"missing-option"},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, types.DocumentScope{Mode: types.DocumentScopeModeNone}, zeroMatch)
}

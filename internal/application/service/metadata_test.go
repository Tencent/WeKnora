package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupMetadataServiceTest(
	t *testing.T,
) (*metadataService, interfaces.KnowledgeMetadataRepository, *gorm.DB, context.Context) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("PRAGMA foreign_keys = ON").Error)
	require.NoError(t, db.AutoMigrate(
		&types.KnowledgeBase{},
		&types.Knowledge{},
		&types.MetadataDefinition{},
		&types.MetadataOption{},
		&types.MetadataValue{},
		&types.MetadataValueOption{},
		&types.MetadataAutoRule{},
	))

	metadataRepo := repository.NewKnowledgeMetadataRepository(db)
	knowledgeRepo := repository.NewKnowledgeRepository(db)
	knowledgeBaseRepo := repository.NewKnowledgeBaseRepository(db)
	require.NoError(t, knowledgeBaseRepo.CreateKnowledgeBase(t.Context(), &types.KnowledgeBase{
		ID: "kb-a", TenantID: 100, Type: types.KnowledgeBaseTypeDocument,
	}))
	service := NewKnowledgeMetadataService(metadataRepo, knowledgeRepo, knowledgeBaseRepo)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(100))
	return service, metadataRepo, db, ctx
}

func TestMetadataService_RejectsFAQKnowledgeBase(t *testing.T) {
	service, _, db, ctx := setupMetadataServiceTest(t)
	require.NoError(t, db.Create(&types.KnowledgeBase{
		ID: "kb-faq", TenantID: 100, Type: types.KnowledgeBaseTypeFAQ,
	}).Error)

	_, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-faq",
		Name:            "audience",
		ValueType:       types.MetadataValueTypeText,
	})
	require.ErrorContains(t, err, "only available for document knowledge bases")
}

func TestMetadataService_ConfigureDefinitionCreatesReadableSchema(t *testing.T) {
	service, _, _, ctx := setupMetadataServiceTest(t)

	created, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "  Document Type  ",
		Description:     "Document classification",
		ValueType:       types.MetadataValueTypeSingleSelect,
		Required:        true,
		Filterable:      true,
		SortOrder:       10,
		Options: []types.ConfigureMetadataOption{
			{Label: " Design ", SortOrder: 20},
			{Label: "API", SortOrder: 10},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "Document Type", created.Name)
	require.Equal(t, "document type", created.NormalizedName)
	require.Len(t, created.Options, 2)
	for _, option := range created.Options {
		require.NotEmpty(t, option.ID)
		require.Equal(t, created.ID, option.MetadataDefinitionID)
	}

	schema, err := service.ReadSchema(ctx, "kb-a")
	require.NoError(t, err)
	require.Equal(t, "kb-a", schema.KnowledgeBaseID)
	require.Len(t, schema.Definitions, 1)
	require.Equal(t, []string{"API", "Design"}, []string{
		schema.Definitions[0].Options[0].Label,
		schema.Definitions[0].Options[1].Label,
	})
}

func TestMetadataService_ConfigureDefinitionLocksTypeAfterValueExists(t *testing.T) {
	service, metadataRepo, _, ctx := setupMetadataServiceTest(t)

	created, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "score",
		ValueType:       types.MetadataValueTypeText,
	})
	require.NoError(t, err)

	updated, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		DefinitionID:    created.ID,
		Name:            "quality_score",
		ValueType:       types.MetadataValueTypeNumber,
		Filterable:      true,
	})
	require.NoError(t, err)
	require.Equal(t, created.ID, updated.ID)
	require.Equal(t, types.MetadataValueTypeNumber, updated.ValueType)

	number := 9.5
	require.NoError(t, metadataRepo.CreateDocumentValue(ctx, &types.MetadataValue{
		TenantID:             100,
		KnowledgeBaseID:      "kb-a",
		KnowledgeID:          "doc-a",
		MetadataDefinitionID: created.ID,
		NumberValue:          &number,
		Source:               types.MetadataValueSourceManual,
		ReviewStatus:         types.MetadataReviewStatusConfirmed,
	}))

	_, err = service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		DefinitionID:    created.ID,
		Name:            "quality_score",
		ValueType:       types.MetadataValueTypeBoolean,
	})
	require.Error(t, err)

	stored, getErr := metadataRepo.GetDefinition(ctx, 100, "kb-a", created.ID)
	require.NoError(t, getErr)
	require.Equal(t, types.MetadataValueTypeNumber, stored.ValueType)
}

func TestMetadataService_ConfigureDefinitionUpdatesOptionsWithoutChangingIDs(t *testing.T) {
	service, _, _, ctx := setupMetadataServiceTest(t)

	created, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "document_type",
		ValueType:       types.MetadataValueTypeSingleSelect,
		Options: []types.ConfigureMetadataOption{
			{Label: "API", SortOrder: 10},
			{Label: "Guide", SortOrder: 20},
		},
	})
	require.NoError(t, err)
	require.Len(t, created.Options, 2)
	apiID := created.Options[0].ID
	guideID := created.Options[1].ID

	updated, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		DefinitionID:    created.ID,
		Name:            "document_type",
		ValueType:       types.MetadataValueTypeSingleSelect,
		Options: []types.ConfigureMetadataOption{
			{ID: apiID, Label: "API Reference", SortOrder: 20},
			{Label: "Design", SortOrder: 10},
		},
	})
	require.NoError(t, err)
	require.Len(t, updated.Options, 3)

	optionsByID := make(map[string]types.MetadataOption, len(updated.Options))
	for _, option := range updated.Options {
		optionsByID[option.ID] = option
	}
	require.Equal(t, "API Reference", optionsByID[apiID].Label)
	require.Equal(t, types.MetadataStatusActive, optionsByID[apiID].Status)
	require.Equal(t, types.MetadataStatusArchived, optionsByID[guideID].Status)

	activeLabels := make([]string, 0, 2)
	for _, option := range updated.Options {
		if option.Status == types.MetadataStatusActive {
			activeLabels = append(activeLabels, option.Label)
		}
	}
	require.ElementsMatch(t, []string{"API Reference", "Design"}, activeLabels)
}

func TestMetadataService_AutoRuleRevisionAndDisable(t *testing.T) {
	service, _, _, ctx := setupMetadataServiceTest(t)

	definition, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "product_line",
		ValueType:       types.MetadataValueTypeText,
	})
	require.NoError(t, err)

	created, err := service.ConfigureAutoRule(ctx, types.ConfigureMetadataAutoRule{
		KnowledgeBaseID: "kb-a",
		DefinitionID:    definition.ID,
		Strategy:        types.MetadataRuleStrategySourceMapping,
		Config:          types.JSONMap{"source_key": "product_line"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.Equal(t, 1, created.Revision)
	require.True(t, created.Enabled)

	updated, err := service.ConfigureAutoRule(ctx, types.ConfigureMetadataAutoRule{
		KnowledgeBaseID: "kb-a",
		DefinitionID:    definition.ID,
		Strategy:        types.MetadataRuleStrategyLLMExtract,
		Config: types.JSONMap{
			"instruction": "Extract the product line",
			"model_id":    "model-a",
		},
	})
	require.NoError(t, err)
	require.Equal(t, created.ID, updated.ID)
	require.Equal(t, 2, updated.Revision)
	require.Equal(t, types.MetadataRuleStrategyLLMExtract, updated.Strategy)

	_, err = service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		DefinitionID:    definition.ID,
		Name:            "product_line",
		ValueType:       types.MetadataValueTypeNumber,
	})
	require.Error(t, err)

	schema, err := service.ReadSchema(ctx, "kb-a")
	require.NoError(t, err)
	require.NotNil(t, schema.Definitions[0].AutoRule)
	require.Equal(t, 2, schema.Definitions[0].AutoRule.Revision)

	require.NoError(t, service.DeleteAutoRule(ctx, "kb-a", definition.ID))
	schema, err = service.ReadSchema(ctx, "kb-a")
	require.NoError(t, err)
	require.Nil(t, schema.Definitions[0].AutoRule)
}

func TestMetadataService_ManualValueTransitionsAndCompletion(t *testing.T) {
	service, _, db, ctx := setupMetadataServiceTest(t)

	definition, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "audience",
		ValueType:       types.MetadataValueTypeText,
		Required:        true,
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.Knowledge{
		ID:              "doc-a",
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Type:            "manual",
		Title:           "Document A",
		Source:          "manual",
	}).Error)

	initial, err := service.ReadDocumentMetadata(ctx, []string{"doc-a"})
	require.NoError(t, err)
	require.Len(t, initial, 1)
	require.Equal(t, 1, initial[0].IncompleteCount)
	require.Equal(t, types.MetadataCompletionStatusIncomplete, initial[0].Values[0].CompletionStatus)
	require.Nil(t, initial[0].Values[0].Value)

	changed, err := service.ChangeDocumentMetadata(ctx, types.ChangeDocumentMetadata{
		KnowledgeID: "doc-a",
		UpdatedBy:   "user-a",
		Changes: []types.MetadataValueChange{
			{
				MetadataDefinitionID: definition.ID,
				Value:                "engineers",
				ValueSet:             true,
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 0, changed.IncompleteCount)
	value := changed.Values[0].Value
	require.NotNil(t, value)
	require.Equal(t, types.MetadataValueSourceManual, value.Source)
	require.Equal(t, types.MetadataReviewStatusConfirmed, value.ReviewStatus)
	require.False(t, value.AllowAutoOverwrite)
	require.Equal(t, 1, value.Version)
	require.Equal(t, "engineers", value.TypedValue(definition.ValueType))

	cleared, err := service.ChangeDocumentMetadata(ctx, types.ChangeDocumentMetadata{
		KnowledgeID: "doc-a",
		UpdatedBy:   "user-a",
		Changes: []types.MetadataValueChange{
			{
				MetadataDefinitionID: definition.ID,
				Value:                nil,
				ValueSet:             true,
				ExpectedVersion:      intPointer(1),
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, cleared.IncompleteCount)
	require.Equal(t, types.MetadataCompletionStatusIncomplete, cleared.Values[0].CompletionStatus)
	require.NotNil(t, cleared.Values[0].Value)
	require.Equal(t, 2, cleared.Values[0].Value.Version)
	require.Nil(t, cleared.Values[0].Value.TypedValue(definition.ValueType))
}

func TestMetadataService_AutomaticOverwriteStateTransitions(t *testing.T) {
	service, _, db, ctx := setupMetadataServiceTest(t)
	definition, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "audience",
		ValueType:       types.MetadataValueTypeText,
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.Knowledge{
		ID:              "doc-a",
		TenantID:        100,
		KnowledgeBaseID: "kb-a",
		Type:            "manual",
		Title:           "Document A",
		Source:          "manual",
	}).Error)

	report, err := service.ApplyAutomaticResults(ctx, types.ApplyAutomaticMetadataResults{
		KnowledgeBaseID: "kb-a",
		KnowledgeID:     "doc-a",
		Results: []types.AutomaticMetadataResult{
			{
				MetadataDefinitionID: definition.ID,
				Value:                "developers",
				AutoRuleID:           "rule-a",
				AutoRuleRevision:     1,
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, &types.ApplyAutomaticMetadataReport{Applied: 1}, report)
	automatic := readOnlyMetadataValue(t, service, ctx, "doc-a")
	require.Equal(t, types.MetadataValueSourceAutomatic, automatic.Source)
	require.Equal(t, types.MetadataReviewStatusPending, automatic.ReviewStatus)
	require.True(t, automatic.AllowAutoOverwrite)
	require.Equal(t, 1, automatic.Version)

	confirmed, err := service.ConfirmDocumentMetadata(ctx, types.ConfirmDocumentMetadata{
		KnowledgeID:           "doc-a",
		MetadataDefinitionIDs: []string{definition.ID},
	})
	require.NoError(t, err)
	require.Equal(t, types.MetadataValueSourceAutomatic, confirmed.Values[0].Value.Source)
	require.Equal(t, types.MetadataReviewStatusConfirmed, confirmed.Values[0].Value.ReviewStatus)
	require.Equal(t, 2, confirmed.Values[0].Value.Version)

	manual, err := service.ChangeDocumentMetadata(ctx, types.ChangeDocumentMetadata{
		KnowledgeID: "doc-a",
		UpdatedBy:   "user-a",
		Changes: []types.MetadataValueChange{
			{
				MetadataDefinitionID: definition.ID,
				Value:                "designers",
				ValueSet:             true,
				ExpectedVersion:      intPointer(2),
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, types.MetadataValueSourceManual, manual.Values[0].Value.Source)
	require.False(t, manual.Values[0].Value.AllowAutoOverwrite)
	require.Equal(t, 3, manual.Values[0].Value.Version)

	report, err = service.ApplyAutomaticResults(ctx, types.ApplyAutomaticMetadataResults{
		KnowledgeBaseID: "kb-a",
		KnowledgeID:     "doc-a",
		Results: []types.AutomaticMetadataResult{
			{
				MetadataDefinitionID: definition.ID,
				Value:                "operators",
				AutoRuleID:           "rule-a",
				AutoRuleRevision:     2,
				ExpectedVersion:      intPointer(3),
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, &types.ApplyAutomaticMetadataReport{Skipped: 1}, report)
	require.Equal(t, "designers", readOnlyMetadataValue(t, service, ctx, "doc-a").TypedValue(definition.ValueType))

	allowOverwrite := true
	policyOnly, err := service.ChangeDocumentMetadata(ctx, types.ChangeDocumentMetadata{
		KnowledgeID: "doc-a",
		UpdatedBy:   "user-a",
		Changes: []types.MetadataValueChange{
			{
				MetadataDefinitionID: definition.ID,
				AllowAutoOverwrite:   &allowOverwrite,
				ExpectedVersion:      intPointer(3),
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, types.MetadataValueSourceManual, policyOnly.Values[0].Value.Source)
	require.Equal(t, types.MetadataReviewStatusConfirmed, policyOnly.Values[0].Value.ReviewStatus)
	require.True(t, policyOnly.Values[0].Value.AllowAutoOverwrite)
	require.Equal(t, 4, policyOnly.Values[0].Value.Version)

	report, err = service.ApplyAutomaticResults(ctx, types.ApplyAutomaticMetadataResults{
		KnowledgeBaseID: "kb-a",
		KnowledgeID:     "doc-a",
		Results: []types.AutomaticMetadataResult{
			{
				MetadataDefinitionID: definition.ID,
				Value:                "operators",
				AutoRuleID:           "rule-a",
				AutoRuleRevision:     2,
				ExpectedVersion:      intPointer(4),
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, &types.ApplyAutomaticMetadataReport{Applied: 1}, report)
	refilled := readOnlyMetadataValue(t, service, ctx, "doc-a")
	require.Equal(t, "operators", refilled.TypedValue(definition.ValueType))
	require.Equal(t, types.MetadataValueSourceAutomatic, refilled.Source)
	require.Equal(t, types.MetadataReviewStatusPending, refilled.ReviewStatus)
	require.Equal(t, 5, refilled.Version)

	report, err = service.ApplyAutomaticResults(ctx, types.ApplyAutomaticMetadataResults{
		KnowledgeBaseID: "kb-a",
		KnowledgeID:     "doc-a",
		Results: []types.AutomaticMetadataResult{{
			MetadataDefinitionID: definition.ID,
			Value:                "operators",
			AutoRuleID:           "rule-a",
			AutoRuleRevision:     2,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, &types.ApplyAutomaticMetadataReport{Skipped: 1}, report)
	require.Equal(t, 5, readOnlyMetadataValue(t, service, ctx, "doc-a").Version)
}

func readOnlyMetadataValue(
	t *testing.T,
	service *metadataService,
	ctx context.Context,
	knowledgeID string,
) *types.MetadataValue {
	t.Helper()
	metadata, err := service.ReadDocumentMetadata(ctx, []string{knowledgeID})
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	require.Len(t, metadata[0].Values, 1)
	require.NotNil(t, metadata[0].Values[0].Value)
	return metadata[0].Values[0].Value
}

func TestMetadataService_ResolveDocumentScopeFiltersAllValueTypes(t *testing.T) {
	service, _, db, ctx := setupMetadataServiceTest(t)
	for _, knowledgeID := range []string{"doc-a", "doc-b"} {
		require.NoError(t, db.Create(&types.Knowledge{
			ID:              knowledgeID,
			TenantID:        100,
			KnowledgeBaseID: "kb-a",
			Type:            "manual",
			Title:           knowledgeID,
			Source:          "manual",
		}).Error)
	}

	definitions := make(map[types.MetadataValueType]*types.MetadataDefinition)
	for _, valueType := range []types.MetadataValueType{
		types.MetadataValueTypeText,
		types.MetadataValueTypeSingleSelect,
		types.MetadataValueTypeMultiSelect,
		types.MetadataValueTypeNumber,
		types.MetadataValueTypeDate,
		types.MetadataValueTypeBoolean,
	} {
		command := types.ConfigureMetadataDefinition{
			KnowledgeBaseID: "kb-a",
			Name:            string(valueType),
			ValueType:       valueType,
			Required:        valueType == types.MetadataValueTypeText,
			Filterable:      true,
		}
		if valueType == types.MetadataValueTypeSingleSelect || valueType == types.MetadataValueTypeMultiSelect {
			command.Options = []types.ConfigureMetadataOption{
				{Label: "v2", SortOrder: 10},
				{Label: "v3", SortOrder: 20},
			}
		}
		definition, err := service.ConfigureDefinition(ctx, command)
		require.NoError(t, err)
		definitions[valueType] = definition
	}

	_, err := service.ChangeDocumentMetadata(ctx, types.ChangeDocumentMetadata{
		KnowledgeID: "doc-a",
		UpdatedBy:   "user-a",
		Changes: []types.MetadataValueChange{
			{
				MetadataDefinitionID: definitions[types.MetadataValueTypeText].ID,
				Value:                "Release Notes",
				ValueSet:             true,
			},
			{
				MetadataDefinitionID: definitions[types.MetadataValueTypeSingleSelect].ID,
				Value:                definitions[types.MetadataValueTypeSingleSelect].Options[0].ID,
				ValueSet:             true,
			},
			{
				MetadataDefinitionID: definitions[types.MetadataValueTypeMultiSelect].ID,
				Value: []string{
					definitions[types.MetadataValueTypeMultiSelect].Options[0].ID,
					definitions[types.MetadataValueTypeMultiSelect].Options[1].ID,
				},
				ValueSet: true,
			},
			{
				MetadataDefinitionID: definitions[types.MetadataValueTypeNumber].ID,
				Value:                42.5,
				ValueSet:             true,
			},
			{
				MetadataDefinitionID: definitions[types.MetadataValueTypeDate].ID,
				Value:                "2026-08-21",
				ValueSet:             true,
			},
			{
				MetadataDefinitionID: definitions[types.MetadataValueTypeBoolean].ID,
				Value:                false,
				ValueSet:             true,
			},
		},
	})
	require.NoError(t, err)

	testCases := []struct {
		name      string
		condition types.MetadataCondition
	}{
		{
			name: "text contains",
			condition: types.MetadataCondition{
				MetadataDefinitionID: definitions[types.MetadataValueTypeText].ID,
				Operator:             types.MetadataOperatorContains,
				Values:               []any{"lease"},
			},
		},
		{
			name: "single select in",
			condition: types.MetadataCondition{
				MetadataDefinitionID: definitions[types.MetadataValueTypeSingleSelect].ID,
				Operator:             types.MetadataOperatorIn,
				Values: []any{
					definitions[types.MetadataValueTypeSingleSelect].Options[0].ID,
				},
			},
		},
		{
			name: "multi select contains all",
			condition: types.MetadataCondition{
				MetadataDefinitionID: definitions[types.MetadataValueTypeMultiSelect].ID,
				Operator:             types.MetadataOperatorContainsAll,
				Values: []any{
					definitions[types.MetadataValueTypeMultiSelect].Options[1].ID,
					definitions[types.MetadataValueTypeMultiSelect].Options[0].ID,
				},
			},
		},
		{
			name: "number between",
			condition: types.MetadataCondition{
				MetadataDefinitionID: definitions[types.MetadataValueTypeNumber].ID,
				Operator:             types.MetadataOperatorBetween,
				Values:               []any{40.0, 50.0},
			},
		},
		{
			name: "date on",
			condition: types.MetadataCondition{
				MetadataDefinitionID: definitions[types.MetadataValueTypeDate].ID,
				Operator:             types.MetadataOperatorOn,
				Values:               []any{"2026-08-21"},
			},
		},
		{
			name: "boolean equals false",
			condition: types.MetadataCondition{
				MetadataDefinitionID: definitions[types.MetadataValueTypeBoolean].ID,
				Operator:             types.MetadataOperatorEqual,
				Values:               []any{false},
			},
		},
		{
			name: "number equals any candidate",
			condition: types.MetadataCondition{
				MetadataDefinitionID: definitions[types.MetadataValueTypeNumber].ID,
				Operator:             types.MetadataOperatorEqual,
				Values:               []any{7.0, 42.5},
			},
		},
		{
			name: "date on any candidate",
			condition: types.MetadataCondition{
				MetadataDefinitionID: definitions[types.MetadataValueTypeDate].ID,
				Operator:             types.MetadataOperatorOn,
				Values:               []any{"2025-01-01", "2026-08-21"},
			},
		},
		{
			name: "boolean equals any candidate",
			condition: types.MetadataCondition{
				MetadataDefinitionID: definitions[types.MetadataValueTypeBoolean].ID,
				Operator:             types.MetadataOperatorEqual,
				Values:               []any{true, false},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scope, err := service.ResolveDocumentScope(ctx, types.MetadataScopeQuery{
				KnowledgeBaseID: "kb-a",
				Conditions:      []types.MetadataCondition{testCase.condition},
			})
			require.NoError(t, err)
			require.Equal(t, types.DocumentScope{Mode: types.DocumentScopeModeIDs, IDs: []string{"doc-a"}}, scope)
		})
	}

	andScope, err := service.ResolveDocumentScope(ctx, types.MetadataScopeQuery{
		KnowledgeBaseID: "kb-a",
		Conditions: []types.MetadataCondition{
			testCases[0].condition,
			{
				MetadataDefinitionID: definitions[types.MetadataValueTypeNumber].ID,
				Operator:             types.MetadataOperatorGreaterThan,
				Values:               []any{100.0},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, types.DocumentScope{Mode: types.DocumentScopeModeNone}, andScope)

	emptyScope, err := service.ResolveDocumentScope(ctx, types.MetadataScopeQuery{
		KnowledgeBaseID: "kb-a",
		Conditions: []types.MetadataCondition{
			{
				MetadataDefinitionID: definitions[types.MetadataValueTypeText].ID,
				Operator:             types.MetadataOperatorIsEmpty,
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, types.DocumentScope{Mode: types.DocumentScopeModeIDs, IDs: []string{"doc-b"}}, emptyScope)
}

func TestMetadataService_ReadDocumentMetadataKeepsArchivedHistoricalValues(t *testing.T) {
	service, _, db, ctx := setupMetadataServiceTest(t)
	active, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "audience",
		ValueType:       types.MetadataValueTypeText,
		Required:        true,
	})
	require.NoError(t, err)
	archived, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "legacy_owner",
		ValueType:       types.MetadataValueTypeText,
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc-a", TenantID: 100, KnowledgeBaseID: "kb-a", Type: "manual", Title: "Document A", Source: "manual",
	}).Error)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc-b", TenantID: 100, KnowledgeBaseID: "kb-a", Type: "manual", Title: "Document B", Source: "manual",
	}).Error)

	_, err = service.ChangeDocumentMetadata(ctx, types.ChangeDocumentMetadata{
		KnowledgeID: "doc-a",
		Changes: []types.MetadataValueChange{{
			MetadataDefinitionID: archived.ID,
			Value:                "platform",
			ValueSet:             true,
		}},
	})
	require.NoError(t, err)
	require.NoError(t, service.ArchiveDefinition(ctx, "kb-a", archived.ID))

	schema, err := service.ReadSchema(ctx, "kb-a")
	require.NoError(t, err)
	require.Len(t, schema.Definitions, 1)
	require.Equal(t, active.ID, schema.Definitions[0].ID)

	withHistory, err := service.ReadDocumentMetadata(ctx, []string{"doc-a"})
	require.NoError(t, err)
	require.Len(t, withHistory, 1)
	require.Len(t, withHistory[0].Values, 2)
	require.Equal(t, active.ID, withHistory[0].Values[0].Definition.ID)
	require.Equal(t, types.MetadataCompletionStatusIncomplete, withHistory[0].Values[0].CompletionStatus)
	require.Equal(t, archived.ID, withHistory[0].Values[1].Definition.ID)
	require.Equal(t, types.MetadataStatusArchived, withHistory[0].Values[1].Definition.Status)
	require.Equal(t, "platform", withHistory[0].Values[1].Value.TypedValue(types.MetadataValueTypeText))
	require.Equal(t, 1, withHistory[0].IncompleteCount)

	withoutHistory, err := service.ReadDocumentMetadata(ctx, []string{"doc-b"})
	require.NoError(t, err)
	require.Len(t, withoutHistory, 1)
	require.Len(t, withoutHistory[0].Values, 1)
	require.Equal(t, active.ID, withoutHistory[0].Values[0].Definition.ID)
}

func TestMetadataService_ReadDocumentMetadataReturnsEmptySchemaForExistingDocument(t *testing.T) {
	service, _, db, ctx := setupMetadataServiceTest(t)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc-empty", TenantID: 100, KnowledgeBaseID: "kb-a", Type: "manual", Title: "Empty", Source: "manual",
	}).Error)

	metadata, err := service.ReadDocumentMetadata(ctx, []string{"doc-empty"})
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	require.Equal(t, "doc-empty", metadata[0].KnowledgeID)
	require.Empty(t, metadata[0].Values)
	require.Equal(t, 0, metadata[0].IncompleteCount)

	missing, err := service.ReadDocumentMetadata(ctx, []string{"doc-missing"})
	require.NoError(t, err)
	require.Empty(t, missing)
}

func TestMetadataService_ChangeDocumentMetadataIsAtomic(t *testing.T) {
	service, _, db, ctx := setupMetadataServiceTest(t)
	first, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "audience",
		ValueType:       types.MetadataValueTypeText,
	})
	require.NoError(t, err)
	second, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "severity",
		ValueType:       types.MetadataValueTypeNumber,
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc-a", TenantID: 100, KnowledgeBaseID: "kb-a", Type: "manual", Title: "Document A", Source: "manual",
	}).Error)

	_, err = service.ChangeDocumentMetadata(ctx, types.ChangeDocumentMetadata{
		KnowledgeID: "doc-a",
		Changes: []types.MetadataValueChange{
			{MetadataDefinitionID: first.ID, Value: "engineers", ValueSet: true},
			{MetadataDefinitionID: second.ID, Value: "not-a-number", ValueSet: true},
		},
	})
	require.Error(t, err)

	metadata, err := service.ReadDocumentMetadata(ctx, []string{"doc-a"})
	require.NoError(t, err)
	require.Len(t, metadata[0].Values, 2)
	for _, field := range metadata[0].Values {
		require.Nil(t, field.Value)
	}
}

func TestMetadataService_ValidateDocumentMetadataChangesRejectsArchivedDefinition(t *testing.T) {
	service, _, _, ctx := setupMetadataServiceTest(t)
	definition, err := service.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a",
		Name:            "legacy_owner",
		ValueType:       types.MetadataValueTypeText,
	})
	require.NoError(t, err)
	require.NoError(t, service.ArchiveDefinition(ctx, "kb-a", definition.ID))

	err = service.ValidateDocumentMetadataChanges(ctx, "kb-a", []types.MetadataValueChange{{
		MetadataDefinitionID: definition.ID,
		Value:                "platform",
		ValueSet:             true,
	}})
	require.ErrorContains(t, err, "metadata definition is archived")
}

func intPointer(value int) *int { return &value }

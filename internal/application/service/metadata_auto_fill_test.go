package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

type metadataAutoFillTaskEnqueuer struct {
	seen map[string]struct{}
}

func (e *metadataAutoFillTaskEnqueuer) Enqueue(
	_ *asynq.Task,
	opts ...asynq.Option,
) (*asynq.TaskInfo, error) {
	var taskID string
	for _, option := range opts {
		if option.Type() == asynq.TaskIDOpt {
			taskID, _ = option.Value().(string)
		}
	}
	if _, exists := e.seen[taskID]; exists {
		return nil, asynq.ErrTaskIDConflict
	}
	e.seen[taskID] = struct{}{}
	return &asynq.TaskInfo{ID: taskID, Type: types.TypeMetadataAutoFill, Queue: "low"}, nil
}

func TestAutomaticSourceMappingResultsConvertsTypedValues(t *testing.T) {
	knowledge := &types.Knowledge{
		Title: "Runbook", FileType: "pdf",
		Metadata: types.JSON(`{"score":"42.5","published":"true","teams":"Platform,Security"}`),
	}
	definitions := []*types.MetadataDefinition{
		autoSourceDefinition("title", types.MetadataValueTypeText, "title", nil),
		autoSourceDefinition("score", types.MetadataValueTypeNumber, "score", nil),
		autoSourceDefinition("published", types.MetadataValueTypeBoolean, "published", nil),
		autoSourceDefinition("teams", types.MetadataValueTypeMultiSelect, "teams", []types.MetadataOption{
			{ID: "team-platform", Label: "Platform", Status: types.MetadataStatusActive},
			{ID: "team-security", Label: "Security", Status: types.MetadataStatusActive},
		}),
	}

	results := automaticSourceMappingResults(knowledge, definitions)
	require.Len(t, results, 4)
	require.Equal(t, "Runbook", results[0].Value)
	require.Equal(t, 42.5, results[1].Value)
	require.Equal(t, true, results[2].Value)
	require.Equal(t, []string{"team-platform", "team-security"}, results[3].Value)
}

func TestDecodeMetadataExtractionUsesJSONNumbersAndRejectsInvalidOutput(t *testing.T) {
	values, err := decodeMetadataExtraction("```json\n{\"score\": 42.5, \"enabled\": false}\n```")
	require.NoError(t, err)
	require.IsType(t, json.Number(""), values["score"])
	require.Equal(t, false, values["enabled"])

	_, err = decodeMetadataExtraction("not-json")
	require.Error(t, err)
}

func TestMetadataAutoFillHandleSkipsDeletedDocument(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledges (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			deleted_at DATETIME
		)
	`).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, deleted_at) VALUES (?, ?, ?, ?)",
		"doc-deleted", 100, "kb-a", time.Now(),
	).Error)

	service := &metadataAutoFillService{knowledgeRepo: repository.NewKnowledgeRepository(db)}
	payload, err := json.Marshal(types.MetadataAutoFillPayload{
		TenantID: 100, KnowledgeBaseID: "kb-a", KnowledgeID: "doc-deleted",
	})
	require.NoError(t, err)

	require.NoError(t, service.Handle(context.Background(), asynq.NewTask(types.TypeMetadataAutoFill, payload)))
}

func TestMetadataAutoFillEnqueueUsesRuleRevisionTaskID(t *testing.T) {
	metadataService, metadataRepo, db, ctx := setupMetadataServiceTest(t)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "doc-a", TenantID: 100, KnowledgeBaseID: "kb-a", Type: "manual", Title: "Document",
	}).Error)
	definition, err := metadataService.ConfigureDefinition(ctx, types.ConfigureMetadataDefinition{
		KnowledgeBaseID: "kb-a", Name: "audience", ValueType: types.MetadataValueTypeText,
	})
	require.NoError(t, err)
	_, err = metadataService.ConfigureAutoRule(ctx, types.ConfigureMetadataAutoRule{
		KnowledgeBaseID: "kb-a", DefinitionID: definition.ID,
		Strategy: types.MetadataRuleStrategySourceMapping,
		Config:   types.JSONMap{"source_key": "title"},
	})
	require.NoError(t, err)

	enqueuer := &metadataAutoFillTaskEnqueuer{seen: make(map[string]struct{})}
	autoFill := &metadataAutoFillService{
		repo: metadataRepo, knowledgeRepo: repository.NewKnowledgeRepository(db), taskEnqueuer: enqueuer,
	}
	payload := types.MetadataAutoFillPayload{
		TenantID: 100, KnowledgeBaseID: "kb-a", KnowledgeID: "doc-a", Trigger: "manual_rerun",
	}
	firstTaskID, err := autoFill.Enqueue(ctx, payload)
	require.NoError(t, err)
	require.NotEmpty(t, firstTaskID)
	duplicateTaskID, err := autoFill.Enqueue(ctx, payload)
	require.NoError(t, err)
	require.Equal(t, firstTaskID, duplicateTaskID)

	_, err = metadataService.ConfigureAutoRule(ctx, types.ConfigureMetadataAutoRule{
		KnowledgeBaseID: "kb-a", DefinitionID: definition.ID,
		Strategy: types.MetadataRuleStrategySourceMapping,
		Config:   types.JSONMap{"source_key": "source"},
	})
	require.NoError(t, err)
	revisedTaskID, err := autoFill.Enqueue(ctx, payload)
	require.NoError(t, err)
	require.NotEqual(t, firstTaskID, revisedTaskID)
}

func autoSourceDefinition(
	id string,
	valueType types.MetadataValueType,
	sourceKey string,
	options []types.MetadataOption,
) *types.MetadataDefinition {
	return &types.MetadataDefinition{
		ID: id, ValueType: valueType, Options: options,
		AutoRule: &types.MetadataAutoRule{
			ID: "rule-" + id, Strategy: types.MetadataRuleStrategySourceMapping,
			Config: types.JSONMap{"source_key": sourceKey}, Revision: 1, Enabled: true,
		},
	}
}

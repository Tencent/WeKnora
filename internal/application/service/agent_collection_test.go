package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newAgentCollectionTestService(t *testing.T) interfacesAgentCollectionService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.AgentCollectionProfile{},
		&types.AgentCollectionHistory{},
		&types.AgentCollectionExport{},
	))
	return NewAgentCollectionService(repository.NewAgentCollectionRepository(db))
}

type interfacesAgentCollectionService interface {
	Prepare(context.Context, types.PrepareCollectionInput) (*types.PreparedCollection, error)
	ApplyStructuredAnswer(context.Context, types.StructuredCollectionAnswerInput) (*types.AgentCollectionProfile, error)
	ApplyExtractedValues(context.Context, types.ExtractedCollectionValuesInput) (*types.AgentCollectionProfile, error)
	UpdateAsSystemAdmin(context.Context, types.SystemAdminCollectionUpdateInput) (*types.AgentCollectionProfile, error)
	ListHistory(context.Context, string, int, int) (*types.AgentCollectionHistoryPage, error)
}

type collectionCleanupRecorder struct {
	agentIDs []string
	userIDs  []string
}

func (r *collectionCleanupRecorder) SoftDeleteByAgent(_ context.Context, agentID string) error {
	r.agentIDs = append(r.agentIDs, agentID)
	return nil
}

func (r *collectionCleanupRecorder) SoftDeleteByUser(_ context.Context, userID string) error {
	r.userIDs = append(r.userIDs, userID)
	return nil
}

func collectionServiceConfig() types.CustomAgentConfig {
	return types.CustomAgentConfig{
		CollectionEnabled:             true,
		CollectionExtractFromMessages: true,
		CollectionSchemaVersion:       3,
		CollectionExtractionThreshold: 0.85,
		CollectionFields: []types.AgentCollectionField{
			{Key: "status", Label: "处理状态", Type: types.AgentCollectionSingleChoice, Required: true, Enabled: true, Order: 1,
				Options: []types.AgentCollectionOption{{ID: "dismissed", Label: "被辞退"}, {ID: "employed", Label: "在职"}}},
			{Key: "reason", Label: "辞退原因", Type: types.AgentCollectionShortText, Required: true, Enabled: true, Order: 2,
				VisibleWhen: &types.AgentCollectionCondition{Field: "status", Operator: "equals", Value: "dismissed"}},
			{Key: "note", Label: "补充说明", Type: types.AgentCollectionLongText, Enabled: true, Order: 3},
		},
	}
}

func collectionPrepareInput(config types.CustomAgentConfig) types.PrepareCollectionInput {
	return types.PrepareCollectionInput{
		TenantID: 8, AgentTenantID: 7, AgentID: "agent-1", UserID: "user-1", Config: config,
	}
}

func TestAgentCollectionServiceRecalculatesConditionalQuestions(t *testing.T) {
	ctx := context.Background()
	service := newAgentCollectionTestService(t)
	config := collectionServiceConfig()

	prepared, err := service.Prepare(ctx, collectionPrepareInput(config))
	require.NoError(t, err)
	require.Equal(t, []string{"status"}, collectionFieldKeys(prepared.MissingFields))
	require.Equal(t, 1, prepared.RemainingCount)
	require.Equal(t, uint64(7), prepared.Profile.AgentTenantID)

	_, err = service.ApplyStructuredAnswer(ctx, types.StructuredCollectionAnswerInput{
		PrepareCollectionInput: collectionPrepareInput(config), FieldKey: "status",
		SchemaVersion: 3, Value: "dismissed", SourceMessageID: "message-1",
	})
	require.NoError(t, err)

	prepared, err = service.Prepare(ctx, collectionPrepareInput(config))
	require.NoError(t, err)
	require.Equal(t, []string{"reason"}, collectionFieldKeys(prepared.MissingFields))
	require.Equal(t, 1, prepared.CompletedCount)
	require.Equal(t, 1, prepared.RemainingCount)
}

func TestAgentCollectionServiceMovesDisabledValuesAndRestoresThem(t *testing.T) {
	ctx := context.Background()
	service := newAgentCollectionTestService(t)
	config := collectionServiceConfig()
	_, err := service.ApplyStructuredAnswer(ctx, types.StructuredCollectionAnswerInput{
		PrepareCollectionInput: collectionPrepareInput(config), FieldKey: "status", SchemaVersion: 3, Value: "employed",
	})
	require.NoError(t, err)

	disabled := config
	disabled.CollectionFields = append([]types.AgentCollectionField(nil), config.CollectionFields...)
	disabled.CollectionFields[0].Enabled = false
	disabled.CollectionSchemaVersion = 4
	prepared, err := service.Prepare(ctx, collectionPrepareInput(disabled))
	require.NoError(t, err)
	require.NotContains(t, prepared.Profile.Values, "status")
	require.Contains(t, prepared.Profile.InactiveValues, "status")

	restored := config
	restored.CollectionSchemaVersion = 5
	prepared, err = service.Prepare(ctx, collectionPrepareInput(restored))
	require.NoError(t, err)
	require.Contains(t, prepared.Profile.Values, "status")
	require.NotContains(t, prepared.Profile.InactiveValues, "status")
}

func TestAgentCollectionServiceFiltersExtractionAndAuditsAdminUpdate(t *testing.T) {
	ctx := context.Background()
	service := newAgentCollectionTestService(t)
	config := collectionServiceConfig()
	now := time.Now().UTC()

	profile, err := service.ApplyExtractedValues(ctx, types.ExtractedCollectionValuesInput{
		PrepareCollectionInput: collectionPrepareInput(config), SourceMessageID: "message-2", SourceMessageAt: &now,
		Values: []types.ExtractedCollectionValue{
			{FieldKey: "status", Value: "employed", Confidence: 0.8},
			{FieldKey: "note", Value: "需要核对补偿", Confidence: 0.96},
		},
	})
	require.NoError(t, err)
	require.NotContains(t, profile.Values, "status")
	require.Contains(t, profile.Values, "note")

	profile, err = service.UpdateAsSystemAdmin(ctx, types.SystemAdminCollectionUpdateInput{
		ProfileID: profile.ID, Config: config, FieldKey: "status", Value: "dismissed",
		ActorUserID: "admin-1", ChangeReason: "用户申请更正",
	})
	require.NoError(t, err)
	history, err := service.ListHistory(ctx, profile.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, types.CollectionSourceSystemAdmin, history.Items[0].Source)
	require.Equal(t, "admin-1", history.Items[0].ActorUserID)
	require.Equal(t, "用户申请更正", history.Items[0].ChangeReason)
}

func TestAgentCollectionServiceCleansProfilesAfterOwnerDeletion(t *testing.T) {
	ctx := collectionServiceContext()
	cleaner := &collectionCleanupRecorder{}
	agentRepo := &collectionAgentRepo{existing: &types.CustomAgent{ID: "agent-1", TenantID: 7}}
	agentService := &customAgentService{repo: agentRepo, collectionRepo: cleaner}
	require.NoError(t, agentService.DeleteAgent(ctx, "agent-1"))
	require.Equal(t, []string{"agent-1"}, cleaner.agentIDs)

	userRepo := &stubUserRepoForAuth{users: map[string]*types.User{"user-1": {ID: "user-1"}}}
	userService := &userService{userRepo: userRepo, collectionRepo: cleaner}
	require.NoError(t, userService.DeleteUser(context.Background(), "user-1"))
	require.Equal(t, []string{"user-1"}, cleaner.userIDs)
}

func collectionFieldKeys(fields []types.AgentCollectionField) []string {
	keys := make([]string, len(fields))
	for index := range fields {
		keys[index] = fields[index].Key
	}
	return keys
}

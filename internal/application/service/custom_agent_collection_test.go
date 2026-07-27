package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type collectionAgentRepo struct {
	existing *types.CustomAgent
	created  *types.CustomAgent
	updated  *types.CustomAgent
}

func (r *collectionAgentRepo) CreateAgent(_ context.Context, agent *types.CustomAgent) error {
	r.created = agent
	return nil
}

func (r *collectionAgentRepo) GetAgentByID(_ context.Context, _ string, _ uint64) (*types.CustomAgent, error) {
	return r.existing, nil
}

func (r *collectionAgentRepo) ListAgentsByTenantID(context.Context, uint64) ([]*types.CustomAgent, error) {
	return nil, nil
}

func (r *collectionAgentRepo) UpdateAgent(_ context.Context, agent *types.CustomAgent) error {
	r.updated = agent
	return nil
}

func (r *collectionAgentRepo) DeleteAgent(context.Context, string, uint64) error { return nil }
func (r *collectionAgentRepo) CountByModelID(context.Context, uint64, string) (int64, error) {
	return 0, nil
}

func collectionServiceContext() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
	return ctx
}

func serviceCollectionConfig() types.CustomAgentConfig {
	return types.CustomAgentConfig{
		CollectionEnabled: true,
		CollectionFields: []types.AgentCollectionField{
			{Key: "case_summary", Label: "请简要描述情况", Type: types.AgentCollectionShortText, Required: true, Enabled: true, Order: 10},
		},
	}
}

func TestCreateAgentNormalizesAndPublishesCollectionSchema(t *testing.T) {
	repo := &collectionAgentRepo{}
	service := &customAgentService{repo: repo}
	agent := &types.CustomAgent{Name: "法务助手", Config: serviceCollectionConfig()}

	created, err := service.CreateAgent(collectionServiceContext(), agent)
	if err != nil {
		t.Fatalf("CreateAgent() error = %v", err)
	}
	if created.Config.CollectionSchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", created.Config.CollectionSchemaVersion)
	}
	if created.Config.CollectionExtractionThreshold != types.DefaultCollectionExtractionThreshold {
		t.Fatalf("threshold = %v", created.Config.CollectionExtractionThreshold)
	}
}

func TestCreateAgentRejectsInvalidCollectionSchemaBeforeWrite(t *testing.T) {
	repo := &collectionAgentRepo{}
	service := &customAgentService{repo: repo}
	config := serviceCollectionConfig()
	config.CollectionFields[0].Label = "请输入 API Key"

	_, err := service.CreateAgent(collectionServiceContext(), &types.CustomAgent{Name: "法务助手", Config: config})
	if err == nil {
		t.Fatal("expected collection validation error")
	}
	if repo.created != nil {
		t.Fatal("repository write occurred for invalid schema")
	}
}

func TestUpdateAgentCollectionSchemaVersionRules(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*types.CustomAgentConfig)
		wantError bool
		wantVer   int64
	}{
		{name: "goal only", mutate: func(c *types.CustomAgentConfig) { c.CollectionGoal = "新的采集目标" }, wantVer: 4},
		{name: "label change", mutate: func(c *types.CustomAgentConfig) { c.CollectionFields[0].Label = "请描述案件事实" }, wantVer: 5},
		{name: "type change", mutate: func(c *types.CustomAgentConfig) { c.CollectionFields[0].Type = types.AgentCollectionLongText }, wantError: true},
		{name: "physical delete", mutate: func(c *types.CustomAgentConfig) { c.CollectionFields = nil }, wantVer: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := serviceCollectionConfig()
			base.CollectionSchemaVersion = 4
			repo := &collectionAgentRepo{existing: &types.CustomAgent{ID: "agent-1", TenantID: 7, Name: "法务助手", Config: base}}
			service := &customAgentService{repo: repo}
			incoming := base
			incoming.CollectionFields = append([]types.AgentCollectionField(nil), base.CollectionFields...)
			tt.mutate(&incoming)

			updated, err := service.UpdateAgent(collectionServiceContext(), &types.CustomAgent{ID: "agent-1", Name: "法务助手", Config: incoming})
			if tt.wantError {
				if err == nil {
					t.Fatal("expected update error")
				}
				if repo.updated != nil {
					t.Fatal("repository write occurred for invalid update")
				}
				return
			}
			if err != nil {
				t.Fatalf("UpdateAgent() error = %v", err)
			}
			if updated.Config.CollectionSchemaVersion != tt.wantVer {
				t.Fatalf("schema version = %d, want %d", updated.Config.CollectionSchemaVersion, tt.wantVer)
			}
		})
	}
}

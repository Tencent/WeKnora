package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/datasource/connector/git_repo"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeIndependentGitRepoCredentialsKeepsOmittedSecret(t *testing.T) {
	existing := map[string]interface{}{
		"access_token":   "old-token",
		"webhook_secret": "old-hook",
	}
	got := mergeIndependentGitRepoCredentials(types.ConnectorTypeGitRepo, existing, map[string]interface{}{
		"access_token": "new-token",
	})
	assert.Equal(t, "new-token", got["access_token"])
	assert.Equal(t, "old-hook", got["webhook_secret"])

	got = mergeIndependentGitRepoCredentials(types.ConnectorTypeGitRepo, existing, map[string]interface{}{
		"webhook_secret": "new-hook",
	})
	assert.Equal(t, "old-token", got["access_token"])
	assert.Equal(t, "new-hook", got["webhook_secret"])
}

func TestMergeIndependentGitRepoCredentialsDoesNotPatchOtherConnectors(t *testing.T) {
	existing := map[string]interface{}{"app_id": "keep", "app_secret": "old"}
	got := mergeIndependentGitRepoCredentials(types.ConnectorTypeFeishu, existing, map[string]interface{}{
		"app_secret": "new",
	})
	assert.Equal(t, map[string]interface{}{"app_secret": "new"}, got)
}

func TestMigrateGitRepoWebhookSecretMovesSettingsIntoCredentials(t *testing.T) {
	existing := &types.DataSourceConfig{
		Type:     types.ConnectorTypeGitRepo,
		Settings: map[string]interface{}{"webhook_secret": "legacy", "repos": []interface{}{}},
	}
	incoming := &types.DataSourceConfig{
		Type:        types.ConnectorTypeGitRepo,
		Settings:    map[string]interface{}{"repos": []interface{}{}},
		Credentials: map[string]interface{}{"access_token": "tok"},
	}
	migrateGitRepoWebhookSecret(existing, incoming)
	assert.Equal(t, "legacy", incoming.Credentials["webhook_secret"])
	_, still := incoming.Settings["webhook_secret"]
	assert.False(t, still)
}

func TestMigrateGitRepoWebhookSecretStripsIncomingSettingsSecret(t *testing.T) {
	incoming := &types.DataSourceConfig{
		Type:     types.ConnectorTypeGitRepo,
		Settings: map[string]interface{}{"webhook_secret": "plain-from-api"},
	}
	migrateGitRepoWebhookSecret(nil, incoming)
	assert.Equal(t, "plain-from-api", incoming.Credentials["webhook_secret"])
	_, still := incoming.Settings["webhook_secret"]
	assert.False(t, still)
}

func TestShouldLiveValidateDataSourcePublicGitRepo(t *testing.T) {
	cfg := &types.DataSourceConfig{Type: types.ConnectorTypeGitRepo}
	assert.True(t, shouldLiveValidateDataSource(types.ConnectorTypeGitRepo, cfg))
	assert.False(t, shouldLiveValidateDataSource(types.ConnectorTypeNotion, cfg))
}

func TestUpdateDataSourceCredentialsMergesGitRepoSecrets(t *testing.T) {
	cfg := types.DataSourceConfig{
		Type: types.ConnectorTypeGitRepo,
		Credentials: map[string]interface{}{
			"access_token":   "old-token",
			"webhook_secret": "old-hook",
		},
	}
	blob, err := cfg.ToJSON()
	require.NoError(t, err)
	ds := &types.DataSource{
		ID:              "ds-git-creds",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            types.ConnectorTypeGitRepo,
		Status:          types.DataSourceStatusActive,
		Config:          blob,
	}
	reg := datasource.NewConnectorRegistry()
	require.NoError(t, reg.Register(git_repo.NewConnector()))
	svc := &DataSourceService{
		dsRepo:            newKBDeleteDSRepo("kb-1", ds),
		connectorRegistry: reg,
	}

	updated, err := svc.UpdateDataSourceCredentials(context.Background(), ds.ID, map[string]interface{}{
		"access_token": "rotated-token",
	})
	require.NoError(t, err)
	parsed, err := updated.ParseConfig()
	require.NoError(t, err)
	assert.Equal(t, "rotated-token", parsed.Credentials["access_token"])
	assert.Equal(t, "old-hook", parsed.Credentials["webhook_secret"])
}

func TestEnqueueSyncCoalescesWebhookOnly(t *testing.T) {
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	ds := &types.DataSource{
		ID:              "ds-sync",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            types.ConnectorTypeGitRepo,
		Status:          types.DataSourceStatusActive,
	}
	running := &types.SyncLog{
		ID:           "log-running",
		DataSourceID: ds.ID,
		Status:       types.SyncLogStatusRunning,
	}
	enq := &countingTaskEnqueuer{}
	logs := &coalesceSyncLogRepo{running: running}
	svc := &DataSourceService{
		dsRepo:       newKBDeleteDSRepo("kb-1", ds),
		syncLogRepo:  logs,
		taskEnqueuer: enq,
	}

	got, err := svc.WebhookSync(context.Background(), ds.ID)
	require.NoError(t, err)
	assert.Equal(t, running.ID, got.ID)
	assert.Equal(t, 0, enq.calls)

	got, err = svc.ManualSync(context.Background(), ds.ID)
	require.NoError(t, err)
	assert.NotEqual(t, running.ID, got.ID)
	assert.Equal(t, 1, enq.calls)

	// After the running log closes, the coalesced push must still be synced.
	logs.running = nil
	svc.flushWebhookResync(context.Background(), ds)
	assert.Equal(t, 2, enq.calls)
}

func TestEnqueueSyncSkipsPausedWebhook(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-paused",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            types.ConnectorTypeGitRepo,
		Status:          types.DataSourceStatusPaused,
	}
	enq := &countingTaskEnqueuer{}
	svc := &DataSourceService{
		dsRepo:       newKBDeleteDSRepo("kb-1", ds),
		syncLogRepo:  &coalesceSyncLogRepo{},
		taskEnqueuer: enq,
	}

	_, err := svc.WebhookSync(context.Background(), ds.ID)
	require.ErrorIs(t, err, datasource.ErrDataSourcePaused)
	assert.Equal(t, 0, enq.calls)

	got, err := svc.ManualSync(context.Background(), ds.ID)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, 1, enq.calls)
}

func TestFlushWebhookResyncSkipsPaused(t *testing.T) {
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	ds := &types.DataSource{
		ID:              "ds-paused-flush",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            types.ConnectorTypeGitRepo,
		Status:          types.DataSourceStatusPaused,
	}
	require.NoError(t, git_repo.MarkWebhookResync(ds.TenantID, ds.ID))
	enq := &countingTaskEnqueuer{}
	svc := &DataSourceService{
		dsRepo:       newKBDeleteDSRepo("kb-1", ds),
		syncLogRepo:  &coalesceSyncLogRepo{},
		taskEnqueuer: enq,
	}
	svc.flushWebhookResync(context.Background(), ds)
	assert.Equal(t, 0, enq.calls)
	assert.True(t, git_repo.ConsumeWebhookResync(ds.TenantID, ds.ID),
		"paused flush must leave the marker for a later active run")
}

func TestDeleteDataSourceRemovesGitRepoClone(t *testing.T) {
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	ds := &types.DataSource{
		ID:              "ds-clean",
		TenantID:        9,
		KnowledgeBaseID: "kb-1",
		Type:            types.ConnectorTypeGitRepo,
		Status:          types.DataSourceStatusActive,
	}
	dir := git_repo.CloneStorageDir(ds.TenantID, ds.ID)
	require.NotEmpty(t, dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "repo"), 0o700))

	syncLogs := &kbDeleteSyncLogRepo{}
	svc := &DataSourceService{
		dsRepo:      newKBDeleteDSRepo("kb-1", ds),
		syncLogRepo: syncLogs,
	}
	require.NoError(t, svc.DeleteDataSource(context.Background(), ds.ID))
	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "clone dir should be removed")
}

type countingTaskEnqueuer struct {
	calls int
}

func (e *countingTaskEnqueuer) Enqueue(_ *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.calls++
	return &asynq.TaskInfo{ID: "task"}, nil
}

type coalesceSyncLogRepo struct {
	kbDeleteSyncLogRepo
	running *types.SyncLog
	created []*types.SyncLog
}

func (r *coalesceSyncLogRepo) HasRunningSync(context.Context, string) (bool, error) {
	return r.running != nil, nil
}

func (r *coalesceSyncLogRepo) FindLatest(context.Context, string) (*types.SyncLog, error) {
	return r.running, nil
}

func (r *coalesceSyncLogRepo) Create(_ context.Context, log *types.SyncLog) error {
	if log.ID == "" {
		log.ID = "log-new"
	}
	r.created = append(r.created, log)
	return nil
}

func TestMigrateDoesNotLeakSettingsSecretInJSON(t *testing.T) {
	incoming := &types.DataSourceConfig{
		Type:     types.ConnectorTypeGitRepo,
		Settings: map[string]interface{}{"webhook_secret": "plain", "repos": []interface{}{}},
	}
	migrateGitRepoWebhookSecret(nil, incoming)
	raw, err := json.Marshal(incoming.Settings)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "plain")
}

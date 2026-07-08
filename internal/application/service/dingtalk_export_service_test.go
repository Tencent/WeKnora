package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDingTalkExportServiceHandlesSuccessEvent(t *testing.T) {
	db := setupDingTalkExportServiceDB(t)
	taskRepo := apprepo.NewDingTalkExportTaskRepository(db)
	require.NoError(t, taskRepo.UpsertPending(context.Background(), &types.DingTalkExportTask{
		TaskID:           "task-1",
		DataSourceID:     "ds-1",
		TenantID:         7,
		ExternalID:       "ws1:doc1",
		SourceResourceID: "ws1:root",
		WorkspaceID:      "ws1",
		NodeID:           "doc1",
		DentryUUID:       "doc1",
		Title:            "Architecture",
		FileName:         "Architecture.md",
		SourceURL:        "https://dingtalk.example/wiki/doc1",
	}))

	exportServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Architecture\n\nexported markdown"))
	}))
	defer exportServer.Close()

	ingestor := &recordingExportIngestor{}
	svc := &DingTalkExportService{
		taskRepo:   taskRepo,
		ingestor:   ingestor,
		httpClient: exportServer.Client(),
	}

	payload := []byte(`{
		"EventType": "dingdoc_export_finish",
		"eventId": "evt-1",
		"biz_data": {
			"taskId": "task-1",
			"dentryUuid": "doc1",
			"name": "Architecture",
			"format": "markdown",
			"extension": "adoc",
			"success": true,
			"url": "` + exportServer.URL + `/export.md"
		}
	}`)

	require.NoError(t, svc.HandleExportFinishEvent(context.Background(), payload))

	require.Len(t, ingestor.items, 1)
	assert.Equal(t, "ds-1", ingestor.dataSourceIDs[0])
	item := ingestor.items[0]
	assert.Equal(t, "ws1:doc1", item.ExternalID)
	assert.Equal(t, "Architecture.md", item.FileName)
	assert.Equal(t, "text/markdown", item.ContentType)
	assert.Contains(t, string(item.Content), "exported markdown")
	assert.Equal(t, "official_markdown_export", item.Metadata["fidelity"])
	assert.Equal(t, "task-1", item.Metadata["export_task_id"])

	stored, err := taskRepo.FindByTaskID(context.Background(), "task-1")
	require.NoError(t, err)
	assert.Equal(t, types.DingTalkExportTaskStatusSucceeded, stored.Status)
	assert.Equal(t, "evt-1", stored.EventID)
	assert.Equal(t, exportServer.URL+"/export.md", stored.ExportURL)
}

func TestDingTalkExportServiceHandlesFailedEventWithoutIngesting(t *testing.T) {
	db := setupDingTalkExportServiceDB(t)
	taskRepo := apprepo.NewDingTalkExportTaskRepository(db)
	require.NoError(t, taskRepo.UpsertPending(context.Background(), &types.DingTalkExportTask{
		TaskID:       "task-fail",
		DataSourceID: "ds-1",
		TenantID:     7,
		ExternalID:   "ws1:doc1",
		DentryUUID:   "doc1",
		Title:        "Architecture",
		FileName:     "Architecture.md",
	}))

	ingestor := &recordingExportIngestor{}
	svc := &DingTalkExportService{
		taskRepo: taskRepo,
		ingestor: ingestor,
	}

	payload := []byte(`{
		"EventType": "dingdoc_export_finish",
		"eventId": "evt-fail",
		"biz_data": {
			"taskId": "task-fail",
			"dentryUuid": "doc1",
			"success": false,
			"errorCode": "52622003",
			"errorMsg": "task initial cp not found"
		}
	}`)

	require.NoError(t, svc.HandleExportFinishEvent(context.Background(), payload))
	assert.Empty(t, ingestor.items)

	stored, err := taskRepo.FindByTaskID(context.Background(), "task-fail")
	require.NoError(t, err)
	assert.Equal(t, types.DingTalkExportTaskStatusFailed, stored.Status)
	assert.Equal(t, "52622003", stored.ErrorCode)
	assert.Equal(t, "task initial cp not found", stored.ErrorMessage)
}

func TestDingTalkExportServiceRejectsMismatchedDentryUUID(t *testing.T) {
	db := setupDingTalkExportServiceDB(t)
	taskRepo := apprepo.NewDingTalkExportTaskRepository(db)
	require.NoError(t, taskRepo.UpsertPending(context.Background(), &types.DingTalkExportTask{
		TaskID:       "task-1",
		DataSourceID: "ds-1",
		TenantID:     7,
		ExternalID:   "ws1:doc1",
		DentryUUID:   "doc1",
		Title:        "Architecture",
		FileName:     "Architecture.md",
	}))

	ingestor := &recordingExportIngestor{}
	svc := &DingTalkExportService{
		taskRepo: taskRepo,
		ingestor: ingestor,
	}

	payload := []byte(`{
		"EventType": "dingdoc_export_finish",
		"eventId": "evt-1",
		"biz_data": {
			"taskId": "task-1",
			"dentryUuid": "another-doc",
			"success": true,
			"url": "https://example.com/export.md"
		}
	}`)

	err := svc.HandleExportFinishEvent(context.Background(), payload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dentry uuid mismatch")
	assert.Empty(t, ingestor.items)
}

func TestDataSourceServicePersistsPendingDingTalkExportItem(t *testing.T) {
	repo := &recordingExportTaskRepo{}
	svc := &DataSourceService{dingtalkExportTaskRepo: repo}

	handled, err := svc.persistPendingDingTalkExportTask(
		context.Background(),
		&types.DataSource{ID: "ds-1", TenantID: 7},
		&types.SyncLog{ID: "log-1"},
		&types.FetchedItem{
			ExternalID:       "ws1:doc1",
			Title:            "Architecture",
			FileName:         "Architecture.md",
			URL:              "https://dingtalk.example/wiki/doc1",
			SourceResourceID: "ws1:root",
			Metadata: map[string]string{
				"channel":        types.ChannelDingtalk,
				"fetcher":        "export",
				"export_status":  "pending",
				"export_task_id": "task-1",
				"workspace_id":   "ws1",
				"node_id":        "doc1",
				"dentry_uuid":    "doc1",
			},
		},
	)

	require.NoError(t, err)
	require.True(t, handled)
	require.Len(t, repo.tasks, 1)
	task := repo.tasks[0]
	assert.Equal(t, "task-1", task.TaskID)
	assert.Equal(t, "ds-1", task.DataSourceID)
	assert.Equal(t, "log-1", task.SyncLogID)
	assert.Equal(t, uint64(7), task.TenantID)
	assert.Equal(t, "ws1:doc1", task.ExternalID)
	assert.Equal(t, "Architecture.md", task.FileName)
}

func setupDingTalkExportServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.DingTalkExportTask{}))
	return db
}

type recordingExportIngestor struct {
	dataSourceIDs []string
	items         []*types.FetchedItem
}

func (r *recordingExportIngestor) IngestFetchedItem(
	_ context.Context,
	dataSourceID string,
	item *types.FetchedItem,
) error {
	r.dataSourceIDs = append(r.dataSourceIDs, dataSourceID)
	r.items = append(r.items, item)
	return nil
}

type recordingExportTaskRepo struct {
	tasks []*types.DingTalkExportTask
}

func (r *recordingExportTaskRepo) UpsertPending(_ context.Context, task *types.DingTalkExportTask) error {
	r.tasks = append(r.tasks, task)
	return nil
}

func (r *recordingExportTaskRepo) FindByTaskID(context.Context, string) (*types.DingTalkExportTask, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingExportTaskRepo) FindPendingOlderThan(
	context.Context,
	time.Time,
	int,
) ([]*types.DingTalkExportTask, error) {
	return nil, nil
}

func (r *recordingExportTaskRepo) MarkSucceeded(context.Context, string, string, string) error {
	return nil
}

func (r *recordingExportTaskRepo) MarkFailed(context.Context, string, string, string, string) error {
	return nil
}

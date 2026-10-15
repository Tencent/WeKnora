package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type incompleteSyncConnector struct {
	mixedSyncConnector
	warning string
}

func (c incompleteSyncConnector) FetchAll(
	context.Context, *types.DataSourceConfig, []string,
) ([]types.FetchedItem, error) {
	return []types.FetchedItem{{
		ExternalID: "doc:1", Title: "Large table", Content: []byte("retained content"),
		FileName: "table.md", Metadata: map[string]string{"content_warning": c.warning},
	}}, nil
}

func TestProcessSync_ContentWarningKeepsImportedDocument(t *testing.T) {
	for _, warning := range []string{"", "Table content was truncated"} {
		t.Run(warning, func(t *testing.T) {
			config, err := (&types.DataSourceConfig{Type: "test-sync-mixed"}).ToJSON()
			require.NoError(t, err)
			ds := &types.DataSource{
				ID: "ds-1", TenantID: 1, KnowledgeBaseID: "kb-1", Type: "test-sync-mixed",
				Config: config, SyncMode: types.SyncModeFull, Status: types.DataSourceStatusActive,
			}
			log := &types.SyncLog{
				ID: "log-1", DataSourceID: ds.ID, TenantID: ds.TenantID,
				Status: types.SyncLogStatusRunning, StartedAt: time.Now().UTC(),
			}
			logs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{log.ID: log}}
			registry := datasource.NewConnectorRegistry()
			require.NoError(t, registry.Register(incompleteSyncConnector{warning: warning}))
			service := &DataSourceService{
				dsRepo: newKBDeleteDSRepo(ds.KnowledgeBaseID, ds), syncLogRepo: logs,
				knowledgeService: &sweepFakeKS{repo: &keyedDeletionRepo{items: map[string]*types.Knowledge{}}},
				kbService: &processSyncKBService{kb: &types.KnowledgeBase{
					ID: ds.KnowledgeBaseID, TenantID: ds.TenantID,
				}},
				connectorRegistry: registry,
				tenantRepo:        &processSyncTenantRepo{tenant: &types.Tenant{ID: ds.TenantID}},
				tagService:        &processSyncTagService{},
			}
			payload, err := json.Marshal(types.DataSourceSyncPayload{
				DataSourceID: ds.ID, TenantID: ds.TenantID, SyncLogID: log.ID, ForceFull: true,
			})
			require.NoError(t, err)
			task := asynq.NewTask(types.TypeDataSourceSync, payload)
			require.NoError(t, service.ProcessSync(context.Background(), task))
			got := logs.logs[log.ID]
			require.Equal(t, 1, got.ItemsCreated)
			require.Zero(t, got.ItemsFailed)
			if warning == "" {
				require.Equal(t, types.SyncLogStatusSuccess, got.Status)
				return
			}
			require.Equal(t, types.SyncLogStatusPartial, got.Status)
			require.Contains(t, string(got.Result), warning)
			require.Contains(t, string(got.Result), "Large table")
		})
	}
}

func TestBytesToFileHeader_PreservesPayloadAndFilename(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("# 文档\n\x00end"), bytes.Repeat([]byte("row|value\n"), 100000)} {
		name := "table 文档.md"
		header, err := bytesToFileHeader(data, name)
		require.NoError(t, err)
		require.Equal(t, int64(len(data)), header.Size)
		require.Equal(t, name, header.Filename)
		file, err := header.Open()
		require.NoError(t, err)
		got, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		require.True(t, bytes.Equal(data, got))
	}
}

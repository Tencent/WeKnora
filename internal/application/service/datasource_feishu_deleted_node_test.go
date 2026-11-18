package service

import (
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/drive"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/wiki"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type feishuSelectedNodeServer struct {
	srv *httptest.Server

	mu            sync.Mutex
	spaceID       string
	nodes         map[string]core.WikiNode
	docxText      map[string]string
	deletedTokens map[string]bool
	deniedTokens  map[string]bool
	driveFiles    map[string][]core.DriveFile
	deletedDrive  map[string]bool
}

func newFeishuSelectedNodeServer(t *testing.T, spaceID string) *feishuSelectedNodeServer {
	t.Helper()
	f := &feishuSelectedNodeServer{
		spaceID:       spaceID,
		nodes:         map[string]core.WikiNode{},
		docxText:      map[string]string{},
		deletedTokens: map[string]bool{},
		deniedTokens:  map[string]bool{},
		driveFiles:    map[string][]core.DriveFile{},
		deletedDrive:  map[string]bool{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *feishuSelectedNodeServer) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "tenant_access_token": "tok-test", "expire": 7200,
		})
	case r.URL.Path == "/open-apis/wiki/v2/spaces":
		_ = json.NewEncoder(w).Encode(core.WikiSpaceListResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data: core.WikiSpaceListData{
				Items: []core.WikiSpace{{SpaceID: f.spaceID, Name: "研发知识库"}},
			},
		})
	case r.URL.Path == "/open-apis/wiki/v2/spaces/get_node":
		tok := r.URL.Query().Get("token")
		if f.deletedTokens[tok] {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(
				`{"code":131005,"msg":"not found: node has been deleted","error":{"log_id":"20261118121628"}}`,
			))
			return
		}
		if f.deniedTokens[tok] {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":131006,"msg":"permission denied: node permission denied"}`))
			return
		}
		node, ok := f.nodes[tok]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":131005,"msg":"not found: node has been deleted"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(core.WikiNodeInfoResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.WikiNodeInfoData{Node: node},
		})
	case strings.HasPrefix(r.URL.Path, "/open-apis/docx/v1/documents/") &&
		strings.HasSuffix(r.URL.Path, "/blocks"):
		trimmed := strings.TrimPrefix(r.URL.Path, "/open-apis/docx/v1/documents/")
		objToken := strings.TrimSuffix(trimmed, "/blocks")
		body := f.docxText[objToken]
		_ = json.NewEncoder(w).Encode(core.DocxBlocksResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data: core.DocxBlocksData{
				Items: []core.DocxBlock{
					{BlockID: "root", BlockType: core.BlockTypePage},
					{
						BlockID:   "b1",
						BlockType: core.BlockTypeText,
						Text: &core.BlockText{
							Elements: []core.TextElement{{TextRun: &core.TextRun{Content: body}}},
						},
					},
				},
			},
		})
	case r.URL.Path == "/open-apis/drive/v1/files":
		folderToken := r.URL.Query().Get("folder_token")
		if f.deletedDrive[folderToken] {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":1061003,"msg":"not found"}`))
			return
		}
		files, ok := f.driveFiles[folderToken]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":1061002,"msg":"params error"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(core.DriveFileListResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.DriveFileListData{Files: files},
		})
	case strings.HasPrefix(r.URL.Path, "/open-apis/drive/explorer/v2/folder/"):
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "data": map[string]any{"name": "云盘目录"},
		})
	case strings.HasPrefix(r.URL.Path, "/open-apis/drive/v1/files/") &&
		strings.HasSuffix(r.URL.Path, "/download"):
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("# drive markdown"))
	default:
		http.NotFound(w, r)
	}
}

type feishuKnowledgeRepo struct {
	seafileKnowledgeRepo
}

func (r *feishuKnowledgeRepo) FindByMetadataKeyPrefix(
	_ context.Context, _ uint64, _, _, prefix string,
) ([]*types.Knowledge, error) {
	var out []*types.Knowledge
	for key, row := range r.live {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) == 2 && strings.HasPrefix(parts[1], prefix) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r *feishuKnowledgeRepo) HardDeleteKnowledgeList(_ context.Context, _ uint64, ids []string) error {
	r.hardDeleted = append(r.hardDeleted, ids...)
	return nil
}

type feishuStatefulKnowledgeService struct {
	interfaces.KnowledgeService
	repo     *feishuKnowledgeRepo
	dsID     string
	contents map[string]string
	deleted  []string
	nextSeq  int
}

func (k *feishuStatefulKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return k.repo
}

func (k *feishuStatefulKnowledgeService) CreateKnowledgeFromFile(
	_ context.Context, kbID string, fh *multipart.FileHeader, metadata map[string]string,
	_ *bool, customFileName string, _ []string, channel string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, fh.Size)
	_, _ = f.Read(buf)

	k.nextSeq++
	id := fmt.Sprintf("kn-%d", k.nextSeq)
	extID := metadata["external_id"]
	metaJSON, _ := json.Marshal(metadata)
	row := &types.Knowledge{
		ID:              id,
		KnowledgeBaseID: kbID,
		Title:           strings.TrimSuffix(customFileName, ".md"),
		FileName:        customFileName,
		Channel:         channel,
		Metadata:        types.JSON(metaJSON),
	}
	// Strip folder prefix from Title to mirror real CreateKnowledgeFromFile behaviour.
	if idx := strings.LastIndex(row.Title, "/"); idx >= 0 {
		row.Title = row.Title[idx+1:]
	}
	k.repo.live[k.dsID+"|"+extID] = row
	k.contents[extID] = string(buf)
	return row, nil
}

func (k *feishuStatefulKnowledgeService) ReplaceKnowledgeFromDataSource(
	_ context.Context, existingID string, item *types.FetchedItem,
	metadata map[string]string, _ []string, channel string,
) (*types.Knowledge, error) {
	metaJSON, _ := json.Marshal(metadata)
	row := &types.Knowledge{
		ID:       existingID,
		Title:    item.Title,
		FileName: item.FileName,
		Channel:  channel,
		Metadata: types.JSON(metaJSON),
	}
	k.repo.live[k.dsID+"|"+item.ExternalID] = row
	k.contents[item.ExternalID] = string(item.Content)
	return row, nil
}

func (k *feishuStatefulKnowledgeService) DeleteKnowledge(_ context.Context, id string) error {
	k.deleted = append(k.deleted, id)
	for key, row := range k.repo.live {
		if row != nil && row.ID == id {
			delete(k.repo.live, key)
		}
	}
	return nil
}

func newFeishuSyncServiceHarness(
	t *testing.T, server *feishuSelectedNodeServer, dsType string,
	resourceIDs []string, syncDeletions bool,
) (*DataSourceService, *types.DataSource, *processSyncSyncLogRepo, *feishuStatefulKnowledgeService) {
	t.Helper()
	withSSRFWhitelist(t, "127.0.0.1,localhost")

	cfgJSON, err := (&types.DataSourceConfig{
		Type: dsType,
		Credentials: map[string]any{
			"app_id":     "cli_test",
			"app_secret": "sec_test",
			"base_url":   server.srv.URL,
		},
		ResourceIDs: resourceIDs,
	}).ToJSON()
	require.NoError(t, err)

	ds := &types.DataSource{
		ID:              "ds-feishu-5633",
		TenantID:        1,
		KnowledgeBaseID: "kb-5633",
		Name:            "飞书研发空间",
		Type:            dsType,
		Config:          cfgJSON,
		SyncMode:        types.SyncModeIncremental,
		Status:          types.DataSourceStatusActive,
		SyncDeletions:   syncDeletions,
	}

	repo := &feishuKnowledgeRepo{seafileKnowledgeRepo: seafileKnowledgeRepo{live: map[string]*types.Knowledge{}}}
	ks := &feishuStatefulKnowledgeService{
		repo:     repo,
		dsID:     ds.ID,
		contents: map[string]string{},
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{}}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(wiki.NewConnector(core.RegionFeishu)))
	require.NoError(t, registry.Register(drive.NewDriveConnector(core.RegionFeishuDrive)))

	kb := &types.KnowledgeBase{ID: ds.KnowledgeBaseID, TenantID: ds.TenantID}
	svc := &DataSourceService{
		dsRepo:            newKBDeleteDSRepo(ds.KnowledgeBaseID, ds),
		syncLogRepo:       syncLogRepo,
		knowledgeService:  ks,
		kbService:         &processSyncKBService{kb: kb},
		connectorRegistry: registry,
		tenantRepo:        &processSyncTenantRepo{tenant: &types.Tenant{ID: ds.TenantID}},
		tagService:        &processSyncTagService{},
	}
	return svc, ds, syncLogRepo, ks
}

func runFeishuSyncRound(
	t *testing.T, svc *DataSourceService, ds *types.DataSource,
	syncLogRepo *processSyncSyncLogRepo, logID string,
) (*types.SyncLog, error) {
	t.Helper()
	log := &types.SyncLog{
		ID:           logID,
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo.logs[logID] = log
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    logID,
	})
	require.NoError(t, err)
	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
	return syncLogRepo.logs[logID], err
}

// TestProcessSync_FeishuDeletedDirectlySelectedNodeSkipsAndSyncsRemaining
// reproduces Tencent/WeKnora#5633 end-to-end through DataSourceService and the
// real Feishu Wiki connector:
//  1. Directly select doc A and doc B, sync once -> both created in KB.
//  2. Delete doc A in Feishu (HTTP 400 code=131005 "not found: node has been
//     deleted") and edit doc B -> sync skips A, reconciles A's KB deletion,
//     updates B, and records A's title in SyncResult.DeletedItems.
//  3. Run another sync while A's ID is still in config.resource_ids and B is
//     unchanged -> sync still succeeds (not all-failed) and retains A's title.
func TestProcessSync_FeishuDeletedDirectlySelectedNodeSkipsAndSyncsRemaining(t *testing.T) {
	const (
		spaceID = "7701598185524874545"
		tokenA  = "Ut8Gw3GsbiNIAnkNp2kcaZTpnAe"
		tokenB  = "DocBNodeToken"
	)
	srv := newFeishuSelectedNodeServer(t, spaceID)
	srv.nodes[tokenA] = core.WikiNode{
		SpaceID: spaceID, NodeToken: tokenA, ObjToken: "obj-a",
		ObjType: "docx", Title: "文档 A", ObjEditTime: "100",
	}
	srv.nodes[tokenB] = core.WikiNode{
		SpaceID: spaceID, NodeToken: tokenB, ObjToken: "obj-b",
		ObjType: "docx", Title: "文档 B", ObjEditTime: "100",
	}
	srv.docxText["obj-a"] = "初始内容 A"
	srv.docxText["obj-b"] = "初始内容 B"

	resA := spaceID + ":" + tokenA
	resB := spaceID + ":" + tokenB
	svc, ds, logRepo, ks := newFeishuSyncServiceHarness(
		t, srv, types.ConnectorTypeFeishu, []string{resA, resB}, true,
	)

	// Round 1: initial sync creates both documents.
	log1, err := runFeishuSyncRound(t, svc, ds, logRepo, "log-round-1")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log1.Status)
	assert.Equal(t, 2, log1.ItemsCreated)
	assert.Contains(t, ks.contents[tokenA], "初始内容 A")
	assert.Contains(t, ks.contents[tokenB], "初始内容 B")

	// Round 2: delete doc A in Feishu (131005) and modify doc B.
	srv.mu.Lock()
	delete(srv.nodes, tokenA)
	srv.deletedTokens[tokenA] = true
	nodeB := srv.nodes[tokenB]
	nodeB.ObjEditTime = "200"
	srv.nodes[tokenB] = nodeB
	srv.docxText["obj-b"] = "更新后的内容 B"
	srv.mu.Unlock()

	log2, err := runFeishuSyncRound(t, svc, ds, logRepo, "log-round-2")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log2.Status)
	assert.Equal(t, types.DataSourceStatusActive, ds.Status)
	assert.Empty(t, ds.ErrorMessage)
	assert.Equal(t, 1, log2.ItemsDeleted)
	assert.Equal(t, 1, log2.ItemsUpdated)
	assert.Equal(t, 0, log2.ItemsFailed)
	assert.Nil(t, ks.repo.live[ds.ID+"|"+tokenA], "deleted doc A must be removed from KB")
	assert.Contains(t, ks.contents[tokenB], "更新后的内容 B", "doc B after A must still update")

	res2, err := log2.ParseResult()
	require.NoError(t, err)
	require.Len(t, res2.DeletedItems, 1)
	assert.Equal(t, tokenA, res2.DeletedItems[0].ExternalID)
	assert.Equal(t, resA, res2.DeletedItems[0].SourceResourceID)
	assert.Equal(t, "文档 A", res2.DeletedItems[0].Title)

	// Round 3: A's ID remains in config.resource_ids and B is unchanged.
	log3, err := runFeishuSyncRound(t, svc, ds, logRepo, "log-round-3")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log3.Status)
	assert.Equal(t, types.DataSourceStatusActive, ds.Status)
	assert.Equal(t, 0, log3.ItemsDeleted)
	assert.Equal(t, 1, log3.ItemsSkipped)
	assert.Equal(t, 0, log3.ItemsFailed)

	res3, err := log3.ParseResult()
	require.NoError(t, err)
	require.Len(t, res3.DeletedItems, 1)
	assert.Equal(t, "文档 A", res3.DeletedItems[0].Title, "prior deleted title must survive subsequent rounds")
}

// TestProcessSync_FeishuDeletedSelectedNodeHonorsSyncDeletionsDisabled verifies
// that when SyncDeletions is disabled, deleting a selected node in Feishu still
// skips it and syncs remaining documents, keeps the KB row intact, and records
// the deleted document in SyncResult.DeletedItems.
func TestProcessSync_FeishuDeletedSelectedNodeHonorsSyncDeletionsDisabled(t *testing.T) {
	const (
		spaceID = "space-keep"
		tokenA  = "node-a"
		tokenB  = "node-b"
	)
	srv := newFeishuSelectedNodeServer(t, spaceID)
	srv.nodes[tokenA] = core.WikiNode{
		SpaceID: spaceID, NodeToken: tokenA, ObjToken: "obj-a",
		ObjType: "docx", Title: "保留文档 A", ObjEditTime: "100",
	}
	srv.nodes[tokenB] = core.WikiNode{
		SpaceID: spaceID, NodeToken: tokenB, ObjToken: "obj-b",
		ObjType: "docx", Title: "文档 B", ObjEditTime: "100",
	}
	srv.docxText["obj-a"] = "A1"
	srv.docxText["obj-b"] = "B1"

	svc, ds, logRepo, ks := newFeishuSyncServiceHarness(
		t, srv, types.ConnectorTypeFeishu, []string{spaceID + ":" + tokenA, spaceID + ":" + tokenB}, false,
	)

	_, err := runFeishuSyncRound(t, svc, ds, logRepo, "log-1")
	require.NoError(t, err)

	srv.mu.Lock()
	srv.deletedTokens[tokenA] = true
	b := srv.nodes[tokenB]
	b.ObjEditTime = "200"
	srv.nodes[tokenB] = b
	srv.docxText["obj-b"] = "B2"
	srv.mu.Unlock()

	log2, err := runFeishuSyncRound(t, svc, ds, logRepo, "log-2")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log2.Status)
	assert.Equal(t, 0, log2.ItemsDeleted)
	assert.Equal(t, 1, log2.ItemsSkipped)
	assert.Equal(t, 1, log2.ItemsUpdated)
	assert.NotNil(t, ks.repo.live[ds.ID+"|"+tokenA], "SyncDeletions=false must keep KB copy")

	res2, err := log2.ParseResult()
	require.NoError(t, err)
	require.Len(t, res2.DeletedItems, 1)
	assert.Equal(t, "保留文档 A", res2.DeletedItems[0].Title)
}

// TestProcessSync_FeishuPermissionDeniedNodeStillFailsWithoutDeletingKB ensures
// non-131005 errors (e.g. 131006 permission denied) are NOT misclassified as
// deletions and never remove previously synced documents from the KB.
func TestProcessSync_FeishuPermissionDeniedNodeStillFailsWithoutDeletingKB(t *testing.T) {
	const (
		spaceID = "space-perm"
		tokenA  = "node-secret"
	)
	srv := newFeishuSelectedNodeServer(t, spaceID)
	srv.nodes[tokenA] = core.WikiNode{
		SpaceID: spaceID, NodeToken: tokenA, ObjToken: "obj-a",
		ObjType: "docx", Title: "受限文档", ObjEditTime: "100",
	}
	srv.docxText["obj-a"] = "机密内容"

	svc, ds, logRepo, ks := newFeishuSyncServiceHarness(
		t, srv, types.ConnectorTypeFeishu, []string{spaceID + ":" + tokenA}, true,
	)
	_, err := runFeishuSyncRound(t, svc, ds, logRepo, "log-1")
	require.NoError(t, err)

	srv.mu.Lock()
	srv.deniedTokens[tokenA] = true
	srv.mu.Unlock()

	log2, err := runFeishuSyncRound(t, svc, ds, logRepo, "log-2")
	require.Error(t, err)
	assert.Equal(t, types.SyncLogStatusFailed, log2.Status)
	assert.NotNil(t, ks.repo.live[ds.ID+"|"+tokenA], "permission error must never delete KB data")
	assert.Empty(t, ks.deleted)
}

// TestProcessSync_FeishuDriveDeletedSelectedItemSkipsAndSyncsRemaining covers
// the Feishu Drive sibling connector when a selected sub-item returns 1061003.
func TestProcessSync_FeishuDriveDeletedSelectedItemSkipsAndSyncsRemaining(t *testing.T) {
	srv := newFeishuSelectedNodeServer(t, "unused")
	srv.driveFiles["root-folder"] = []core.DriveFile{
		{Token: "file-a", Name: "驱动文档A.md", Type: "file", ParentToken: "root-folder", ModifiedTime: "100"},
		{Token: "file-b", Name: "驱动文档B.md", Type: "file", ParentToken: "root-folder", ModifiedTime: "100"},
	}

	svc, ds, logRepo, ks := newFeishuSyncServiceHarness(
		t, srv, types.ConnectorTypeFeishuDrive, []string{"root-folder:file-a", "root-folder:file-b"}, true,
	)
	log1, err := runFeishuSyncRound(t, svc, ds, logRepo, "drive-1")
	require.NoError(t, err)
	assert.Equal(t, 2, log1.ItemsCreated)

	srv.mu.Lock()
	srv.driveFiles["root-folder"] = []core.DriveFile{
		{Token: "file-b", Name: "驱动文档B.md", Type: "file", ParentToken: "root-folder", ModifiedTime: "200"},
	}
	srv.deletedDrive["file-a"] = true
	srv.mu.Unlock()

	log2, err := runFeishuSyncRound(t, svc, ds, logRepo, "drive-2")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log2.Status)
	assert.Equal(t, 1, log2.ItemsDeleted)
	assert.Equal(t, 1, log2.ItemsUpdated)
	assert.Nil(t, ks.repo.live[ds.ID+"|file-a"])

	res2, err := log2.ParseResult()
	require.NoError(t, err)
	require.Len(t, res2.DeletedItems, 1)
	assert.Equal(t, "file-a", res2.DeletedItems[0].ExternalID)
	assert.Equal(t, "驱动文档A", res2.DeletedItems[0].Title)
}

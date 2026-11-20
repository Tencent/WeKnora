package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingFeishuKnowledgeService struct {
	*feishuStatefulKnowledgeService
	failByExtID map[string]error
	attempted   []string
}

func (k *failingFeishuKnowledgeService) CreateKnowledgeFromFile(
	ctx context.Context, kbID string, fh *multipart.FileHeader, metadata map[string]string,
	enableMultimodel *bool, customFileName string, tagIDs []string, channel string,
	processOverrides *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	extID := metadata["external_id"]
	k.attempted = append(k.attempted, extID)
	if err := k.failByExtID[extID]; err != nil {
		return nil, err
	}
	return k.feishuStatefulKnowledgeService.CreateKnowledgeFromFile(
		ctx, kbID, fh, metadata, enableMultimodel, customFileName, tagIDs, channel, processOverrides,
	)
}

func (k *failingFeishuKnowledgeService) ReplaceKnowledgeFromDataSource(
	ctx context.Context, existingID string, item *types.FetchedItem,
	metadata map[string]string, tagIDs []string, channel string,
) (*types.Knowledge, error) {
	k.attempted = append(k.attempted, item.ExternalID)
	if err := k.failByExtID[item.ExternalID]; err != nil {
		return nil, err
	}
	return k.feishuStatefulKnowledgeService.ReplaceKnowledgeFromDataSource(
		ctx, existingID, item, metadata, tagIDs, channel,
	)
}

func setDataSourceResourceIDs(t *testing.T, ds *types.DataSource, resourceIDs []string) {
	t.Helper()
	cfg, err := ds.ParseConfig()
	require.NoError(t, err)
	cfg.ResourceIDs = resourceIDs
	blob, err := cfg.ToJSON()
	require.NoError(t, err)
	ds.Config = blob
}

func decodeFeishuDriveCursor(t *testing.T, ds *types.DataSource) core.FeishuDriveCursor {
	t.Helper()
	cur, err := ds.ParseSyncCursor()
	require.NoError(t, err)
	require.NotNil(t, cur)
	raw, err := json.Marshal(cur.ConnectorCursor)
	require.NoError(t, err)
	var out core.FeishuDriveCursor
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func decodeFeishuWikiCursor(t *testing.T, ds *types.DataSource) core.FeishuCursor {
	t.Helper()
	cur, err := ds.ParseSyncCursor()
	require.NoError(t, err)
	require.NotNil(t, cur)
	raw, err := json.Marshal(cur.ConnectorCursor)
	require.NoError(t, err)
	var out core.FeishuCursor
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// Reproduces Tencent/WeKnora#5724 on Feishu Drive: a single PNG failing ingest
// in the first selected folder must not block unrelated files in that folder
// or files in folders added later. The failed file stays out of the cursor so
// it retries once the underlying issue clears.
func TestProcessSync_FeishuDriveSingleItemIngestFailureDoesNotBlockScopeExpansion(t *testing.T) {
	const (
		rootTok = "fld-root"
		dir1Tok = "fld-dir1"
		dir2Tok = "fld-dir2"
		badPNG  = "file-bad-png"
		okDoc1  = "doc-dir1-ok"
		okDoc2  = "doc-dir2-1"
		okDoc3  = "doc-dir2-2"
	)

	srv := newFeishuSelectedNodeServer(t, "unused-space")
	srv.driveFiles[rootTok] = []core.DriveFile{
		{Token: dir1Tok, Name: "目录一", Type: "folder", ParentToken: rootTok},
		{Token: dir2Tok, Name: "目录二", Type: "folder", ParentToken: rootTok},
	}
	srv.driveFiles[dir1Tok] = []core.DriveFile{
		{Token: badPNG, Name: "源码管理流程.png", Type: "file", ParentToken: dir1Tok, ModifiedTime: "100"},
	}
	srv.driveFiles[dir2Tok] = []core.DriveFile{
		{Token: okDoc2, Name: "新增文档A", Type: "docx", ParentToken: dir2Tok, ModifiedTime: "100"},
		{Token: okDoc3, Name: "新增文档B", Type: "docx", ParentToken: dir2Tok, ModifiedTime: "100"},
	}
	srv.docxText[okDoc1] = "dir1 ok content"
	srv.docxText[okDoc2] = "dir2 ok A"
	srv.docxText[okDoc3] = "dir2 ok B"

	svc, ds, logRepo, baseKS := newFeishuSyncServiceHarness(
		t, srv, types.ConnectorTypeFeishuDrive,
		[]string{rootTok + ":" + dir1Tok}, true,
	)
	ks := &failingFeishuKnowledgeService{
		feishuStatefulKnowledgeService: baseKS,
		failByExtID: map[string]error{
			badPNG: errors.New("private storage error for png"),
		},
	}
	svc.knowledgeService = ks

	// Round 1: only dir1 selected and it currently has only the failing PNG.
	// All fetched items failed -> round fails without leaking private diagnostics
	// and without a misleading "Fetch failed:" prefix.
	log1, err := runFeishuSyncRound(t, svc, ds, logRepo, "drive-5724-1")
	require.Error(t, err)
	assert.Equal(t, types.SyncLogStatusFailed, log1.Status)
	assert.Equal(t, 1, log1.ItemsTotal)
	assert.Equal(t, 1, log1.ItemsFailed)
	assert.Equal(t, 0, log1.ItemsCreated)
	assert.NotContains(t, log1.ErrorMessage, "private storage error")
	assert.NotContains(t, log1.ErrorMessage, "Fetch failed:")

	res1, parseErr := log1.ParseResult()
	require.NoError(t, parseErr)
	require.Len(t, res1.Errors, 1)
	assert.Equal(t, "ingest_failed", res1.Errors[0].Code)
	assert.Equal(t, "云盘目录/目录一/源码管理流程.png", res1.Errors[0].Title)

	// Round 2: user expands scope to include dir2 (and dir1 also has a healthy doc).
	// The still-failing PNG must NOT abort the stream; healthy items in dir1 and dir2
	// must all sync and advance their cursors, moving status to partial/active.
	srv.mu.Lock()
	srv.driveFiles[dir1Tok] = append(srv.driveFiles[dir1Tok], core.DriveFile{
		Token: okDoc1, Name: "目录一正常文档", Type: "docx", ParentToken: dir1Tok, ModifiedTime: "100",
	})
	srv.mu.Unlock()
	setDataSourceResourceIDs(t, ds, []string{rootTok + ":" + dir1Tok, rootTok + ":" + dir2Tok})

	log2, err := runFeishuSyncRound(t, svc, ds, logRepo, "drive-5724-2")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusPartial, log2.Status)
	assert.Equal(t, types.DataSourceStatusActive, ds.Status)
	assert.Equal(t, 4, log2.ItemsTotal)
	assert.Equal(t, 3, log2.ItemsCreated)
	assert.Equal(t, 1, log2.ItemsFailed)
	assert.NotNil(t, ks.repo.live[ds.ID+"|"+okDoc1])
	assert.NotNil(t, ks.repo.live[ds.ID+"|"+okDoc2])
	assert.NotNil(t, ks.repo.live[ds.ID+"|"+okDoc3])
	assert.Nil(t, ks.repo.live[ds.ID+"|"+badPNG])

	cur2 := decodeFeishuDriveCursor(t, ds)
	assert.NotEmpty(t, cur2.FileTimes[rootTok+":"+dir1Tok][okDoc1])
	assert.Empty(t, cur2.FileTimes[rootTok+":"+dir1Tok][badPNG], "failed PNG must remain unadvanced for retry")
	assert.NotEmpty(t, cur2.FileTimes[rootTok+":"+dir2Tok][okDoc2])
	assert.NotEmpty(t, cur2.FileTimes[rootTok+":"+dir2Tok][okDoc3])

	// Round 3: PNG recovers -> only the failed PNG is retried; healthy docs are skipped via cursor.
	delete(ks.failByExtID, badPNG)
	ks.attempted = nil
	log3, err := runFeishuSyncRound(t, svc, ds, logRepo, "drive-5724-3")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log3.Status)
	assert.Equal(t, 1, log3.ItemsTotal)
	assert.Equal(t, 1, log3.ItemsCreated)
	assert.Equal(t, 0, log3.ItemsFailed)
	assert.Equal(t, []string{badPNG}, ks.attempted)

	// Round 4: steady state -> everything is in the cursor, 0 items fetched.
	ks.attempted = nil
	log4, err := runFeishuSyncRound(t, svc, ds, logRepo, "drive-5724-4")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log4.Status)
	assert.Equal(t, 0, log4.ItemsTotal)
	assert.Empty(t, ks.attempted)
}

// Verifies Feishu Wiki isolates single-item ingest failures across sibling
// nodes and newly added resources, and retains prior cursor entries for updated
// nodes so a later deletion is still reconciled.
func TestProcessSync_FeishuWikiIsolatesItemFailureAndPreservesDeletionIdentity(t *testing.T) {
	const (
		spaceID = "space-5724"
		tokBad  = "node-bad"
		tokGood = "node-good"
		tokNew  = "node-new"
	)

	srv := newFeishuSelectedNodeServer(t, spaceID)
	srv.nodes[tokBad] = core.WikiNode{
		SpaceID: spaceID, NodeToken: tokBad, ObjToken: "obj-bad",
		ObjType: "file", Title: "源码管理流程.png", ObjEditTime: "100",
	}
	srv.nodes[tokGood] = core.WikiNode{
		SpaceID: spaceID, NodeToken: tokGood, ObjToken: "obj-good",
		ObjType: "docx", Title: "正常页面", ObjEditTime: "100",
	}
	srv.nodes[tokNew] = core.WikiNode{
		SpaceID: spaceID, NodeToken: tokNew, ObjToken: "obj-new",
		ObjType: "docx", Title: "新增目录文档", ObjEditTime: "100",
	}
	srv.children[""] = []core.WikiNode{srv.nodes[tokBad], srv.nodes[tokGood]}
	srv.docxText["obj-good"] = "good body"
	srv.docxText["obj-new"] = "new body"

	svc, ds, logRepo, baseKS := newFeishuSyncServiceHarness(
		t, srv, types.ConnectorTypeFeishu, []string{spaceID}, true,
	)
	ks := &failingFeishuKnowledgeService{
		feishuStatefulKnowledgeService: baseKS,
		failByExtID:                    map[string]error{},
	}
	svc.knowledgeService = ks

	// Round 1: both initial nodes succeed.
	log1, err := runFeishuSyncRound(t, svc, ds, logRepo, "wiki-5724-1")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log1.Status)
	assert.Equal(t, 2, log1.ItemsCreated)

	cur1 := decodeFeishuWikiCursor(t, ds)
	priorBadEdit := cur1.SpaceNodeTimes[spaceID][tokBad]
	require.NotEmpty(t, priorBadEdit)

	// Round 2: tokBad is edited in Feishu and its replacement fails ingest,
	// while a second resource (tokNew) is added to scope. tokNew must still sync
	// and tokBad must keep its prior cursor edit time (not the new "200").
	srv.mu.Lock()
	updatedBad := srv.nodes[tokBad]
	updatedBad.ObjEditTime = "200"
	srv.nodes[tokBad] = updatedBad
	srv.children[""] = []core.WikiNode{updatedBad, srv.nodes[tokGood]}
	srv.mu.Unlock()
	setDataSourceResourceIDs(t, ds, []string{spaceID, spaceID + ":" + tokNew})
	ks.failByExtID[tokBad] = errors.New("temporary replace failure")

	log2, err := runFeishuSyncRound(t, svc, ds, logRepo, "wiki-5724-2")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusPartial, log2.Status)
	assert.Equal(t, 1, log2.ItemsCreated)
	assert.Equal(t, 1, log2.ItemsFailed)
	assert.NotNil(t, ks.repo.live[ds.ID+"|"+tokNew])

	cur2 := decodeFeishuWikiCursor(t, ds)
	assert.Equal(t, priorBadEdit, cur2.SpaceNodeTimes[spaceID][tokBad],
		"failed update must retain prior cursor entry so deletion tracking stays intact")

	// Round 3: tokBad is removed from the wiki -> because its prior cursor
	// identity was preserved, deletion reconciliation still cleans up the KB row.
	srv.mu.Lock()
	srv.children[""] = []core.WikiNode{srv.nodes[tokGood]}
	srv.mu.Unlock()
	delete(ks.failByExtID, tokBad)

	log3, err := runFeishuSyncRound(t, svc, ds, logRepo, "wiki-5724-3")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log3.Status)
	assert.Equal(t, 1, log3.ItemsDeleted)
	assert.Nil(t, ks.repo.live[ds.ID+"|"+tokBad])
}

// Verifies global workspace errors (storage quota) abort immediately without
// continuing through the rest of the tree, and systemic consecutive failures
// trip the circuit breaker instead of exhausting remote API quotas.
func TestProcessSync_FeishuGlobalErrorAndConsecutiveFailureCircuitBreaker(t *testing.T) {
	t.Run("storage quota aborts immediately on first item", func(t *testing.T) {
		const rootTok = "fld-quota"
		srv := newFeishuSelectedNodeServer(t, "unused")
		srv.driveFiles[rootTok] = []core.DriveFile{
			{Token: "f1", Name: "doc1.md", Type: "file", ParentToken: rootTok, ModifiedTime: "100"},
			{Token: "f2", Name: "doc2.md", Type: "file", ParentToken: rootTok, ModifiedTime: "100"},
		}
		svc, ds, logRepo, baseKS := newFeishuSyncServiceHarness(
			t, srv, types.ConnectorTypeFeishuDrive, []string{rootTok}, true,
		)
		ks := &failingFeishuKnowledgeService{
			feishuStatefulKnowledgeService: baseKS,
			failByExtID: map[string]error{
				"f1": types.NewStorageQuotaExceededError(),
			},
		}
		svc.knowledgeService = ks

		log, err := runFeishuSyncRound(t, svc, ds, logRepo, "quota-abort")
		require.Error(t, err)
		assert.Equal(t, types.SyncLogStatusFailed, log.Status)
		assert.Equal(t, types.DataSourceStatusError, ds.Status)
		assert.Equal(t, []string{"f1"}, ks.attempted, "quota error must abort before trying f2")
		assert.Equal(t, "Ingest failed; see server logs", log.ErrorMessage)

		res, parseErr := log.ParseResult()
		require.NoError(t, parseErr)
		require.Len(t, res.Errors, 1)
		assert.Equal(t, "storage_quota_exceeded", res.Errors[0].Code)
	})

	t.Run("circuit breaker aborts systemic outage at cap", func(t *testing.T) {
		const rootTok = "fld-outage"
		srv := newFeishuSelectedNodeServer(t, "unused")
		failMap := make(map[string]error, maxConsecutiveIsolatedIngestFailures+5)
		files := make([]core.DriveFile, 0, maxConsecutiveIsolatedIngestFailures+5)
		for i := 0; i < maxConsecutiveIsolatedIngestFailures+5; i++ {
			tok := fmt.Sprintf("f-%02d", i)
			files = append(files, core.DriveFile{
				Token: tok, Name: fmt.Sprintf("doc-%02d.md", i),
				Type: "file", ParentToken: rootTok, ModifiedTime: "100",
			})
			failMap[tok] = errors.New("minio unreachable")
		}
		srv.driveFiles[rootTok] = files

		svc, ds, logRepo, baseKS := newFeishuSyncServiceHarness(
			t, srv, types.ConnectorTypeFeishuDrive, []string{rootTok}, true,
		)
		ks := &failingFeishuKnowledgeService{
			feishuStatefulKnowledgeService: baseKS,
			failByExtID:                    failMap,
		}
		svc.knowledgeService = ks

		log, err := runFeishuSyncRound(t, svc, ds, logRepo, "breaker-abort")
		require.Error(t, err)
		assert.Equal(t, types.SyncLogStatusFailed, log.Status)
		assert.Equal(t, types.DataSourceStatusError, ds.Status)
		assert.Len(t, ks.attempted, maxConsecutiveIsolatedIngestFailures)
		assert.Equal(t, "Ingest failed; see server logs", log.ErrorMessage)
		assert.NotContains(t, log.ErrorMessage, "minio unreachable")
	})

	t.Run("successful write resets consecutive failure counter", func(t *testing.T) {
		const rootTok = "fld-reset"
		srv := newFeishuSelectedNodeServer(t, "unused")
		totalFiles := maxConsecutiveIsolatedIngestFailures + 4
		failMap := map[string]error{}
		files := make([]core.DriveFile, 0, totalFiles)
		for i := 0; i < totalFiles; i++ {
			tok := fmt.Sprintf("f-%02d", i)
			files = append(files, core.DriveFile{
				Token: tok, Name: fmt.Sprintf("doc-%02d.md", i),
				Type: "file", ParentToken: rootTok, ModifiedTime: "100",
			})
			// Healthy items break up the failure streaks so neither reaches the cap.
			if i != 5 && i != totalFiles-1 {
				failMap[tok] = errors.New("item-specific failure")
			}
		}
		srv.driveFiles[rootTok] = files

		svc, ds, logRepo, baseKS := newFeishuSyncServiceHarness(
			t, srv, types.ConnectorTypeFeishuDrive, []string{rootTok}, true,
		)
		ks := &failingFeishuKnowledgeService{
			feishuStatefulKnowledgeService: baseKS,
			failByExtID:                    failMap,
		}
		svc.knowledgeService = ks

		log, err := runFeishuSyncRound(t, svc, ds, logRepo, "breaker-reset")
		require.NoError(t, err)
		assert.Equal(t, types.SyncLogStatusPartial, log.Status)
		assert.Equal(t, types.DataSourceStatusActive, ds.Status)
		assert.Equal(t, 2, log.ItemsCreated)
		assert.Equal(t, totalFiles-2, log.ItemsFailed)
		assert.Len(t, ks.attempted, totalFiles)
	})
}

type urlFailingIngestSink struct {
	*ingestAckKnowledgeService
	urlErr error
}

func (s *urlFailingIngestSink) CreateKnowledgeFromURL(
	_ context.Context, _, _, _, _ string, _ *bool, _ string, _ []string, _ string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	if s.urlErr != nil {
		return nil, s.urlErr
	}
	return &types.Knowledge{ID: "url-created"}, nil
}

// Verifies permanent per-item validation rejections (illegal filename/folder,
// invalid URL, malformed JSON replacement, unsupported file URL) stay visible
// with specific codes and advance the cursor without blocking other items.
func TestProcessSync_PermanentValidationRejectionsAdvanceCursor(t *testing.T) {
	cases := []struct {
		name     string
		item     types.FetchedItem
		failure  error
		wantCode string
	}{
		{
			name: "invalid filename validation",
			item: types.FetchedItem{
				ExternalID: "blocked", Title: "bad<script>.png",
				FileName: "dir/bad<script>.png", Content: []byte("png"),
			},
			failure:  werrors.NewValidationError("文件名包含非法字符"),
			wantCode: "invalid_item_name",
		},
		{
			name:     "invalid URL",
			item:     types.FetchedItem{ExternalID: "blocked", Title: "Bad Link", URL: "http://127.0.0.1/private"},
			failure:  ErrInvalidURL,
			wantCode: "invalid_url",
		},
		{
			name: "invalid JSON content on replace",
			item: types.FetchedItem{
				ExternalID: "blocked", Title: "config.json",
				FileName: "dir/config.json", Content: []byte("{bad"),
			},
			failure: fmt.Errorf(
				"%w: %w", errInvalidJSONContent, werrors.NewBadRequestError("JSON 文件内容无效，请检查格式"),
			),
			wantCode: "invalid_json_content",
		},
		{
			name: "unsupported file URL extension",
			item: types.FetchedItem{
				ExternalID: "blocked", Title: "clip.mp4",
				FileName: "clip.mp4", URL: "https://example.com/clip.mp4",
			},
			wantCode: "unsupported_file_type",
		},
	}

	for _, tc := range cases {
		for _, streaming := range []bool{false, true} {
			for _, onlyRejected := range []bool{false, true} {
				name := fmt.Sprintf("%s/stream=%v/only=%v", tc.name, streaming, onlyRejected)
				t.Run(name, func(t *testing.T) {
					h := newIngestAckHarness(t, streaming)
					h.connector.items[1] = tc.item
					if onlyRejected {
						h.connector.items = h.connector.items[1:2]
					}
					h.sink.failure = tc.failure
					h.svc.knowledgeService = &urlFailingIngestSink{
						ingestAckKnowledgeService: h.sink,
						urlErr:                    tc.failure,
					}

					require.NoError(t, h.svc.ProcessSync(context.Background(), h.task))
					log := h.syncLogRepo.logs[h.syncLogID]
					assert.Equal(t, types.SyncLogStatusPartial, log.Status)
					assert.Equal(t, types.DataSourceStatusActive, h.ds.Status)
					assert.Equal(t, 1, log.ItemsFailed)
					assert.Equal(t, len(h.connector.items)-1, log.ItemsCreated)

					res, err := log.ParseResult()
					require.NoError(t, err)
					require.Len(t, res.Errors, 1)
					assert.Equal(t, tc.wantCode, res.Errors[0].Code)

					cursor, err := h.ds.ParseSyncCursor()
					require.NoError(t, err)
					assert.Equal(t, len(h.connector.items), ingestAckOffset(cursor))
				})
			}
		}
	}
}

type failingSeafileKnowledgeService struct {
	*seafileKnowledgeService
	failPath string
	failErr  error
}

func (k *failingSeafileKnowledgeService) CreateKnowledgeFromFile(
	ctx context.Context, kbID string, fh *multipart.FileHeader, metadata map[string]string,
	enableMultimodel *bool, customFileName string, tagIDs []string, channel string,
	processOverrides *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	if metadata["seafile_path"] == k.failPath && k.failErr != nil {
		return nil, k.failErr
	}
	return k.seafileKnowledgeService.CreateKnowledgeFromFile(
		ctx, kbID, fh, metadata, enableMultimodel, customFileName, tagIDs, channel, processOverrides,
	)
}

// Verifies the sibling Seafile streaming connector also isolates a single
// item's ingest failure without blocking remaining files, and retries only the
// failed file on the next incremental sync.
func TestProcessSync_SeafileIsolatesSingleItemIngestFailure(t *testing.T) {
	h := newSeafileSyncHarness(t, true, nil)
	h.server.files["/docs/a_bad.png"] = []byte("png")
	h.server.files["/docs/b_ok.pdf"] = []byte("ok pdf")

	ks := &failingSeafileKnowledgeService{
		seafileKnowledgeService: h.knowledge,
		failPath:                "/docs/a_bad.png",
		failErr:                 errors.New("storage backend rejected png"),
	}
	h.svc.knowledgeService = ks

	log1, err := h.run(t, false)
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusPartial, log1.Status)
	assert.Equal(t, types.DataSourceStatusActive, h.ds.Status)
	assert.Equal(t, 1, log1.ItemsCreated)
	assert.Equal(t, 1, log1.ItemsFailed)

	files := h.cursorFiles(t)
	assert.NotEmpty(t, files["/docs/b_ok.pdf"])
	_, badTracked := files["/docs/a_bad.png"]
	assert.False(t, badTracked, "failed Seafile item must stay out of cursor for retry")

	// Next round after recovery: only /docs/a_bad.png is retried.
	ks.failErr = nil
	h.knowledge.created = nil
	h.syncLog.Status = types.SyncLogStatusRunning
	log2, err := h.run(t, false)
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log2.Status)
	assert.Equal(t, 1, log2.ItemsTotal)
	assert.Equal(t, 1, log2.ItemsCreated)
	require.Len(t, h.knowledge.created, 1)
	assert.Equal(t, "/docs/a_bad.png", h.knowledge.created[0].metadata["seafile_path"])
}

// End-to-end integration test wiring the real Feishu Drive connector, real
// DataSourceService.ProcessSync, real knowledgeService, and a real SQLite
// KnowledgeRepository: verifies that real permanent rejections (missing VLM
// for PNG, illegal filename characters, malformed JSON replacement) stay
// visible with specific error codes and do not block healthy documents.
func TestProcessSync_RealKnowledgeServiceFeishuDrivePermanentRejections(t *testing.T) {
	h, _, _, _ := datasourceReplacementFixture(t)
	// Wire a real no-VLM KB + tenant 7 matching datasourceReplacementFixture.
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 7}
	h.svc.kbService = &processSyncKBService{kb: kb}
	h.svc.task = &replaceFileEnqueuer{events: &[]string{}}

	const (
		rootTok    = "fld-real"
		pngTok     = "tok-png"
		badNameTok = "tok-bad-name"
		badJSONTok = "doc-1"
		healthyTok = "tok-ok"
	)
	srv := newFeishuSelectedNodeServer(t, "unused")
	srv.driveFiles[rootTok] = []core.DriveFile{
		{Token: pngTok, Name: "源码管理流程.png", Type: "file", ParentToken: rootTok, ModifiedTime: "100"},
		{Token: badNameTok, Name: "doc_onclick=1.md", Type: "file", ParentToken: rootTok, ModifiedTime: "100"},
		{Token: badJSONTok, Name: "config.json", Type: "file", ParentToken: rootTok, ModifiedTime: "200"},
		{Token: healthyTok, Name: "正常文档", Type: "docx", ParentToken: rootTok, ModifiedTime: "100"},
	}
	srv.docxText[healthyTok] = "real knowledge body"

	svc, ds, logRepo, _ := newFeishuSyncServiceHarness(
		t, srv, types.ConnectorTypeFeishuDrive, []string{rootTok}, true,
	)
	ds.ID = "ds-1"
	ds.TenantID = 7
	ds.KnowledgeBaseID = "kb-1"
	svc.dsRepo = newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)
	svc.kbService = &processSyncKBService{kb: kb}
	svc.tenantRepo = &processSyncTenantRepo{tenant: &types.Tenant{ID: 7, StorageQuota: 1024 * 1024}}
	svc.knowledgeService = h.svc

	log1, err := runFeishuSyncRound(t, svc, ds, logRepo, "real-ks-1")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusPartial, log1.Status)
	assert.Equal(t, types.DataSourceStatusActive, ds.Status)
	assert.Equal(t, 4, log1.ItemsTotal)
	assert.Equal(t, 1, log1.ItemsCreated)
	assert.Equal(t, 3, log1.ItemsFailed)

	res1, parseErr := log1.ParseResult()
	require.NoError(t, parseErr)
	require.Len(t, res1.Errors, 3)
	codes := []string{res1.Errors[0].Code, res1.Errors[1].Code, res1.Errors[2].Code}
	assert.Equal(t, []string{
		"prerequisite_not_configured",
		"invalid_item_name",
		"invalid_json_content",
	}, codes)
	assert.Equal(t, "云盘目录/源码管理流程.png", res1.Errors[0].Title)

	// Confirm the healthy docx actually persisted into SQLite and the original
	// JSON knowledge row survived the rejected malformed replacement.
	createdHealthy, lookupErr := h.svc.repo.FindByDataSourceExternalID(
		context.Background(), 7, "kb-1", "ds-1", healthyTok,
	)
	require.NoError(t, lookupErr)
	require.NotNil(t, createdHealthy)
	existingJSON, lookupErr := h.svc.repo.GetKnowledgeByID(context.Background(), 7, h.original.ID)
	require.NoError(t, lookupErr)
	require.NotNil(t, existingJSON)

	// Next incremental sync sees all 4 items in cursor and does zero redundant work.
	log2, err := runFeishuSyncRound(t, svc, ds, logRepo, "real-ks-2")
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log2.Status)
	assert.Equal(t, 0, log2.ItemsTotal)
}

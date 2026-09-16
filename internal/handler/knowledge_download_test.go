package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type downloadKnowledgeStub struct {
	interfaces.KnowledgeService
	items          []*types.Knowledge
	names          map[string]string
	opened         []string
	failID         string
	expectedTenant uint64
}

func (s *downloadKnowledgeStub) GetKnowledgeBatch(context.Context, uint64, []string) ([]*types.Knowledge, error) {
	return s.items, nil
}

func (s *downloadKnowledgeStub) GetKnowledgeFile(ctx context.Context, id string) (io.ReadCloser, string, error) {
	want := s.expectedTenant
	if want == 0 {
		want = 7
	}
	if types.MustTenantIDFromContext(ctx) != want {
		return nil, "", fmt.Errorf("错误的租户上下文")
	}
	s.opened = append(s.opened, id)
	if s.failID == id {
		return nil, "", fmt.Errorf("模拟存储读取失败")
	}
	return io.NopCloser(strings.NewReader("原文-" + id)), s.names[id], nil
}

type downloadKBStub struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *downloadKBStub) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type downloadShareStub struct {
	interfaces.KBShareService
	permission types.OrgMemberRole
}

func (s *downloadShareStub) CheckTenantKBPermission(
	context.Context, string, uint64, types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	return s.permission, true, nil
}

func runBatchDownload(
	t *testing.T,
	svc *downloadKnowledgeStub,
	ids []string,
	kb *types.KnowledgeBase,
	share interfaces.KBShareService,
	scope *types.TenantAPIKeyScope,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := types.WithCaller(c.Request.Context(), types.Caller{
			TenantID: 7, UserID: "user-1", Role: types.TenantRoleContributor,
		})
		ctx = types.WithExecutionTenant(ctx, 7)
		if scope != nil {
			ctx = types.WithTenantAPIKeyScope(ctx, *scope)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Set(types.UserIDContextKey.String(), "user-1")
		c.Next()
	})
	h := &KnowledgeHandler{
		kgService:      svc,
		kbService:      &downloadKBStub{kb: kb},
		kbShareService: share,
	}
	router.POST("/knowledge-bases/:id/knowledge/batch-download", h.BatchDownloadKnowledge)
	body, err := json.Marshal(BatchDownloadKnowledgeRequest{IDs: ids})
	require.NoError(t, err)
	req := httptest.NewRequest(
		http.MethodPost, "/knowledge-bases/kb-1/knowledge/batch-download", bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestBatchDownloadKnowledgeProducesCompleteZIP(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	svc := &downloadKnowledgeStub{
		items: []*types.Knowledge{
			{ID: "a", TenantID: 7, KnowledgeBaseID: "kb-1", FilePath: "stored-a"},
			{ID: "b", TenantID: 7, KnowledgeBaseID: "kb-1", Type: types.KnowledgeTypeManual},
			{ID: "c", TenantID: 7, KnowledgeBaseID: "kb-1", FilePath: "stored-c"},
		},
		names: map[string]string{"a": "设计说明.md", "b": "设计说明.md", "c": `..\设计说明 (2).md`},
	}
	w := runBatchDownload(t, svc, []string{"a", "b", "a", "c"},
		&types.KnowledgeBase{ID: "kb-1", TenantID: 7}, nil, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "application/zip", w.Header().Get("Content-Type"))
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	require.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	reader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.NoError(t, err)
	require.Len(t, reader.File, 3)
	for index, name := range []string{"设计说明.md", "设计说明 (2).md", "设计说明 (2) (2).md"} {
		entry := reader.File[index]
		require.Equal(t, name, entry.Name)
		file, err := entry.Open()
		require.NoError(t, err)
		content, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		require.Equal(t, "原文-"+[]string{"a", "b", "c"}[index], string(content))
	}
	require.Equal(t, []string{"a", "b", "c"}, svc.opened)
	entries, err := os.ReadDir(os.TempDir())
	require.NoError(t, err)
	require.Empty(t, entries, "请求完成后不应留下临时压缩包")
}

func TestBatchDownloadKnowledgeRejectsInvalidSelectionsBeforeReading(t *testing.T) {
	cases := []struct {
		name   string
		ids    []string
		item   *types.Knowledge
		kb     *types.KnowledgeBase
		share  interfaces.KBShareService
		scope  *types.TenantAPIKeyScope
		status int
	}{
		{
			name: "空列表", status: 400,
			kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7},
		},
		{
			name: "空白ID", ids: []string{" "}, status: 400,
			kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7},
		},
		{
			name: "超出数量", ids: make([]string, maxBatchDownloadFiles+1), status: 400,
			kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7},
		},
		{
			name: "文档缺失", ids: []string{"missing"}, status: 404,
			kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7},
		},
		{
			name: "跨知识库", ids: []string{"a"}, status: 404,
			item: &types.Knowledge{ID: "a", TenantID: 7, KnowledgeBaseID: "other", FilePath: "secret"},
			kb:   &types.KnowledgeBase{ID: "kb-1", TenantID: 7},
		},
		{
			name: "跨租户", ids: []string{"a"}, status: 404,
			item: &types.Knowledge{ID: "a", TenantID: 8, KnowledgeBaseID: "kb-1", FilePath: "secret"},
			kb:   &types.KnowledgeBase{ID: "kb-1", TenantID: 7},
		},
		{
			name: "仅无原文件", ids: []string{"a"}, status: 400,
			item: &types.Knowledge{ID: "a", TenantID: 7, KnowledgeBaseID: "kb-1", Type: "url"},
			kb:   &types.KnowledgeBase{ID: "kb-1", TenantID: 7},
		},
		{
			name: "共享只读", ids: []string{"a"}, status: 403,
			kb:    &types.KnowledgeBase{ID: "kb-1", TenantID: 8},
			share: &downloadShareStub{permission: types.OrgRoleViewer},
		},
		{
			name: "密钥无此库权限", ids: []string{"a"}, status: 403,
			kb:    &types.KnowledgeBase{ID: "kb-1", TenantID: 7},
			scope: &types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"other"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &downloadKnowledgeStub{}
			if tc.item != nil {
				svc.items = []*types.Knowledge{tc.item}
			}
			w := runBatchDownload(t, svc, tc.ids, tc.kb, tc.share, tc.scope)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Empty(t, svc.opened)
			require.NotContains(t, w.Header().Get("Content-Type"), "zip")
		})
	}
}

func TestBatchDownloadKnowledgeFailureDoesNotReturnPartialZIP(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	svc := &downloadKnowledgeStub{
		items: []*types.Knowledge{
			{ID: "a", TenantID: 7, KnowledgeBaseID: "kb-1", FilePath: "a"},
			{ID: "b", TenantID: 7, KnowledgeBaseID: "kb-1", FilePath: "b"},
		},
		names: map[string]string{"a": "first.txt"}, failID: "b",
	}
	w := runBatchDownload(t, svc, []string{"a", "b"},
		&types.KnowledgeBase{ID: "kb-1", TenantID: 7}, nil, nil)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "json")
	require.Empty(t, w.Header().Get("Content-Disposition"))
	entries, err := os.ReadDir(os.TempDir())
	require.NoError(t, err)
	require.Empty(t, entries)
}

type downloadTrackingReader struct {
	io.Reader
	closed bool
}

func (r *downloadTrackingReader) Close() error {
	r.closed = true
	return nil
}

func TestBatchDownloadKnowledgeSkipsEntriesWithoutOriginalFiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	svc := &downloadKnowledgeStub{
		items: []*types.Knowledge{
			{ID: "a", TenantID: 7, KnowledgeBaseID: "kb-1", FilePath: "stored-a"},
			{ID: "url", TenantID: 7, KnowledgeBaseID: "kb-1", Type: "url"},
		},
		names: map[string]string{"a": "keep.txt"},
	}
	w := runBatchDownload(t, svc, []string{"a", "url"},
		&types.KnowledgeBase{ID: "kb-1", TenantID: 7}, nil, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	reader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.NoError(t, err)
	require.Len(t, reader.File, 1)
	require.Equal(t, "keep.txt", reader.File[0].Name)
	require.Equal(t, []string{"a"}, svc.opened)
}

func TestBatchDownloadKnowledgeAllowsSharedEditor(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	svc := &downloadKnowledgeStub{
		items:          []*types.Knowledge{{ID: "a", TenantID: 8, KnowledgeBaseID: "kb-1", FilePath: "stored-a"}},
		names:          map[string]string{"a": "shared.txt"},
		expectedTenant: 8,
	}
	w := runBatchDownload(t, svc, []string{"a"},
		&types.KnowledgeBase{ID: "kb-1", TenantID: 8},
		&downloadShareStub{permission: types.OrgRoleEditor}, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, []string{"a"}, svc.opened)
}

func TestBatchDownloadKnowledgeRejectsWhenBusy(t *testing.T) {
	for i := 0; i < maxConcurrentBatchDownloads; i++ {
		batchDownloadSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for i := 0; i < maxConcurrentBatchDownloads; i++ {
			<-batchDownloadSlots
		}
	})
	svc := &downloadKnowledgeStub{
		items: []*types.Knowledge{{ID: "a", TenantID: 7, KnowledgeBaseID: "kb-1", FilePath: "a"}},
		names: map[string]string{"a": "a.txt"},
	}
	w := runBatchDownload(t, svc, []string{"a"},
		&types.KnowledgeBase{ID: "kb-1", TenantID: 7}, nil, nil)
	require.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
	require.Empty(t, svc.opened)
}

func TestKnowledgeDownloadArchiveEnforcesActualByteLimitAndClosesFiles(t *testing.T) {
	for _, limit := range []int64{4, 5} {
		var output bytes.Buffer
		file := &downloadTrackingReader{Reader: strings.NewReader("12345")}
		err := writeKnowledgeDownloadArchive(context.Background(), &output,
			[]knowledgeDownloadEntry{{ID: "a"}},
			func(context.Context, string) (io.ReadCloser, string, error) { return file, "a.txt", nil }, limit)
		if limit == 4 {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
		require.True(t, file.closed)
	}
}

func TestKnowledgeDownloadArchiveStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	err := writeKnowledgeDownloadArchive(ctx, &output, []knowledgeDownloadEntry{{ID: "a"}},
		func(context.Context, string) (io.ReadCloser, string, error) {
			t.Fatal("取消后不应继续读取文件")
			return nil, "", nil
		}, 100)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, output.Len())
}

func TestKnowledgeDownloadArchiveCancelsAfterOpenAndClosesFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	file := &downloadTrackingReader{Reader: strings.NewReader("12345")}
	err := writeKnowledgeDownloadArchive(ctx, io.Discard, []knowledgeDownloadEntry{{ID: "a"}},
		func(context.Context, string) (io.ReadCloser, string, error) {
			cancel()
			return file, "a.txt", nil
		}, 100)
	require.True(t, isKnowledgeDownloadCanceled(err), err)
	require.True(t, file.closed)
	requireAppHTTP(t, mapKnowledgeDownloadError(err), http.StatusBadRequest)
}

func TestMapKnowledgeDownloadError(t *testing.T) {
	requireAppHTTP(t, mapKnowledgeDownloadError(context.Canceled), http.StatusBadRequest)
	requireAppHTTP(t, mapKnowledgeDownloadError(context.DeadlineExceeded), http.StatusBadRequest)
}

func requireAppHTTP(t *testing.T, err error, status int) {
	t.Helper()
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok, err)
	require.Equal(t, status, appErr.HTTPCode)
}

func TestKnowledgeDownloadNamesAreSafeForWindowsAndUTF8(t *testing.T) {
	used := map[string]bool{}
	for _, name := range []string{
		"../报告.txt", `C:\secret\Report.TXT`, "report.txt", "CON.txt", "COM¹.txt", "a:b?.txt", "..",
		strings.Repeat("中", 100) + ".pdf",
	} {
		got := uniqueKnowledgeDownloadName(name, used)
		require.True(t, utf8.ValidString(got))
		require.NotContains(t, got, "/")
		require.NotContains(t, got, "\\")
		require.NotContains(t, got, ":")
		require.NotEqual(t, "CON.txt", got)
		require.NotEqual(t, "COM¹.txt", got)
		require.Less(t, len(got), 255)
	}
	require.True(t, used["report (2).txt"])
}

func TestKnowledgeDownloadZipPathKeepsSafeFolders(t *testing.T) {
	used := map[string]bool{}
	require.Equal(t, "docs/spec/a.md", uniqueKnowledgeDownloadZipPath(`..\docs/spec`, "a.md", used))
	require.Equal(t, "docs/spec/a (2).md", uniqueKnowledgeDownloadZipPath("docs/spec", "../a.md", used))
	got := uniqueKnowledgeDownloadZipPath("/abs/../secret", "CON.txt", map[string]bool{})
	require.Equal(t, "abs/secret/_CON.txt", got)
	require.NotContains(t, got, "..")
	require.False(t, strings.HasPrefix(got, "/"))
}

func TestBatchDownloadKnowledgePreservesFolderPaths(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	svc := &downloadKnowledgeStub{
		items: []*types.Knowledge{{
			ID: "a", TenantID: 7, KnowledgeBaseID: "kb-1", FilePath: "stored-a", FolderPath: "docs/spec",
		}},
		names: map[string]string{"a": "design.md"},
	}
	w := runBatchDownload(t, svc, []string{"a"},
		&types.KnowledgeBase{ID: "kb-1", TenantID: 7}, nil, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	reader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.NoError(t, err)
	require.Len(t, reader.File, 1)
	require.Equal(t, "docs/spec/design.md", reader.File[0].Name)
}

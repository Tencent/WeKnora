package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wikiRecoveryKBStub struct {
	interfaces.KnowledgeBaseService
}

func (wikiRecoveryKBStub) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{
		ID: "kb", TenantID: 7, Type: types.KnowledgeBaseTypeWiki,
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
	}, nil
}

type wikiRecoveryRepoStub struct {
	calls    int
	tenantID uint64
	err      error
	count    int
}

func (r *wikiRecoveryRepoStub) ListDocuments(
	_ context.Context, tenantID uint64, _ string, _ string, _, _ int,
) (*types.WikiDocumentList, error) {
	r.calls++
	r.tenantID = tenantID
	return &types.WikiDocumentList{Documents: []*types.WikiDocument{}}, r.err
}

func (r *wikiRecoveryRepoStub) RetryDocuments(
	_ context.Context, tenantID uint64, _ string, _ []string,
) (int, error) {
	r.calls++
	r.tenantID = tenantID
	return r.count, r.err
}

type wikiRecoveryQueueStub struct{ err error }

func (q wikiRecoveryQueueStub) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{}, q.err
}

func wikiRecoveryEngine(repo repository.WikiRecoveryRepository, queue interfaces.TaskEnqueuer) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &WikiPageHandler{
		kbService:       wikiRecoveryKBStub{},
		recoveryService: service.NewWikiRecoveryService(repo, queue),
	}
	r.GET("/knowledgebase/:kb_id/wiki/documents", h.ListDocuments)
	r.POST("/knowledgebase/:kb_id/wiki/retry", h.RetryDocuments)
	return r
}

func TestWikiRecoveryHTTPRejectsInvalidRequests(t *testing.T) {
	tooMany, err := json.Marshal(WikiRetryRequest{KnowledgeIDs: make([]string, 101)})
	require.NoError(t, err)
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, "/documents?status=invalid", ""},
		{http.MethodGet, "/documents?page=0", ""},
		{http.MethodGet, "/documents?page_size=101", ""},
		{http.MethodPost, "/retry", `{}`},
		{http.MethodPost, "/retry", `{"knowledge_ids":[" "]}`},
		{http.MethodPost, "/retry", `{"knowledge_ids":`},
		{http.MethodPost, "/retry", string(tooMany)},
	} {
		t.Run(test.method+test.path+test.body, func(t *testing.T) {
			repo := &wikiRecoveryRepoStub{}
			r := wikiRecoveryEngine(repo, wikiRecoveryQueueStub{})
			request := httptest.NewRequest(test.method, "/knowledgebase/kb/wiki"+test.path,
				strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			r.ServeHTTP(response, request)
			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Zero(t, repo.calls)
		})
	}
}

func TestWikiRecoveryHTTPUsesSourceTenant(t *testing.T) {
	repo := &wikiRecoveryRepoStub{count: 1}
	r := wikiRecoveryEngine(repo, wikiRecoveryQueueStub{})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		path, body := "/documents", ""
		if method == http.MethodPost {
			path, body = "/retry", `{"knowledge_ids":["doc"]}`
		}
		request := httptest.NewRequest(method, "/knowledgebase/kb/wiki"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, uint64(7), repo.tenantID)
	}
}

func TestWikiRecoveryHTTPReportsFailureKinds(t *testing.T) {
	for _, test := range []struct {
		name, message string
		repoErr       error
		queueErr      error
		status        int
	}{
		{"source unavailable", "knowledge", repository.ErrKnowledgeNotFound, nil, http.StatusNotFound},
		{"source incomplete", "ready", repository.ErrWikiRecoveryNotReady, nil, http.StatusConflict},
		{
			"database failure", "database unavailable", errors.New("database unavailable"), nil,
			http.StatusInternalServerError,
		},
		{
			"scheduling failure", "operations are saved", nil, errors.New("queue unavailable"),
			http.StatusInternalServerError,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &wikiRecoveryRepoStub{count: 1, err: test.repoErr}
			r := wikiRecoveryEngine(repo, wikiRecoveryQueueStub{err: test.queueErr})
			request := httptest.NewRequest(http.MethodPost, "/knowledgebase/kb/wiki/retry",
				strings.NewReader(`{"knowledge_ids":["doc"]}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			r.ServeHTTP(response, request)
			assert.Equal(t, test.status, response.Code)
			assert.Contains(t, response.Body.String(), test.message)
		})
	}
}

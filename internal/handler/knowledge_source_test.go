package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type sourceContentHandlerStub struct {
	documentWriteHandlerKnowledge
	content, revision string
	err               error
}

func (s *sourceContentHandlerStub) GetKnowledgeSourceContent(
	ctx context.Context, _ string,
) (*types.KnowledgeSourceContent, error) {
	if err := access.RequireKBWrite(ctx, &types.KnowledgeBase{ID: "kb", TenantID: 7}); err != nil {
		return nil, err
	}
	return &types.KnowledgeSourceContent{Content: "original", Revision: "r1", FileName: "a.md"}, s.err
}

func (s *sourceContentHandlerStub) UpdateKnowledgeSourceContent(
	ctx context.Context, id, content, revision string,
) (*types.Knowledge, error) {
	if err := access.RequireKBWrite(ctx, &types.KnowledgeBase{ID: "kb", TenantID: 7}); err != nil {
		return nil, err
	}
	s.content, s.revision = content, revision
	return &types.Knowledge{ID: id, ParseStatus: types.ParseStatusPending}, s.err
}

func TestKnowledgeSourceHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		status             int
		err                error
	}{
		{name: "read", method: http.MethodGet, status: http.StatusOK},
		{name: "save", method: http.MethodPut, body: `{"content":"新原文","revision":"r1"}`, status: http.StatusOK},
		{name: "missing-revision", method: http.MethodPut, body: `{"content":"new"}`, status: http.StatusBadRequest},
		{name: "invalid-json", method: http.MethodPut, body: `{`, status: http.StatusBadRequest},
		{
			name: "conflict", method: http.MethodPut, body: `{"content":"new","revision":"r1"}`,
			status: http.StatusConflict, err: apperrors.NewConflictError("Source changed"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &sourceContentHandlerStub{err: tc.err}
			h := &KnowledgeHandler{kgService: svc}
			r := documentHandlerRouter()
			r.GET("/:id/content", h.GetKnowledgeSourceContent)
			r.PUT("/:id/content", h.UpdateKnowledgeSourceContent)
			req := httptest.NewRequest(tc.method, "/doc/content", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.name == "read" {
				require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
				require.Contains(t, w.Body.String(), `"revision":"r1"`)
			}
			if tc.name == "save" {
				require.Equal(t, "新原文", svc.content)
				require.Equal(t, "r1", svc.revision)
			}
		})
	}
}

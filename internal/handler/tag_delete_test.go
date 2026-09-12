package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type tagDeleteServiceSpy struct {
	interfaces.KnowledgeTagService
	calls    int
	excluded []string
}

func (s *tagDeleteServiceSpy) DeleteTag(_ context.Context, _ string, _, _ bool, excluded []string) error {
	s.calls++
	s.excluded = excluded
	return nil
}

type tagDeleteChunks struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
	err    error
}

func (s *tagDeleteChunks) ListChunksBySeqID(context.Context, uint64, []int64) ([]*types.Chunk, error) {
	return s.chunks, s.err
}

func TestTagDeletionNeverIgnoresInvalidExclusions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name, body string
		chunks     []*types.Chunk
		err        error
		status     int
	}{
		{name: "invalid JSON", body: `{`, status: 400},
		{name: "missing exclusion", body: `{"exclude_ids":[1]}`, status: 404},
		{name: "lookup failure", body: `{"exclude_ids":[1]}`, err: errors.New("database unavailable"), status: 500},
		{
			name: "foreign exclusion",
			body: `{"exclude_ids":[1]}`,
			chunks: []*types.Chunk{{
				ID:              "one",
				SeqID:           1,
				TenantID:        7,
				KnowledgeBaseID: "other",
				ChunkType:       types.ChunkTypeFAQ,
			}},
			status: 403,
		},

		{
			name: "valid exclusion",
			body: `{"exclude_ids":[1]}`,
			chunks: []*types.Chunk{{
				ID:              "one",
				SeqID:           1,
				TenantID:        7,
				KnowledgeBaseID: "kb",
				ChunkType:       types.ChunkTypeFAQ,
			}},
			status: 200,
		},

		{name: "optional empty body", status: 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &tagDeleteServiceSpy{}
			h := &TagHandler{tagService: svc, chunkRepo: &tagDeleteChunks{chunks: tt.chunks, err: tt.err}}
			router := gin.New()
			router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				c.Request = c.Request.WithContext(
					context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7)),
				)
				c.Next()
			})
			router.DELETE("/knowledge-bases/:id/tags/:tag_id", h.DeleteTag)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(
				http.MethodDelete,
				"/knowledge-bases/kb/tags/tag?force=true",
				strings.NewReader(tt.body),
			)
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			require.Equal(t, tt.status, w.Code, w.Body.String())
			if tt.status == 200 {
				require.Equal(t, 1, svc.calls)
				if tt.body != "" {
					require.Equal(t, []string{"one"}, svc.excluded)
				}
			} else {
				require.Zero(t, svc.calls)
			}
		})
	}
}

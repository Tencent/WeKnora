package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type artifactLibraryMessageServiceStub struct {
	interfaces.MessageService
	result *types.PageResult
}

func (s *artifactLibraryMessageServiceStub) ListArtifactLibrary(
	_ context.Context, _ *types.ArtifactLibraryQuery,
) (*types.PageResult, error) {
	return s.result, nil
}

func TestListArtifactLibraryResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		result *types.PageResult
		want   string
	}{
		{
			name:   "nil data and zero pagination remain present",
			result: &types.PageResult{},
			want:   `{"success":true,"data":null,"total":0,"page":0,"page_size":0}`,
		},
		{
			name: "typed nil slice remains null",
			result: &types.PageResult{
				Data: ([]*types.ArtifactLibraryItem)(nil), Page: 1, PageSize: 20,
			},
			want: `{"success":true,"data":null,"total":0,"page":1,"page_size":20}`,
		},
		{
			name: "empty slice remains an array",
			result: &types.PageResult{
				Data: []*types.ArtifactLibraryItem{}, Page: 1, PageSize: 20,
			},
			want: `{"success":true,"data":[],"total":0,"page":1,"page_size":20}`,
		},
		{
			name: "items preserve names values and omitted fields",
			result: &types.PageResult{
				Total: 7, Page: 2, PageSize: 3,
				Data: []*types.ArtifactLibraryItem{
					{
						SessionID: "session-1", SessionTitle: "Report", MessageID: "message-1", Index: 2,
						URL: "local://private/report.pdf", Handle: "resource://artifact-1",
						FileName: "report.pdf", FileType: ".pdf", FileSize: 123,
						SourcePath: "/workspace/report.pdf", VersionCount: 4,
						CreatedAt: time.Date(2026, time.October, 28, 10, 0, 0, 0, time.UTC),
					},
					{URL: "local://private/hidden.pdf"},
					nil,
				},
			},
			want: `{
				"success":true,"total":7,"page":2,"page_size":3,
				"data":[
					{
						"session_id":"session-1","session_title":"Report","message_id":"message-1","index":2,
						"handle":"resource://artifact-1","file_name":"report.pdf","file_type":".pdf","file_size":123,
						"source_path":"/workspace/report.pdf","created_at":"2026-10-28T10:00:00Z","version_count":4
					},
					{
						"session_id":"","session_title":"","message_id":"","index":0,
						"file_name":"","file_type":"","file_size":0,"source_path":"",
						"created_at":"0001-01-01T00:00:00Z","version_count":0
					},
					null
				]
			}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{messageService: &artifactLibraryMessageServiceStub{result: tt.result}}
			router := gin.New()
			router.GET("/artifacts", h.ListArtifactLibrary)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/artifacts", nil))

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))
			require.JSONEq(t, tt.want, recorder.Body.String())
		})
	}
}

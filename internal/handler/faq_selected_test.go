package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type selectedFAQService struct {
	interfaces.KnowledgeService
	request *types.FAQEntryFieldsBatchUpdate
	kb      string
}

func (s *selectedFAQService) UpdateFAQEntryFieldsBatch(
	_ context.Context, kb string, req *types.FAQEntryFieldsBatchUpdate,
) error {
	s.request, s.kb = req, kb
	return nil
}

func TestSelectedFAQFieldsKeepExplicitScopeAndFalse(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"entry_ids":[1,2],"is_enabled":false,"tag_id":0,"by_tag":{"9":{"is_enabled":true}}}`, true},
		{`{"entry_ids":[1],"is_recommended":false}`, true},
		{`{"entry_ids":[]}`, false},
		{`{"entry_ids":[1]}`, false},
		{`{"entry_ids":[0],"is_enabled":true}`, false},
		{`{"entry_ids":[1],"tag_id":-1}`, false},
	} {
		svc := &selectedFAQService{}
		h := &FAQHandler{knowledgeService: svc}
		r := gin.New()
		r.PUT("/:id", h.UpdateSelectedFields)
		req := httptest.NewRequest(http.MethodPut, "/kb1", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(httptest.NewRecorder(), req)
		if !tc.valid {
			require.Nil(t, svc.request, tc.body)
			continue
		}
		require.NotNil(t, svc.request)
		require.Equal(t, "kb1", svc.kb)
		require.Empty(t, svc.request.ByTag)
		for _, patch := range svc.request.ByID {
			if patch.IsEnabled != nil {
				require.False(t, *patch.IsEnabled)
			}
			if patch.TagID != nil {
				require.Zero(t, *patch.TagID)
			}
		}
	}
}

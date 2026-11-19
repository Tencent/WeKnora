package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type faqBatchService struct {
	interfaces.KnowledgeService
	kbID string
	plan *types.FAQEntryFieldsBatchUpdate
	err  error
}

func (s *faqBatchService) UpdateFAQEntryFieldsBatch(
	_ context.Context,
	kb string,
	p *types.FAQEntryFieldsBatchUpdate,
) error {
	s.kbID = kb
	s.plan = p
	return s.err
}

func runFAQBatchPatch(body string, s *faqBatchService) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPatch, "/knowledge-bases/kb1/faq/entries", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "kb1"}}
	(&FAQHandler{knowledgeService: s}).PatchEntriesBatch(c)
	return c, w
}

func TestFAQBatchPatchPreservesOmittedFieldsAndExplicitFalse(t *testing.T) {
	for _, body := range []string{
		`{"entry_ids":[1,2],"is_enabled":false}`,
		`{"entry_ids":[1,2],"is_recommended":false,"tag_id":0}`,
	} {
		s := &faqBatchService{}
		c, w := runFAQBatchPatch(body, s)
		require.Empty(t, c.Errors)
		assert.Equal(t, 200, w.Code)
		assert.Equal(t, "kb1", s.kbID)
		require.Len(t, s.plan.ByID, 2)
		assert.Empty(t, s.plan.ByTag)
		for _, p := range s.plan.ByID {
			if strings.Contains(body, "is_enabled") {
				require.NotNil(t, p.IsEnabled)
				assert.False(t, *p.IsEnabled)
				assert.Nil(t, p.IsRecommended)
				assert.Nil(t, p.TagID)
			} else {
				assert.Nil(t, p.IsEnabled)
				require.NotNil(t, p.IsRecommended)
				assert.False(t, *p.IsRecommended)
				require.NotNil(t, p.TagID)
				assert.Zero(t, *p.TagID)
			}
		}
	}
}

func TestFAQBatchPatchRejectsInvalidSelectionWithoutDispatch(t *testing.T) {
	ids := make([]string, 21)
	for i := range ids {
		ids[i] = fmt.Sprint(i + 1)
	}
	for _, body := range []string{
		`{"entry_ids":[],"is_enabled":false}`, `{"entry_ids":[1]}`, `{"entry_ids":[1],"is_enabled":null}`,
		`{"entry_ids":[0],"is_enabled":false}`, `{"entry_ids":[1,1],"is_enabled":false}`,
		`{"entry_ids":[1],"tag_id":-1}`, `{"entry_ids":[` + strings.Join(ids, ",") + `],"is_enabled":false}`,
	} {
		s := &faqBatchService{}
		c, _ := runFAQBatchPatch(body, s)
		require.NotEmpty(t, c.Errors, body)
		assert.Nil(t, s.plan, body)
	}
}

func TestFAQBatchPatchPropagatesAccessFailure(t *testing.T) {
	s := &faqBatchService{err: errors.NewForbiddenError("entry belongs to another knowledge base")}
	c, w := runFAQBatchPatch(`{"entry_ids":[1],"is_enabled":false}`, s)
	require.NotEmpty(t, c.Errors)
	assert.NotContains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, c.Errors.Last().Error(), "another knowledge base")
}

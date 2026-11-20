package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// upsertFAQProbe drives the batch-upsert handler with one body. The service is
// only reached when binding succeeded, so a nil service doubles as proof that
// the payload never got past the read.
func upsertFAQProbe(
	t *testing.T, body string, svc *fakeFAQUpsertService,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/faq/entries", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: "kb-1"}}

	h := &FAQHandler{knowledgeService: svc}
	h.UpsertEntries(c)
	return c, w
}

type fakeFAQUpsertService struct {
	interfaces.KnowledgeService
	calls int
}

func (s *fakeFAQUpsertService) UpsertFAQEntries(
	_ context.Context, _ string, _ *types.FAQBatchUpsertPayload,
) (string, error) {
	s.calls++
	return "task-1", nil
}

// The batch upsert payload's entries array has no count limit, and validation
// only runs after ShouldBindJSON has materialized the whole array, so without a
// reader cap one request can make the process hold an arbitrarily large body.
func TestUpsertFAQsRejectsAnOversizedBody(t *testing.T) {
	oversize := int(faqJSONMaxBytes()) + 1
	body := `{"entries":[{"standard_question":"` + strings.Repeat("a", oversize) + `"}]}`

	c, _ := upsertFAQProbe(t, body, &fakeFAQUpsertService{})

	require.NotEmpty(t, c.Errors)
	appErr, ok := c.Errors[0].Err.(*errors.AppError)
	require.True(t, ok, "expected an AppError, got %T", c.Errors[0].Err)
	require.Equal(t, errors.ErrBadRequest, appErr.Code)
	require.Equal(t, "request body is too large", appErr.Message)
}

// A malformed but small body must still report the parse failure, not the size
// limit: the cap changes only what is refused for size.
func TestUpsertFAQsKeepsTheParseErrorForMalformedBodies(t *testing.T) {
	c, _ := upsertFAQProbe(t, `{"entries":`, &fakeFAQUpsertService{})

	require.NotEmpty(t, c.Errors)
	appErr, ok := c.Errors[0].Err.(*errors.AppError)
	require.True(t, ok, "expected an AppError, got %T", c.Errors[0].Err)
	require.NotEqual(t, "request body is too large", appErr.Message)
}

// The cap must track the file-import budget rather than a second, unrelated
// number: a batch is the JSON equivalent of the CSV import, which is bounded by
// GetMaxFileSize().
func TestFAQBodyCapFollowsTheFileImportBudget(t *testing.T) {
	require.Greater(t, faqJSONMaxBytes(), int64(0))
	require.GreaterOrEqual(t, faqJSONMaxBytes(), int64(50*1024*1024),
		"the default budget must hold a 50 MiB batch")
}

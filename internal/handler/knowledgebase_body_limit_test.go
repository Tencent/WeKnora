package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

func createKnowledgeBaseProbe(t *testing.T, body string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set(types.TenantIDContextKey.String(), uint64(1))

	h := &KnowledgeBaseHandler{}
	h.CreateKnowledgeBase(c)
	return c
}

func firstAppError(t *testing.T, c *gin.Context) *errors.AppError {
	t.Helper()
	require.NotEmpty(t, c.Errors, "the handler must record an error")
	appErr, ok := c.Errors[0].Err.(*errors.AppError)
	require.True(t, ok, "expected an AppError, got %T", c.Errors[0].Err)
	return appErr
}

// A knowledge-base definition carries scalars, ids and prompt instructions,
// whose per-field limits keep a legal body to a few hundred KiB. Without a cap
// the handler buffered whatever arrived — the Logger middleware retains only
// 10 KiB for its log line but still hands the rest of the stream to the
// handler — so one authenticated POST could hold an arbitrary body in memory
// before the JSON parser saw it.
func TestCreateKnowledgeBaseRejectsAnOversizedBody(t *testing.T) {
	// Valid JSON past the cap: without the cap the parser would accept it and
	// the handler would go on to validate the name. The cap must answer first.
	body := `{"name":"` + strings.Repeat("a", knowledgeBaseJSONMaxBytes) + `"}`

	c := createKnowledgeBaseProbe(t, body)

	appErr := firstAppError(t, c)
	require.Equal(t, errors.ErrBadRequest, appErr.Code)
	require.Equal(t, "request body is too large", appErr.Message)
}

// The cap must not change how a legal body is answered: an empty name is still
// the validation error, proving the read completed and the JSON parsed.
func TestCreateKnowledgeBaseKeepsALegalBodyPath(t *testing.T) {
	c := createKnowledgeBaseProbe(t, `{"name":"   "}`)

	appErr := firstAppError(t, c)
	require.Equal(t, errors.ErrBadRequest, appErr.Code)
	require.Equal(t, "name cannot be empty", appErr.Message)
}

// A definition that fits the cap but fails to parse must still report the
// parse failure, not the size limit.
func TestCreateKnowledgeBaseStillReportsMalformedJSON(t *testing.T) {
	c := createKnowledgeBaseProbe(t, `{"name":`)

	appErr := firstAppError(t, c)
	require.Equal(t, errors.ErrBadRequest, appErr.Code)
	require.Contains(t, appErr.Message, "Invalid request parameters")
}

package session

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
)

// parseQARequestWithBody drives the shared QA entry point with one body, so the
// cap is measured where both chat routes install it.
func parseQARequestWithBody(t *testing.T, body string) error {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat/session-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Params = gin.Params{{Key: "session_id", Value: "session-1"}}

	h := &Handler{}
	_, _, err := h.parseQARequest(c, "KnowledgeQA")
	return err
}

// The chat body carries up to five inline base64 attachments whose decoded
// total may reach 100 MiB, and both /knowledge-chat and /agent-chat are
// reachable by anonymous embed visitors. ShouldBindJSON buffers the whole body
// before decoding and the attachment limits are applied after base64 decoding,
// so nothing else bounds the read: one request could allocate as much memory as
// the process will surrender.
func TestParseQARequestRejectsAnOversizedBody(t *testing.T) {
	body := `{"query":"` + strings.Repeat("a", int(maxQARequestBodyBytes)+1) + `"}`

	err := parseQARequestWithBody(t, body)

	require.Error(t, err)
	appErr, ok := err.(*apperrors.AppError)
	require.True(t, ok, "expected an AppError, got %T", err)
	require.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	require.Equal(t, "request body is too large", appErr.Message)
}

// The cap must not refuse a legal request: a body that fits is still parsed,
// and its failure (if any) is the ordinary validation error rather than a size
// error. An empty body is the simplest legal-envelope case.
func TestParseQARequestKeepsAHandledBodyPath(t *testing.T) {
	err := parseQARequestWithBody(t, `{}`)

	if err == nil {
		return
	}
	appErr, ok := err.(*apperrors.AppError)
	require.True(t, ok, "expected an AppError, got %T", err)
	assert.NotEqual(t, "request body is too large", appErr.Message)
}

// A malformed but small body must still report the parse failure, not the size
// limit: the cap changes only what is refused for size.
func TestParseQARequestKeepsTheParseErrorForMalformedBodies(t *testing.T) {
	err := parseQARequestWithBody(t, `{"query":`)

	require.Error(t, err)
	appErr, ok := err.(*apperrors.AppError)
	require.True(t, ok, "expected an AppError, got %T", err)
	require.NotEqual(t, "request body is too large", appErr.Message)
}

// The cap must leave room for the largest legal attachment set: five decoded
// files totalling 100 MiB arrive as ~134 MiB of base64, which the constant is
// derived from. Pin that relationship so an edit cannot quietly shrink it below
// the documented attachment budget.
func TestQABodyCapExceedsTheEncodedAttachmentBudget(t *testing.T) {
	encoded := int64(maxAttachmentUploadTotalBytes) * 4 / 3

	require.Greater(t, int64(maxQARequestBodyBytes), encoded,
		"the body cap must exceed the base64 encoding of the attachment total")
}

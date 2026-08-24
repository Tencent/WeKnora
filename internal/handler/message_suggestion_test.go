package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// When the suggestion pipeline bubbles up a session-not-found (e.g. an embed
// Viewer whose session read was rejected), the response must be a 404, not a
// misleading 500. apperrors.ErrSessionNotFound is a bare errors.New and so is
// not caught by the gorm.ErrRecordNotFound branch on its own.
func TestMessageSuggestionWriteErrorMapsSessionNotFoundTo404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewMessageSuggestionHandler(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	h.writeError(c, apperrors.ErrSessionNotFound)

	require.Len(t, c.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, c.Errors[0].Err, &appErr)
	require.Equal(t, apperrors.ErrNotFound, appErr.Code)
	require.Equal(t, http.StatusNotFound, appErr.HTTPCode)
}

package handler

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubMessageService satisfies just the four MessageService methods this
// file exercises. Embedding the interface keeps the rest nil-panicky on
// purpose: any test that reaches an un-stubbed method should fail loudly
// rather than silently returning zero-values.
type stubMessageService struct {
	interfaces.MessageService
	getRecent     func(ctx context.Context, sessionID string, limit int) ([]*types.Message, error)
	getBeforeTime func(ctx context.Context, sessionID string, beforeTime time.Time, limit int) ([]*types.Message, error)
	deleteMessage func(ctx context.Context, sessionID, id string) error
}

func (s *stubMessageService) GetRecentMessagesBySession(
	ctx context.Context, sessionID string, limit int,
) ([]*types.Message, error) {
	return s.getRecent(ctx, sessionID, limit)
}

func (s *stubMessageService) GetMessagesBySessionBeforeTime(
	ctx context.Context, sessionID string, beforeTime time.Time, limit int,
) ([]*types.Message, error) {
	return s.getBeforeTime(ctx, sessionID, beforeTime, limit)
}

func (s *stubMessageService) DeleteMessage(ctx context.Context, sessionID, id string) error {
	return s.deleteMessage(ctx, sessionID, id)
}

// newMessageTestRouter mounts the standard ErrorHandler middleware so
// c.Error(NewNotFoundError(...)) renders as a real 404 envelope —
// matching production routing exactly.
func newMessageTestRouter(svc interfaces.MessageService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &MessageHandler{MessageService: svc}
	r.GET("/messages/:session_id/load", h.LoadMessages)
	r.DELETE("/messages/:session_id/:id", h.DeleteMessage)
	return r
}

// TestLoadMessagesMapsErrSessionNotFoundToNotFound pins the fix for
// PR #1309's surfacing gap: when message-service ops can't see the
// owning session (non-owner / wrong tenant), the handler used to
// surface a 500 because ErrSessionNotFound flowed through the generic
// error branch. We now map it to 404 so clients can tell "wrong URL"
// from a real 5xx.
func TestLoadMessagesMapsErrSessionNotFoundToNotFound(t *testing.T) {
	svc := &stubMessageService{
		getRecent: func(_ context.Context, _ string, _ int) ([]*types.Message, error) {
			return nil, apperrors.ErrSessionNotFound
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/messages/sess1/load?limit=20", nil)
	newMessageTestRouter(svc).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("ErrSessionNotFound must map to 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestLoadMessagesBeforeTimeMapsErrSessionNotFoundToNotFound covers the
// second branch of LoadMessages (the `before_time` path), which has its
// own error-handling block.
func TestLoadMessagesBeforeTimeMapsErrSessionNotFoundToNotFound(t *testing.T) {
	svc := &stubMessageService{
		getBeforeTime: func(_ context.Context, _ string, _ time.Time, _ int) ([]*types.Message, error) {
			return nil, apperrors.ErrSessionNotFound
		},
	}
	w := httptest.NewRecorder()
	url := "/messages/sess1/load?limit=20&before_time=" + time.Now().UTC().Format(time.RFC3339Nano)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	newMessageTestRouter(svc).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("ErrSessionNotFound on before-time path must map to 404, got %d body=%s",
			w.Code, w.Body.String())
	}
}

// TestDeleteMessageMapsErrSessionNotFoundToNotFound mirrors the
// LoadMessages test for the delete endpoint.
func TestDeleteMessageMapsErrSessionNotFoundToNotFound(t *testing.T) {
	svc := &stubMessageService{
		deleteMessage: func(_ context.Context, _, _ string) error {
			return apperrors.ErrSessionNotFound
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/messages/sess1/msg1", nil)
	newMessageTestRouter(svc).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("ErrSessionNotFound on DeleteMessage must map to 404, got %d body=%s",
			w.Code, w.Body.String())
	}
}

// TestDeleteMessageHonoursWrappedErrSessionNotFound is the regression
// test against the stale `==` sentinel comparison the original PR used.
// errors.Is unwraps fmt.Errorf("%w", ...); a literal `==` does not. If
// anyone reverts the comparison, this test fails before the change ships.
func TestDeleteMessageHonoursWrappedErrSessionNotFound(t *testing.T) {
	svc := &stubMessageService{
		deleteMessage: func(_ context.Context, _, _ string) error {
			return fmt.Errorf("delete: %w", apperrors.ErrSessionNotFound)
		},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/messages/sess1/msg1", nil)
	newMessageTestRouter(svc).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("wrapped ErrSessionNotFound must still map to 404, got %d body=%s",
			w.Code, w.Body.String())
	}
	// Defence-in-depth: the response body should expose the AppError
	// envelope (ErrorHandler renders {success,error:{code,message}}),
	// not the bare gin error string.
	if !strings.Contains(w.Body.String(), `"code":1003`) {
		t.Fatalf("expected NotFound envelope with code=1003, got body=%s", w.Body.String())
	}
}

// guardAgainstStaleSentinelEquality is a compile-time assertion: if a
// future refactor accidentally drops the stderrors import, the test file
// stops compiling and the human gets a clear signal that something tied
// to errors.Is wrapping went away.
var _ = stderrors.Is

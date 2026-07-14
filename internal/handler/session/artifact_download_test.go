package session

import (
	"context"
	stderrors "errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// -----------------------------------------------------------------------------
// Test doubles
//
// The download handler only touches three collaborators: SessionService (for
// ownership check), MessageService (for the message + artifact list), and
// FileService (for the blob stream). We embed the full interfaces so the
// zero-value struct compiles, then override just the methods each test
// exercises. A stray call to an un-stubbed method deliberately nil-panics
// so the failure surfaces immediately.
// -----------------------------------------------------------------------------

type stubSessionService struct {
	interfaces.SessionService
	getSession func(ctx context.Context, id string) (*types.Session, error)
}

func (s *stubSessionService) GetSession(ctx context.Context, id string) (*types.Session, error) {
	return s.getSession(ctx, id)
}

type stubMessageServiceForArtifacts struct {
	interfaces.MessageService
	getMessage         func(ctx context.Context, sessionID, id string) (*types.Message, error)
	getSessionArtifact func(ctx context.Context, sessionID string) (types.MessageArtifacts, error)
}

func (s *stubMessageServiceForArtifacts) GetMessage(ctx context.Context, sessionID, id string) (*types.Message, error) {
	return s.getMessage(ctx, sessionID, id)
}

func (s *stubMessageServiceForArtifacts) GetSessionArtifacts(ctx context.Context, sessionID string) (types.MessageArtifacts, error) {
	if s.getSessionArtifact == nil {
		return types.MessageArtifacts{}, nil
	}
	return s.getSessionArtifact(ctx, sessionID)
}

// fakeArtifactFileService serves canned bytes for a single URL.
type fakeArtifactFileService struct {
	interfaces.FileService
	url  string
	data []byte
}

func (f *fakeArtifactFileService) GetFile(_ context.Context, url string) (io.ReadCloser, error) {
	if url != f.url {
		return nil, stderrors.New("not found")
	}
	return io.NopCloser(strings.NewReader(string(f.data))), nil
}

func (f *fakeArtifactFileService) SaveFile(_ context.Context, _ *multipart.FileHeader, _ uint64, _ string) (string, error) {
	return "", nil
}

func (f *fakeArtifactFileService) SaveBytes(_ context.Context, _ []byte, _ uint64, _ string, _ bool) (string, error) {
	return "", nil
}

func (f *fakeArtifactFileService) GetFileURL(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (f *fakeArtifactFileService) DeleteFile(_ context.Context, _ string) error {
	return nil
}

// -----------------------------------------------------------------------------
// Router builder
// -----------------------------------------------------------------------------

func newArtifactTestRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	// Match the production route's wildcard names (see router.go — GET tree
	// binds :id to align with /sessions/:id) so paramSessionID resolves via
	// the same code path exercised in prod.
	r.GET("/sessions/:id/artifacts", h.ListSessionArtifacts)
	r.GET("/sessions/:id/messages/:message_id/artifacts", h.ListMessageArtifacts)
	r.GET("/sessions/:id/messages/:message_id/artifacts/:index/download", h.DownloadMessageArtifact)
	return r
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestDownloadMessageArtifact_HappyPath(t *testing.T) {
	sessionID := "sess-1"
	messageID := "msg-1"
	url := "fake://tenant-42/report.pptx"
	body := []byte("PPTX-BYTES")

	h := &Handler{
		sessionService: &stubSessionService{
			getSession: func(_ context.Context, id string) (*types.Session, error) {
				if id != sessionID {
					return nil, apperrors.ErrSessionNotFound
				}
				return &types.Session{ID: id, TenantID: 42}, nil
			},
		},
		messageService: &stubMessageServiceForArtifacts{
			getMessage: func(_ context.Context, sid, mid string) (*types.Message, error) {
				if sid != sessionID || mid != messageID {
					return nil, nil
				}
				return &types.Message{
					ID: mid, SessionID: sid,
					Artifacts: types.MessageArtifacts{{
						URL:      url,
						FileName: "报告.pptx", // non-ASCII to exercise RFC 5987 encoding
						FileType: ".pptx",
						FileSize: int64(len(body)),
					}},
				}, nil
			},
		},
		fileService: &fakeArtifactFileService{url: url, data: body},
	}

	router := newArtifactTestRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/sessions/sess-1/messages/msg-1/artifacts/0/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != string(body) {
		t.Fatalf("body = %q, want %q", got, string(body))
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition = %q, want attachment prefix", cd)
	}
	if !strings.Contains(cd, "filename*=UTF-8''") {
		t.Fatalf("Content-Disposition = %q, want RFC 5987 filename*", cd)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestDownloadMessageArtifact_SessionNotOwnedReturns404(t *testing.T) {
	h := &Handler{
		sessionService: &stubSessionService{
			getSession: func(_ context.Context, _ string) (*types.Session, error) {
				return nil, apperrors.ErrSessionNotFound
			},
		},
	}
	router := newArtifactTestRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/sessions/sess-x/messages/msg-x/artifacts/0/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestDownloadMessageArtifact_IndexOutOfRange(t *testing.T) {
	sessionID := "sess-1"
	messageID := "msg-1"
	h := &Handler{
		sessionService: &stubSessionService{
			getSession: func(_ context.Context, _ string) (*types.Session, error) {
				return &types.Session{ID: sessionID, TenantID: 42}, nil
			},
		},
		messageService: &stubMessageServiceForArtifacts{
			getMessage: func(_ context.Context, _, _ string) (*types.Message, error) {
				return &types.Message{ID: messageID, SessionID: sessionID, Artifacts: types.MessageArtifacts{}}, nil
			},
		},
	}
	router := newArtifactTestRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/sessions/sess-1/messages/msg-1/artifacts/7/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for out-of-range index", w.Code)
	}
}

func TestDownloadMessageArtifact_InvalidIndex(t *testing.T) {
	h := &Handler{
		sessionService: &stubSessionService{
			getSession: func(_ context.Context, _ string) (*types.Session, error) {
				return &types.Session{ID: "sess-1", TenantID: 42}, nil
			},
		},
	}
	router := newArtifactTestRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/sessions/sess-1/messages/msg-1/artifacts/not-a-number/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for non-integer index", w.Code)
	}
}

func TestListSessionArtifacts_StripsURL(t *testing.T) {
	h := &Handler{
		sessionService: &stubSessionService{
			getSession: func(_ context.Context, _ string) (*types.Session, error) {
				return &types.Session{ID: "sess-1", TenantID: 42}, nil
			},
		},
		messageService: &stubMessageServiceForArtifacts{
			getSessionArtifact: func(_ context.Context, _ string) (types.MessageArtifacts, error) {
				return types.MessageArtifacts{
					{URL: "fake://internal/1", FileName: "a.txt", FileSize: 1, CreatedAt: time.Now()},
				}, nil
			},
		},
	}
	router := newArtifactTestRouter(h)
	req := httptest.NewRequest(http.MethodGet, "/sessions/sess-1/artifacts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	// The URL must not appear in the response body: it's an internal
	// storage path and leaking it defeats the download endpoint's
	// ownership check.
	if strings.Contains(w.Body.String(), "fake://internal/1") {
		t.Fatalf("response body leaked storage URL: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "a.txt") {
		t.Fatalf("response body missing file name: %s", w.Body.String())
	}
}

func TestBuildAttachmentHeader_CJK(t *testing.T) {
	// The RFC 5987 filename* segment must percent-encode every non-attr-char
	// byte so downstream browsers preserve "报告.pptx" instead of mojibake.
	got := buildAttachmentHeader("报告.pptx")
	if !strings.Contains(got, "filename=") {
		t.Fatalf("missing ASCII filename fallback: %q", got)
	}
	if !strings.Contains(got, "filename*=UTF-8''") {
		t.Fatalf("missing RFC 5987 filename*: %q", got)
	}
	// Non-ASCII bytes must be percent-encoded (never appear literally).
	if strings.ContainsRune(got, '报') {
		t.Fatalf("filename* contains raw CJK: %q", got)
	}
}

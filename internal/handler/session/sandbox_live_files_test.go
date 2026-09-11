package session

import (
	"bytes"
	"context"
	stderrors "errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type liveFileSessionServiceStub struct {
	interfaces.SessionService
	getCalls      int
	getOwnedCalls int
	readErr       error
	writeErr      error
}

func (s *liveFileSessionServiceStub) GetSession(context.Context, string) (*types.Session, error) {
	s.getCalls++
	if s.readErr != nil {
		return nil, s.readErr
	}
	return &types.Session{ID: "session-owned"}, nil
}

func (s *liveFileSessionServiceStub) GetOwnedSession(context.Context, string) (*types.Session, error) {
	s.getOwnedCalls++
	if s.writeErr != nil {
		return nil, s.writeErr
	}
	return &types.Session{ID: "session-owned"}, nil
}

type liveFilesServiceStub struct {
	list    func(context.Context, string, string) ([]sandbox.SessionLiveFileEntry, error)
	read    func(context.Context, string, string) ([]byte, error)
	write   func(context.Context, string, string, []byte) error
	rename  func(context.Context, string, string, string) error
	remove  func(context.Context, string, string) error
	called  bool
	session string
	path    string
}

func (s *liveFilesServiceStub) List(
	ctx context.Context,
	sessionID, relativeDir string,
) ([]sandbox.SessionLiveFileEntry, error) {
	s.called, s.session, s.path = true, sessionID, relativeDir
	if s.list != nil {
		return s.list(ctx, sessionID, relativeDir)
	}
	return nil, nil
}

func (s *liveFilesServiceStub) Read(ctx context.Context, sessionID, relativePath string) ([]byte, error) {
	s.called, s.session, s.path = true, sessionID, relativePath
	if s.read != nil {
		return s.read(ctx, sessionID, relativePath)
	}
	return nil, nil
}

func (s *liveFilesServiceStub) Write(ctx context.Context, sessionID, relativePath string, content []byte) error {
	s.called, s.session, s.path = true, sessionID, relativePath
	if s.write != nil {
		return s.write(ctx, sessionID, relativePath, content)
	}
	return nil
}

func (s *liveFilesServiceStub) Rename(ctx context.Context, sessionID, source, target string) error {
	s.called, s.session, s.path = true, sessionID, source+"->"+target
	if s.rename != nil {
		return s.rename(ctx, sessionID, source, target)
	}
	return nil
}

func (s *liveFilesServiceStub) Remove(ctx context.Context, sessionID, relativePath string) error {
	s.called, s.session, s.path = true, sessionID, relativePath
	if s.remove != nil {
		return s.remove(ctx, sessionID, relativePath)
	}
	return nil
}

func liveFileTestContext(
	method, target, sessionID string,
	body *bytes.Reader,
) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	var requestBody io.Reader
	if body != nil {
		requestBody = body
	}
	c.Request = httptest.NewRequest(method, target, requestBody)
	c.Params = gin.Params{{Key: "id", Value: sessionID}, {Key: "session_id", Value: sessionID}}
	return c, recorder
}

func TestListSandboxLiveFilesAuthorizesBeforeAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessions := &liveFileSessionServiceStub{}
	files := &liveFilesServiceStub{
		list: func(_ context.Context, _, _ string) ([]sandbox.SessionLiveFileEntry, error) {
			return []sandbox.SessionLiveFileEntry{{
				Name: "result.txt", Path: "reports/result.txt", Type: sandbox.RemoteEntryFile,
			}}, nil
		},
	}
	h := &Handler{sessionService: sessions, liveFilesService: files}
	c, recorder := liveFileTestContext(http.MethodGet, "/?path=reports", "session-owned", nil)

	h.ListSandboxLiveFiles(c)

	if len(c.Errors) != 0 || recorder.Code != http.StatusOK {
		t.Fatalf("status=%d errors=%v", recorder.Code, c.Errors)
	}
	if sessions.getCalls != 1 || sessions.getOwnedCalls != 0 || !files.called || files.path != "reports" {
		t.Fatalf("unexpected calls: sessions=%+v files=%+v", sessions, files)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"path":"reports/result.txt"`) || strings.Contains(body, `"Path"`) {
		t.Fatalf("unexpected JSON shape: %s", body)
	}
}

func TestSandboxLiveFilesRejectsNonOwnedSessionBeforeProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessions := &liveFileSessionServiceStub{readErr: apperrors.ErrSessionNotFound}
	files := &liveFilesServiceStub{}
	h := &Handler{sessionService: sessions, liveFilesService: files}
	c, _ := liveFileTestContext(http.MethodGet, "/?path=secret.txt", "other-session", nil)

	h.DownloadSandboxLiveFile(c)

	if files.called {
		t.Fatal("provider was called before session authorization")
	}
	if len(c.Errors) != 1 {
		t.Fatalf("errors=%v want one authorization error", c.Errors)
	}
	appErr, ok := c.Errors[0].Err.(*apperrors.AppError)
	if !ok || appErr.HTTPCode != http.StatusNotFound {
		t.Fatalf("error=%T %v want 404 AppError", c.Errors[0].Err, c.Errors[0].Err)
	}
}

func TestDownloadSandboxLiveFileSetsSafeAttachmentHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessions := &liveFileSessionServiceStub{}
	files := &liveFilesServiceStub{read: func(context.Context, string, string) ([]byte, error) {
		return []byte("<svg onload=alert(1) />"), nil
	}}
	h := &Handler{sessionService: sessions, liveFilesService: files}
	c, recorder := liveFileTestContext(
		http.MethodGet,
		"/?path=reports/%E7%BB%93%E6%9E%9C.svg",
		"session-owned",
		nil,
	)

	h.DownloadSandboxLiveFile(c)

	if len(c.Errors) != 0 || recorder.Code != http.StatusOK {
		t.Fatalf("status=%d errors=%v", recorder.Code, c.Errors)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q", got)
	}
	got := recorder.Header().Get("Content-Disposition")
	if !strings.HasPrefix(got, "attachment;") || !strings.Contains(got, "filename*=") {
		t.Fatalf("Content-Disposition=%q", got)
	}
}

func TestSandboxLiveFileMutationRejectsReadOnlyAdminFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessions := &liveFileSessionServiceStub{writeErr: apperrors.ErrSessionNotFound}
	files := &liveFilesServiceStub{}
	h := &Handler{sessionService: sessions, liveFilesService: files}
	c, _ := liveFileTestContext(http.MethodDelete, "/?path=report.txt", "other-session", nil)

	h.DeleteSandboxLiveFile(c)

	if sessions.getOwnedCalls != 1 || sessions.getCalls != 0 {
		t.Fatalf("mutation did not use strict owner lookup: %+v", sessions)
	}
	if files.called {
		t.Fatal("provider was called for a non-owned mutation")
	}
	if len(c.Errors) != 1 {
		t.Fatalf("errors=%v want one authorization error", c.Errors)
	}
}

func TestUploadSandboxLiveFileUsesStrictOwnerCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessions := &liveFileSessionServiceStub{}
	var uploaded []byte
	files := &liveFilesServiceStub{write: func(_ context.Context, _, _ string, content []byte) error {
		uploaded = append([]byte(nil), content...)
		return nil
	}}
	h := &Handler{sessionService: sessions, liveFilesService: files}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("path", "reports/new.txt"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("new body")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	c, recorder := liveFileTestContext(http.MethodPost, "/", "session-owned", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	h.UploadSandboxLiveFile(c)

	if len(c.Errors) != 0 || recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d errors=%v body=%s", recorder.Code, c.Errors, recorder.Body.String())
	}
	if sessions.getOwnedCalls != 1 || sessions.getCalls != 0 {
		t.Fatalf("mutation did not use strict ownership: %+v", sessions)
	}
	if files.path != "reports/new.txt" || string(uploaded) != "new body" {
		t.Fatalf("unexpected upload path=%q body=%q", files.path, uploaded)
	}
}

func TestSandboxLiveFilesMapsUnsafePathsWithoutLeakingDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessions := &liveFileSessionServiceStub{}
	files := &liveFilesServiceStub{read: func(context.Context, string, string) ([]byte, error) {
		return nil, stderrors.Join(sandbox.ErrLiveFileUnsafe, stderrors.New("/etc/passwd"))
	}}
	h := &Handler{sessionService: sessions, liveFilesService: files}
	c, _ := liveFileTestContext(http.MethodGet, "/?path=link", "session-owned", nil)

	h.DownloadSandboxLiveFile(c)

	if len(c.Errors) != 1 {
		t.Fatalf("errors=%v want one path error", c.Errors)
	}
	appErr, ok := c.Errors[0].Err.(*apperrors.AppError)
	if !ok || appErr.HTTPCode != http.StatusBadRequest || strings.Contains(appErr.Message, "/etc") {
		t.Fatalf("unsafe error leaked detail: %#v", appErr)
	}
}

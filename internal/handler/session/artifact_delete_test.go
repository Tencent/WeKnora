package session

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type stubMessageServiceForDelete struct {
	interfaces.MessageService
	got    *types.ArtifactDeleteRequest
	result *types.ArtifactDeleteResult
	err    error
}

func (s *stubMessageServiceForDelete) DeleteSessionArtifact(
	_ context.Context, req *types.ArtifactDeleteRequest,
) (*types.ArtifactDeleteResult, error) {
	s.got = req
	return s.result, s.err
}

type deleteCatalogStub struct {
	interfaces.ResourceCatalog
	remaining  int64
	releaseErr error
	released   []string
	resource   *types.StoredResource
}

func (s *deleteCatalogStub) Release(_ context.Context, ref, ownerType, ownerID string) (int64, error) {
	s.released = append(s.released, ref+"|"+ownerType+"|"+ownerID)
	return s.remaining, s.releaseErr
}

func (s *deleteCatalogStub) ResolvePath(_ context.Context, ref string) (string, *types.StoredResource, error) {
	if s.resource == nil {
		return ref, nil, nil
	}
	return s.resource.PhysicalPath, s.resource, nil
}

type deletingFileService struct {
	interfaces.FileService
	deleted []string
}

func (f *deletingFileService) DeleteFile(_ context.Context, path string) error {
	f.deleted = append(f.deleted, path)
	return nil
}

func newArtifactDeleteRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(42)))
		c.Next()
	})
	r.DELETE("/sessions/:id/messages/:message_id/artifacts/:index", h.DeleteMessageArtifact)
	r.DELETE("/artifacts", h.DeleteLibraryArtifact)
	return r
}

func TestDeleteMessageArtifactReclaimsTheBlob(t *testing.T) {
	catalog := &deleteCatalogStub{
		remaining: 0,
		resource:  &types.StoredResource{TenantID: 42, PhysicalPath: "local://42/exports/report.pptx"},
	}
	files := &deletingFileService{}
	svc := &stubMessageServiceForDelete{result: &types.ArtifactDeleteResult{
		FileName: "报告.pptx",
		Deleted:  1,
		Reclaim:  []types.ArtifactBlobRef{{URL: "resource://abc", MessageIDs: []string{"msg-1"}}},
	}}
	h := &Handler{messageService: svc, resourceCatalog: catalog, fileService: files}

	w := httptest.NewRecorder()
	newArtifactDeleteRouter(h).ServeHTTP(w,
		httptest.NewRequest(http.MethodDelete, "/sessions/sess-1/messages/msg-1/artifacts/2", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if svc.got.SessionID != "sess-1" || svc.got.MessageID != "msg-1" || svc.got.Index != 2 {
		t.Fatalf("service got %+v, want sess-1/msg-1/2", svc.got)
	}
	if svc.got.AllVersions {
		t.Fatal("the in-chat panel lists versions separately, so all_versions defaults to false")
	}
	want := "resource://abc|" + types.ResourceOwnerMessage + "|msg-1"
	if len(catalog.released) != 1 || catalog.released[0] != want {
		t.Fatalf("released = %v, want [%s]", catalog.released, want)
	}
	// DeleteFile takes the stored reference; the catalog-backed service
	// resolves it and marks the resource deleted itself.
	if len(files.deleted) != 1 || files.deleted[0] != "resource://abc" {
		t.Fatalf("deleted = %v, want [resource://abc]", files.deleted)
	}
}

// A blob another owner still claims — an answer saved into a knowledge base, a
// later turn that re-attached the same file — must survive the delete.
func TestDeleteMessageArtifactKeepsBlobStillReferenced(t *testing.T) {
	catalog := &deleteCatalogStub{remaining: 1}
	files := &deletingFileService{}
	h := &Handler{
		messageService: &stubMessageServiceForDelete{result: &types.ArtifactDeleteResult{
			FileName: "shared.pptx",
			Deleted:  1,
			Reclaim:  []types.ArtifactBlobRef{{URL: "resource://shared", MessageIDs: []string{"msg-1"}}},
		}},
		resourceCatalog: catalog,
		fileService:     files,
	}

	w := httptest.NewRecorder()
	newArtifactDeleteRouter(h).ServeHTTP(w,
		httptest.NewRequest(http.MethodDelete, "/sessions/sess-1/messages/msg-1/artifacts/0", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(files.deleted) != 0 {
		t.Fatalf("deleted = %v, want none: another owner still references the blob", files.deleted)
	}
}

// An unreadable binding count keeps the bytes: an orphaned blob is reclaimable
// later, a file that vanished from someone else's document is not.
func TestDeleteMessageArtifactKeepsBlobWhenReleaseFails(t *testing.T) {
	files := &deletingFileService{}
	h := &Handler{
		messageService: &stubMessageServiceForDelete{result: &types.ArtifactDeleteResult{
			Deleted: 1,
			Reclaim: []types.ArtifactBlobRef{{URL: "resource://x", MessageIDs: []string{"msg-1"}}},
		}},
		resourceCatalog: &deleteCatalogStub{releaseErr: stderrors.New("catalog down")},
		fileService:     files,
	}

	w := httptest.NewRecorder()
	newArtifactDeleteRouter(h).ServeHTTP(w,
		httptest.NewRequest(http.MethodDelete, "/sessions/sess-1/messages/msg-1/artifacts/0", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: the rows are already tombstoned", w.Code)
	}
	if len(files.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", files.deleted)
	}
}

// Deleting several versions can retire more than one claim on the same object:
// a later answer that re-attached an earlier file has its own binding. Every
// claim has to go before the bytes count as unreferenced.
func TestDeleteArtifactReleasesEveryOwningMessage(t *testing.T) {
	catalog := &deleteCatalogStub{remaining: 0}
	files := &deletingFileService{}
	h := &Handler{
		messageService: &stubMessageServiceForDelete{result: &types.ArtifactDeleteResult{
			FileName: "report.pptx",
			Deleted:  2,
			Reclaim: []types.ArtifactBlobRef{
				{URL: "resource://abc", MessageIDs: []string{"msg-1", "msg-2"}},
			},
		}},
		resourceCatalog: catalog,
		fileService:     files,
	}

	w := httptest.NewRecorder()
	newArtifactDeleteRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodDelete,
		"/artifacts?session_id=sess-1&message_id=msg-1&index=0", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	want := []string{
		"resource://abc|" + types.ResourceOwnerMessage + "|msg-1",
		"resource://abc|" + types.ResourceOwnerMessage + "|msg-2",
	}
	if len(catalog.released) != 2 || catalog.released[0] != want[0] || catalog.released[1] != want[1] {
		t.Fatalf("released = %v, want %v", catalog.released, want)
	}
	if len(files.deleted) != 1 {
		t.Fatalf("deleted = %v, want the object removed exactly once", files.deleted)
	}
}

// The library row is a file with a version count, not one row per
// regeneration, so its delete takes every version unless told otherwise.
func TestDeleteLibraryArtifactDefaultsToAllVersions(t *testing.T) {
	svc := &stubMessageServiceForDelete{result: &types.ArtifactDeleteResult{FileName: "a.pptx", Deleted: 3}}
	h := &Handler{messageService: svc, fileService: &deletingFileService{}}
	router := newArtifactDeleteRouter(h)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete,
		"/artifacts?session_id=sess-1&message_id=msg-1&index=0", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if !svc.got.AllVersions {
		t.Fatal("library deletes take every version by default")
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete,
		"/artifacts?session_id=sess-1&message_id=msg-1&index=0&all_versions=false", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if svc.got.AllVersions {
		t.Fatal("all_versions=false must be honoured")
	}
}

// The two routes differ only in what an absent flag means. An explicit value
// must be read identically on both, so a client cannot have "1" mean one thing
// on one route and another elsewhere.
func TestAllVersionsIsParsedTheSameOnBothRoutes(t *testing.T) {
	cases := []struct {
		query   string
		session bool // in-chat route, absent => false
		library bool // library route, absent => true
	}{
		{"", false, true},
		{"&all_versions=true", true, true},
		{"&all_versions=false", false, false},
		{"&all_versions=1", true, true},
		{"&all_versions=0", false, false},
		{"&all_versions=TRUE", true, true},
		{"&all_versions=F", false, false},
		// Unparseable falls back to the route's default rather than to false.
		{"&all_versions=perhaps", false, true},
	}
	for _, tc := range cases {
		t.Run("q="+tc.query, func(t *testing.T) {
			svc := &stubMessageServiceForDelete{result: &types.ArtifactDeleteResult{Deleted: 1}}
			router := newArtifactDeleteRouter(&Handler{messageService: svc, fileService: &deletingFileService{}})

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete,
				"/sessions/sess-1/messages/msg-1/artifacts/0?x=1"+tc.query, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("session route status = %d, want 200", w.Code)
			}
			if svc.got.AllVersions != tc.session {
				t.Fatalf("session route all_versions = %v, want %v", svc.got.AllVersions, tc.session)
			}

			w = httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete,
				"/artifacts?session_id=sess-1&message_id=msg-1&index=0"+tc.query, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("library route status = %d, want 200", w.Code)
			}
			if svc.got.AllVersions != tc.library {
				t.Fatalf("library route all_versions = %v, want %v", svc.got.AllVersions, tc.library)
			}
		})
	}
}

func TestDeleteArtifactErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"already deleted or unknown", service.ErrArtifactNotFound, http.StatusNotFound},
		{"session not owned", apperrors.ErrSessionNotFound, http.StatusNotFound},
		{"repository failure", stderrors.New("db down"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{messageService: &stubMessageServiceForDelete{err: tc.err}}
			w := httptest.NewRecorder()
			newArtifactDeleteRouter(h).ServeHTTP(w,
				httptest.NewRequest(http.MethodDelete, "/sessions/sess-1/messages/msg-1/artifacts/0", nil))
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestDeleteMessageArtifactRejectsBadIndex(t *testing.T) {
	h := &Handler{messageService: &stubMessageServiceForDelete{}}
	w := httptest.NewRecorder()
	newArtifactDeleteRouter(h).ServeHTTP(w,
		httptest.NewRequest(http.MethodDelete, "/sessions/sess-1/messages/msg-1/artifacts/-1", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

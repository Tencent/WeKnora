package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubGalleryUserSvc serves the saved gallery preferences to
// resolveGalleryConfig; the rest of the service interface stays unused.
type stubGalleryUserSvc struct {
	interfaces.UserService
	user *types.User
}

func (s *stubGalleryUserSvc) GetUserByID(_ context.Context, _ string) (*types.User, error) {
	return s.user, nil
}

func newGalleryConfigRouter(userSvc interfaces.UserService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	// Mirror applyAuthSession: the caller identity must land on the request
	// context too, because resolveGalleryConfig reads it via
	// types.UserIDFromContext (not c.Get).
	r.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.UserIDContextKey, "u-test")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h := &KnowledgeHandler{kbService: &stubFolderKBService{}, userSvc: userSvc}
	r.GET("/knowledge-bases/:id/gallery-config", h.GetGalleryConfig)
	return r
}

// The gallery-config response must carry the resolved view_mode/thumb_cols
// through: a missing key here silently reverts the gallery UI to defaults on
// every reload even though the write path persisted the preference.
func TestGetGalleryConfigPassesViewPrefsThrough(t *testing.T) {
	hide := false
	svc := &stubGalleryUserSvc{user: &types.User{
		Preferences: types.UserPreferences{
			Gallery: &types.GalleryUserPrefs{ViewMode: "card", ThumbCols: 6, ShowMeta: &hide},
		},
	}}
	r := newGalleryConfigRouter(svc)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/gallery-config", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Data struct {
			ViewMode  string `json:"view_mode"`
			ThumbCols int    `json:"thumb_cols"`
			ShowMeta  *bool  `json:"show_meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.ViewMode != "card" || body.Data.ThumbCols != 6 {
		t.Fatalf("view prefs not passed through: view_mode=%q thumb_cols=%d",
			body.Data.ViewMode, body.Data.ThumbCols)
	}
	if body.Data.ShowMeta == nil || *body.Data.ShowMeta {
		t.Fatalf("show_meta = %v, want false", body.Data.ShowMeta)
	}
}

// Without recorded prefs the contract degrades to defaults instead of an
// error, and the keys are still present so the client keeps one code path.
func TestGetGalleryConfigDefaultsWhenNoPrefs(t *testing.T) {
	r := newGalleryConfigRouter(&stubGalleryUserSvc{user: &types.User{}})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/gallery-config", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Data struct {
			ViewMode  string `json:"view_mode"`
			ThumbCols int    `json:"thumb_cols"`
			ShowMeta  *bool  `json:"show_meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.ViewMode != "waterfall" || body.Data.ThumbCols != 0 {
		t.Fatalf("unexpected defaults: view_mode=%q thumb_cols=%d",
			body.Data.ViewMode, body.Data.ThumbCols)
	}
	if body.Data.ShowMeta == nil || !*body.Data.ShowMeta {
		t.Fatalf("show_meta = %v, want true", body.Data.ShowMeta)
	}
}

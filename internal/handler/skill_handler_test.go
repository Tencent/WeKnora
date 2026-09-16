package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeUsableSkillLister struct {
	tenantID uint64
	configID string
	skills   []*types.TenantSkillEntity
}

func (f *fakeUsableSkillLister) ListUsableSkills(
	_ context.Context, tenantID uint64, configID string,
) []*types.TenantSkillEntity {
	f.tenantID = tenantID
	f.configID = configID
	if configID == "" {
		return nil
	}
	return f.skills
}

func newChatSkillRouter(h *SkillHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), testSkillTenantID)
		c.Next()
	})
	r.GET("/skills", h.ListSkills)
	return r
}

func TestListSkillsHidesThePickerWhenNoSandboxConfigIsSelected(t *testing.T) {
	lister := &fakeUsableSkillLister{
		skills: []*types.TenantSkillEntity{{Name: "ppt-generator", Description: "make ppt"}},
	}
	router := newChatSkillRouter(NewSkillHandler(lister, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/skills", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Success         bool `json:"success"`
		Data            []SkillInfoResponse
		SkillsAvailable bool `json:"skills_available"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Empty(t, body.Data, "unscoped skills must not appear in @")
	require.False(t, body.SkillsAvailable)
	require.Empty(t, lister.configID)
}

func TestListSkillsReturnsUsableInstalledSkillsForTheSelectedConfig(t *testing.T) {
	lister := &fakeUsableSkillLister{
		skills: []*types.TenantSkillEntity{
			{Name: "ppt-generator", Description: "make ppt"},
		},
	}
	router := newChatSkillRouter(NewSkillHandler(lister, nil))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(
		http.MethodGet, "/skills?sandbox_config_id=cfg-1", nil,
	))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, testSkillTenantID, lister.tenantID)
	require.Equal(t, "cfg-1", lister.configID)

	var body struct {
		Success         bool `json:"success"`
		Data            []SkillInfoResponse
		SkillsAvailable bool `json:"skills_available"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.True(t, body.SkillsAvailable)
	require.Equal(t, []SkillInfoResponse{
		{Name: "ppt-generator", Description: "make ppt"},
	}, body.Data)
}

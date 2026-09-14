package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeSkillCatalog struct {
	list         []service.SkillCatalogView
	registerID   string
	installs     map[string]string
	installErrs  map[string]string
	deleteErr    error
	source       string
	installCalls int
}

func (f *fakeSkillCatalog) ListCatalog(context.Context, uint64) ([]service.SkillCatalogView, error) {
	return f.list, nil
}

func (f *fakeSkillCatalog) RegisterCatalogFromArchive(
	context.Context, uint64, []byte,
) (*types.TenantSkillCatalogEntity, error) {
	return &types.TenantSkillCatalogEntity{ID: f.registerID, Name: "pdf"}, nil
}

func (f *fakeSkillCatalog) RegisterCatalogFromSource(
	_ context.Context, _ uint64, source string,
) (*types.TenantSkillCatalogEntity, error) {
	f.source = source
	return &types.TenantSkillCatalogEntity{ID: f.registerID, Name: "pdf"}, nil
}

func (f *fakeSkillCatalog) InstallCatalogToConfigs(
	context.Context, uint64, string, []string,
) (*service.CatalogInstallResult, error) {
	f.installCalls++
	return &service.CatalogInstallResult{Installs: f.installs, Errors: f.installErrs}, nil
}

func (f *fakeSkillCatalog) DeleteCatalog(context.Context, uint64, string) error {
	return f.deleteErr
}

func (f *fakeSkillCatalog) ListCatalogFiles(context.Context, uint64, string) ([]service.SkillFileEntry, error) {
	return nil, nil
}

func (f *fakeSkillCatalog) ReadCatalogFile(context.Context, uint64, string, string) (*service.SkillFileContent, error) {
	return nil, nil
}

func newCatalogRouter(h *SkillHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), testSkillTenantID)
		c.Next()
	})
	r.GET("/skills/catalog", h.ListCatalog)
	r.POST("/skills/catalog", h.RegisterCatalog)
	r.POST("/skills/catalog/:id/install", h.InstallCatalog)
	r.DELETE("/skills/catalog/:id", h.DeleteCatalog)
	return r
}

func TestListCatalogReturnsDefinitionsAndInstallations(t *testing.T) {
	now := time.Now()
	catalog := &fakeSkillCatalog{
		list: []service.SkillCatalogView{{
			ID: "cat-1", Name: "pdf", Description: "extract", CreatedAt: now, UpdatedAt: now,
			Installations: []service.SkillCatalogInstallView{{
				SkillID: "sk-1", SandboxConfigID: "cfg-1", SandboxConfigName: "prod",
				Status: types.SkillStatusReady, Enabled: true, UpdatedAt: now,
			}},
		}},
	}
	router := newCatalogRouter(NewSkillHandler(&fakeUsableSkillLister{}, catalog))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/skills/catalog", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Success bool
		Data    []service.SkillCatalogView
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Len(t, body.Data, 1)
	require.Equal(t, "pdf", body.Data[0].Name)
	require.Equal(t, "cfg-1", body.Data[0].Installations[0].SandboxConfigID)
}

func TestRegisterCatalogFromSource(t *testing.T) {
	catalog := &fakeSkillCatalog{registerID: "cat-9"}
	router := newCatalogRouter(NewSkillHandler(&fakeUsableSkillLister{}, catalog))

	body, err := json.Marshal(map[string]string{"source": "@owner/pdf"})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/skills/catalog", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "@owner/pdf", catalog.source)
}

func TestRegisterCatalogFromSourceRejectsAnOversizedJSONBody(t *testing.T) {
	catalog := &fakeSkillCatalog{registerID: "cat-9"}
	router := newCatalogRouter(NewSkillHandler(&fakeUsableSkillLister{}, catalog))

	req := httptest.NewRequest(http.MethodPost, "/skills/catalog",
		bytes.NewReader(oversizedSkillSourceJSON(1)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "skill source request is too large")
	require.Empty(t, catalog.source)
}

func TestInstallCatalogRejectsAnOversizedJSONBody(t *testing.T) {
	catalog := &fakeSkillCatalog{installs: map[string]string{"cfg-1": "sk-1"}}
	router := newCatalogRouter(NewSkillHandler(&fakeUsableSkillLister{}, catalog))

	req := httptest.NewRequest(http.MethodPost, "/skills/catalog/cat-1/install",
		bytes.NewReader(oversizedSkillSourceJSON(1)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "skill request is too large")
	require.Equal(t, 0, catalog.installCalls)
}

func TestInstallCatalogAcceptsPerConfigIDs(t *testing.T) {
	catalog := &fakeSkillCatalog{installs: map[string]string{"cfg-1": "sk-1"}}
	router := newCatalogRouter(NewSkillHandler(&fakeUsableSkillLister{}, catalog))

	body, err := json.Marshal(map[string][]string{"sandbox_config_ids": {"cfg-1"}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/skills/catalog/cat-1/install", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
}

func TestInstallCatalogIncludesPerConfigErrors(t *testing.T) {
	catalog := &fakeSkillCatalog{
		installs:    map[string]string{"cfg-1": "sk-1"},
		installErrs: map[string]string{"cfg-2": "sandbox config not found"},
	}
	router := newCatalogRouter(NewSkillHandler(&fakeUsableSkillLister{}, catalog))

	body, err := json.Marshal(map[string][]string{"sandbox_config_ids": {"cfg-1", "cfg-2"}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/skills/catalog/cat-1/install", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Installs map[string]string `json:"installs"`
			Errors   map[string]string `json:"errors"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.False(t, payload.Success)
	require.Equal(t, "sk-1", payload.Data.Installs["cfg-1"])
	require.Equal(t, "sandbox config not found", payload.Data.Errors["cfg-2"])
}

func TestDeleteCatalogRefusesWhileInstalled(t *testing.T) {
	catalog := &fakeSkillCatalog{
		deleteErr: apperrors.NewConflictError("remove this skill from every sandbox"),
	}
	router := newCatalogRouter(NewSkillHandler(&fakeUsableSkillLister{}, catalog))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/skills/catalog/cat-1", nil))
	require.Equal(t, http.StatusConflict, w.Code)
}

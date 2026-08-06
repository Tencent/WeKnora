package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// Responses must never carry plaintext credentials, no matter which path
// produced them.
func TestSandboxConfigResponseMasksSecrets(t *testing.T) {
	cfg := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "super-secret"},
		EnvVars:     map[string]string{"TOKEN": "also-secret"},
	}
	entity := &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		Name:        "primary",
		SandboxType: "e2b",
		Config:      cfg,
		CreatedAt:   time.Unix(1, 0),
		UpdatedAt:   time.Unix(2, 0),
	}

	body, err := json.Marshal(toSandboxConfigResponse(entity))
	require.NoError(t, err)

	require.NotContains(t, string(body), "super-secret")
	require.NotContains(t, string(body), "also-secret")
	require.Contains(t, string(body), types.RedactedSecretPlaceholder)
}

// The 409 must be machine-readable: the UI renders "release N sandboxes first"
// from it rather than parsing a message string.
func TestSandboxesStillLiveMapsTo409WithCounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/sandbox-configs/:id", func(c *gin.Context) {
		respondSandboxesStillLive(c, service.SandboxInventory{
			SandboxCount: 3,
			SessionIDs:   []string{"s-1", "s-2"},
			AgentNames:   []string{"数据分析"},
		})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/sandbox-configs/cfg-a",
		strings.NewReader(`{}`))
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code)

	var payload struct {
		Error struct {
			Code string `json:"code"`
			Data struct {
				SandboxCount int      `json:"sandbox_count"`
				SessionIDs   []string `json:"session_ids"`
				AgentNames   []string `json:"agent_names"`
			} `json:"data"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, "sandboxes_still_live", payload.Error.Code)
	require.Equal(t, 3, payload.Error.Data.SandboxCount)
	require.Len(t, payload.Error.Data.SessionIDs, 2)
	require.Equal(t, []string{"数据分析"}, payload.Error.Data.AgentNames)
}

func TestSandboxInventoryUnverifiableMapsToDistinct409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/sandbox-configs/:id", func(c *gin.Context) {
		respondSandboxInventoryUnverifiable(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/sandbox-configs/cfg-a", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code)

	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, "sandbox_inventory_unverifiable", payload.Error.Code)
	require.Contains(t, payload.Error.Message, "无法连接")
}

func TestSandboxConfigDeletePassesForceQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := &fakeSandboxConfigService{}
	h := &SandboxConfigHandler{service: svc, defaults: &sandbox.ConfigDefaults{}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(42))
		c.Next()
	})
	router.DELETE("/sandbox-configs/:id", h.Delete)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/sandbox-configs/cfg-a?force=true", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, uint64(42), svc.deleteTenantID)
	require.Equal(t, "cfg-a", svc.deleteID)
	require.True(t, svc.deleteForce)

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/sandbox-configs/cfg-b", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "cfg-b", svc.deleteID)
	require.False(t, svc.deleteForce)
}

// Bad input must not surface as a 500. The sentinels are matched with errors.Is
// so rewording a message cannot silently reclassify these.
func TestSandboxConfigValidationSentinelsMapTo400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"missing name", service.ErrSandboxConfigNameRequired},
		{"unknown backend", fmt.Errorf("%w %q", sandbox.ErrUnsupportedSandboxType, "quantum")},
		{"unsafe endpoint", fmt.Errorf("%w: host is private", sandbox.ErrUnsafeOutboundURL)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			respondSandboxConfigServiceError(c, tc.err)

			require.Len(t, c.Errors, 1)
			var appErr *apperrors.AppError
			require.ErrorAs(t, c.Errors[0].Err, &appErr)
			require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
		})
	}
}

// An unexpected failure must keep its original error so the middleware can map
// it, rather than being flattened into a 400.
func TestSandboxConfigUnexpectedErrorIsNotDowngraded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	boom := stderrors.New("database is on fire")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	respondSandboxConfigServiceError(c, boom)

	require.Len(t, c.Errors, 1)
	require.ErrorIs(t, c.Errors[0].Err, boom)
}

type fakeSandboxConfigService struct {
	deleteTenantID uint64
	deleteID       string
	deleteForce    bool
}

func (s *fakeSandboxConfigService) Create(
	context.Context,
	uint64,
	service.CreateSandboxConfigInput,
) (*types.TenantSandboxConfigEntity, error) {
	return nil, nil
}

func (s *fakeSandboxConfigService) List(
	context.Context,
	uint64,
) ([]*types.TenantSandboxConfigEntity, error) {
	return nil, nil
}

func (s *fakeSandboxConfigService) Get(
	context.Context,
	uint64,
	string,
) (*types.TenantSandboxConfigEntity, error) {
	return nil, nil
}

func (s *fakeSandboxConfigService) Update(
	context.Context,
	uint64,
	string,
	service.UpdateSandboxConfigInput,
) (*types.TenantSandboxConfigEntity, error) {
	return nil, nil
}

func (s *fakeSandboxConfigService) Delete(
	_ context.Context,
	tenantID uint64,
	id string,
	force bool,
) error {
	s.deleteTenantID = tenantID
	s.deleteID = id
	s.deleteForce = force
	return nil
}

func (s *fakeSandboxConfigService) Inventory(
	context.Context,
	uint64,
	string,
) (service.SandboxInventory, error) {
	return service.SandboxInventory{}, nil
}

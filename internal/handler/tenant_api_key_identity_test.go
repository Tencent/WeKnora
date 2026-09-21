package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

type identityTestKeyService struct {
	interfaces.TenantAPIKeyService
	keys []*types.TenantAPIKey
}

func (s *identityTestKeyService) ListAPIKeys(context.Context, uint64) ([]*types.TenantAPIKey, error) {
	return s.keys, nil
}

func TestAPIKeyIdentityResponseRedactsSigningSecret(t *testing.T) {
	key := &types.TenantAPIKey{
		IdentityNamespace: "app-a",
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: "secret-must-not-leak",
		},
	}
	response := tenantAPIKeyForResponse(key)
	require.True(t, response.APIPrincipalConfig.HasHMACSecret)
	data, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret-must-not-leak")
	require.Contains(t, string(data), "app-a")
	model, err := json.Marshal(key)
	require.NoError(t, err)
	require.NotContains(t, string(model), "secret-must-not-leak")
}

func TestAPIKeyPlaygroundTokenUsesSelectedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tid := uint64(7)
	key := &types.TenantAPIKey{
		ID:                2,
		TenantID:          &tid,
		IdentityNamespace: "app-a",
		APIPrincipalConfig: &types.APIPrincipalConfig{
			Mode:       types.APIPrincipalModeSignedToken,
			HMACSecret: "key-secret",
		},
	}
	h := &TenantHandler{
		service: &stubTenantService{tenant: &types.Tenant{
			ID:                 tid,
			APIPrincipalConfig: &types.APIPrincipalConfig{Mode: types.APIPrincipalModeTenant},
		}},

		apiKeyService: &identityTestKeyService{keys: []*types.TenantAPIKey{key}},
	}
	engine := gin.New()
	engine.Use(middleware.ErrorHandler())
	engine.POST("/tenants/:id/api-principal-test-token", h.CreateAPIPrincipalTestToken)
	run := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/tenants/7/api-principal-test-token", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}
	rec := run(`{"api_key_id":2,"external_user_id":"alice"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var response struct {
		Data apiPrincipalTestTokenResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(
		response.Data.Token, claims,
		func(*jwt.Token) (interface{}, error) { return []byte("key-secret"), nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("weknora"),
	)
	require.NoError(t, err)
	require.True(t, token.Valid)
	require.Equal(t, "app-a", claims["identity_namespace"])
	require.Equal(t, "alice", claims["sub"])
	require.Equal(t, "7", claims["tenant_id"])
	rec = run(`{"api_key_id":999,"external_user_id":"alice"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	past := time.Now().Add(-time.Hour)
	key.ExpiresAt = &past
	rec = run(`{"api_key_id":2,"external_user_id":"alice"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	key.ExpiresAt = nil
	otherTenant := uint64(8)
	key.TenantID = &otherTenant
	rec = run(`{"api_key_id":2,"external_user_id":"alice"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

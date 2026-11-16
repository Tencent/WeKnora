package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// updateCapturingMCPServices records what UpdateMCPService is asked to merge.
type updateCapturingMCPServices struct {
	interfaces.MCPServiceService
	service *types.MCPService
	fields  map[string]bool
}

func (s *updateCapturingMCPServices) UpdateMCPService(
	_ context.Context, service *types.MCPService, fields map[string]bool,
) error {
	s.service, s.fields = service, fields
	return nil
}

func (s *updateCapturingMCPServices) GetMCPServiceByID(context.Context, uint64, string) (*types.MCPService, error) {
	return &types.MCPService{ID: "svc-1"}, nil
}

func (s *updateCapturingMCPServices) ListMCPMetadataSummaries(
	context.Context, uint64, []*types.MCPService,
) (map[string]*types.MCPMetadataSummary, error) {
	return nil, nil
}

func putMCPService(t *testing.T, body string) *updateCapturingMCPServices {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := &updateCapturingMCPServices{}
	h := &MCPServiceHandler{mcpServiceService: svc}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/mcp-services/svc-1", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "svc-1"}}
	c.Set(types.TenantIDContextKey.String(), uint64(7))
	h.UpdateMCPService(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return svc
}

func TestUpdateMCPServiceSendsClientIDOnlyWhenTheBodyHasIt(t *testing.T) {
	svc := putMCPService(t, `{"auth_config":{"auth_type":"oauth","client_id":"  pre-registered  "}}`)
	require.True(t, svc.fields["client_id"])
	require.Equal(t, "pre-registered", svc.service.AuthConfig.ClientID)

	svc = putMCPService(t, `{"auth_config":{"auth_type":"oauth","client_id":""}}`)
	require.True(t, svc.fields["client_id"], "an empty client_id clears it")

	svc = putMCPService(t, `{"auth_config":{"auth_type":"oauth"}}`)
	require.False(t, svc.fields["client_id"], "an absent client_id is kept")
}

func TestUpdateMCPServiceNeverTakesTheClientSecretFromTheMainBody(t *testing.T) {
	svc := putMCPService(t, `{"auth_config":{"auth_type":"oauth","client_secret":"from-main-body"}}`)
	require.Empty(t, svc.service.AuthConfig.ClientSecret)
}

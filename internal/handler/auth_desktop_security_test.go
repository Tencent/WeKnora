package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type desktopTestUsers struct {
	interfaces.UserService
	minted int
}

func (s *desktopTestUsers) GetUserByEmail(context.Context, string) (*types.User, error) {
	return &types.User{ID: "test-user", TenantID: 1}, nil
}

func (s *desktopTestUsers) GenerateTokens(context.Context, *types.User) (string, string, error) {
	s.minted++
	return "test-access", "test-refresh", nil
}

type desktopTestTenants struct{ interfaces.TenantService }

func (s *desktopTestTenants) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: 1}, nil
}

func TestAutoSetupRequiresNativeCapability(t *testing.T) {
	oldEdition, oldToken := Edition, liteSetupToken
	t.Cleanup(func() { Edition = oldEdition; liteSetupToken = oldToken })
	Edition = "lite"
	SetLiteSetupToken("test-native-capability")
	users := &desktopTestUsers{}
	h := &AuthHandler{userService: users, tenantService: &desktopTestTenants{}}
	for _, token := range []string{"", "wrong", "test-native-capability"} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest("POST", "/api/v1/auth/auto-setup", nil)
		ctx.Request.Header.Set("X-WeKnora-Desktop-Token", token)
		h.AutoSetup(ctx)
		if token != "test-native-capability" {
			require.NotEmpty(t, ctx.Errors)
			require.Zero(t, users.minted)
		} else {
			require.Empty(t, ctx.Errors)
			require.Equal(t, 1, users.minted)
		}
	}
	SetLiteSetupToken("")
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/api/v1/auth/auto-setup", nil)
	h.AutoSetup(ctx)
	require.NotEmpty(t, ctx.Errors)
	require.Equal(t, 1, users.minted)
}

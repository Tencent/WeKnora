package dto

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthLoginResponse_ViewerOmitsActiveTenantSecrets(t *testing.T) {
	tenant := sampleSecretTenant()
	resp := NewAuthLoginResponse(&types.LoginResponse{
		Success:      true,
		ActiveTenant: tenant,
		Memberships: []types.Membership{{
			TenantID: tenant.ID,
			Role:     types.TenantRoleViewer,
		}},
	})
	body, err := json.Marshal(resp)
	require.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, "tenant-api-key-123")
	assert.NotContains(t, s, "parser-secret-123")
}

func TestAuthLoginResponse_OwnerGetsAPIKey(t *testing.T) {
	tenant := sampleSecretTenant()
	resp := NewAuthLoginResponse(&types.LoginResponse{
		Success:      true,
		ActiveTenant: tenant,
		Memberships: []types.Membership{{
			TenantID: tenant.ID,
			Role:     types.TenantRoleOwner,
		}},
	})
	body, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.Contains(t, string(body), "tenant-api-key-123")
}

func TestAuthOIDCCallbackResponse_ViewerOmitsTenantSecrets(t *testing.T) {
	tenant := sampleSecretTenant()
	resp := NewAuthOIDCCallbackResponse(&types.OIDCCallbackResponse{
		Success: true,
		Tenant:  tenant,
		Memberships: []types.Membership{{
			TenantID: tenant.ID,
			Role:     types.TenantRoleViewer,
		}},
	})
	body, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "tenant-api-key-123")
}

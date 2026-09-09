package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type apiKeySharedKBList struct {
	interfaces.KBShareService
	role types.TenantRole
}

func (s *apiKeySharedKBList) ListSharedKnowledgeBases(
	_ context.Context, _ uint64, role types.TenantRole,
) ([]*types.SharedKnowledgeBaseInfo, error) {
	s.role = role
	var rows []*types.SharedKnowledgeBaseInfo
	for _, id := range []string{"read", "write", "outside"} {
		rows = append(rows, &types.SharedKnowledgeBaseInfo{
			KnowledgeBase: &types.KnowledgeBase{ID: id, TenantID: 99, Name: id},
			Permission:    types.OrgRoleAdmin, SourceTenantID: 99,
		})
	}
	return rows, nil
}

func TestSharedKBListIntersectsKeyScopeAndReportsEffectivePermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	shares := &apiKeySharedKBList{}
	handler := &OrganizationHandler{shareService: shares}
	for _, empty := range []bool{false, true} {
		grants := types.APIKeyKBPermissions{"read": types.APIKeyKBRead, "write": types.APIKeyKBWrite}
		if empty {
			grants = types.APIKeyKBPermissions{}
		}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest("GET", "/shared-knowledge-bases", nil)
		ctx := context.WithValue(req.Context(), types.TenantIDContextKey, uint64(42))
		ctx = types.WithTenantAPIKeyScope(
			ctx,
			types.TenantAPIKeyScope{
				Capabilities:             types.StringArray{"retrieve", "ingest"},
				KnowledgeBasePermissions: grants,
			},
		)
		c.Request = req.WithContext(ctx)
		handler.ListSharedKnowledgeBases(c)
		require.Empty(t, c.Errors)
		require.Equal(t, types.TenantRoleOwner, shares.role)
		var result struct {
			Data []struct {
				Permission string `json:"permission"`
			} `json:"data"`
			Total int `json:"total"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		if empty {
			require.Zero(t, result.Total)
			continue
		}
		require.Equal(t, 2, result.Total)
		require.Equal(t, "viewer", result.Data[0].Permission)
		require.Equal(t, "editor", result.Data[1].Permission)
	}
}

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type sharePathKBShares struct {
	interfaces.KBShareService
	mutated bool
}

func (*sharePathKBShares) GetShare(_ context.Context, shareID string) (*types.KnowledgeBaseShare, error) {
	if shareID != "share-1" {
		return nil, service.ErrShareNotFound
	}
	return &types.KnowledgeBaseShare{ID: "share-1", KnowledgeBaseID: "kb-owned-elsewhere"}, nil
}

func (s *sharePathKBShares) UpdateSharePermission(context.Context, string, types.OrgMemberRole, string, uint64) error {
	s.mutated = true
	return nil
}

func (s *sharePathKBShares) RemoveShare(context.Context, string, string, uint64) error {
	s.mutated = true
	return nil
}

type sharePathAgentShares struct {
	interfaces.AgentShareService
	mutated bool
}

func (*sharePathAgentShares) GetShare(context.Context, string) (*types.AgentShare, error) {
	return &types.AgentShare{ID: "share-1", AgentID: "agent-owned-elsewhere"}, nil
}

func (s *sharePathAgentShares) RemoveShare(context.Context, string, string, uint64) error {
	s.mutated = true
	return nil
}

func sharePathContext(pathID, shareID, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: pathID}, {Key: "share_id", Value: shareID}}
	c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func requireNotFoundError(t *testing.T, c *gin.Context) {
	t.Helper()
	require.Len(t, c.Errors, 1)
	appErr, ok := c.Errors[0].Err.(*apperrors.AppError)
	require.True(t, ok)
	require.Equal(t, http.StatusNotFound, appErr.HTTPCode)
}

// The share routes' ownership guard evaluates :id, so a share of another KB or
// agent must not be managed through an unrelated path.
func TestShareManagementRequiresShareOfPathResource(t *testing.T) {
	kbShares := &sharePathKBShares{}
	agentShares := &sharePathAgentShares{}
	h := &OrganizationHandler{shareService: kbShares, agentShareService: agentShares}

	c, _ := sharePathContext("kb-bogus", "share-1", `{"permission":"editor"}`)
	h.UpdateSharePermission(c)
	requireNotFoundError(t, c)

	c, _ = sharePathContext("kb-bogus", "share-1", "")
	h.RemoveShare(c)
	requireNotFoundError(t, c)

	c, _ = sharePathContext("kb-owned-elsewhere", "missing-share", "")
	h.RemoveShare(c)
	requireNotFoundError(t, c)

	c, _ = sharePathContext("agent-bogus", "share-1", "")
	h.RemoveAgentShare(c)
	requireNotFoundError(t, c)

	require.False(t, kbShares.mutated)
	require.False(t, agentShares.mutated)

	c, _ = sharePathContext("kb-owned-elsewhere", "share-1", `{"permission":"viewer"}`)
	h.UpdateSharePermission(c)
	require.Empty(t, c.Errors)
	require.True(t, kbShares.mutated)
}

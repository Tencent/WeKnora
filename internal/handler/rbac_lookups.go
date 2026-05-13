package handler

import (
	"errors"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/gin-gonic/gin"
)

// Per-resource creator-id resolvers used by middleware.RequireOwnershipOrRole.
// The route registration wires these as closures so they capture the handler's
// service dependencies; the middleware just calls them with the gin.Context
// and gets back ("", nil) for legacy tenant-owned rows or (creatorID, nil)
// for resources we know how to attribute.
//
// Lookup contract (mirrors middleware.CreatorLookup):
//   - URL param missing -> error (the middleware will 403; better than
//     letting an unauthenticated mutation through on a malformed route).
//   - Resource not found -> error (downstream handler would 404 anyway,
//     and middleware-level 403 is a fine signal to clients).
//   - Resource found but creator_id == "" -> ("", nil); the middleware
//     treats that as tenant-owned and falls back to pure role check.

// KBCreatorLookup resolves :id -> KnowledgeBase.CreatorID. Used by all
// per-KB mutating routes.
func (h *KnowledgeBaseHandler) KBCreatorLookup(c *gin.Context) (string, error) {
	id := c.Param("id")
	if id == "" {
		return "", errors.New("missing :id param for KB creator lookup")
	}
	kb, err := h.service.GetKnowledgeBaseByID(c.Request.Context(), id)
	if err != nil {
		return "", err
	}
	if kb == nil {
		return "", apprepo.ErrKnowledgeBaseNotFound
	}
	return kb.CreatorID, nil
}

// AgentCreatorLookup resolves :id -> CustomAgent.CreatedBy. Built-in
// agents (IsBuiltin == true) are tenant-owned across the board: they
// belong to the tenant rather than to any one user, so we return
// ("", nil) and let the role check decide. The same holds for legacy
// rows whose CreatedBy was never populated.
func (h *CustomAgentHandler) AgentCreatorLookup(c *gin.Context) (string, error) {
	id := c.Param("id")
	if id == "" {
		return "", errors.New("missing :id param for agent creator lookup")
	}
	agent, err := h.service.GetAgentByID(c.Request.Context(), id)
	if err != nil {
		return "", err
	}
	if agent == nil {
		return "", errors.New("agent not found")
	}
	if agent.IsBuiltin {
		return "", nil
	}
	return agent.CreatedBy, nil
}

// Compile-time guards: the methods must satisfy middleware.CreatorLookup
// so route wiring stays type-safe even if a signature drifts.
var (
	_ middleware.CreatorLookup = (*KnowledgeBaseHandler)(nil).KBCreatorLookup
	_ middleware.CreatorLookup = (*CustomAgentHandler)(nil).AgentCreatorLookup
)

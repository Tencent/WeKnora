package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MCPToolSessionPolicyRepository stores independent, tenant-scoped IM restrictions.
type MCPToolSessionPolicyRepository interface {
	ListByService(context.Context, uint64, string) ([]*types.MCPToolSessionPolicy, error)
	Upsert(context.Context, *types.MCPToolSessionPolicy) error
	Delete(context.Context, uint64, string, string) error
}

// MCPToolSessionPolicyService manages IM restrictions for accessible MCP services.
type MCPToolSessionPolicyService interface {
	ListByService(context.Context, uint64, string) ([]*types.MCPToolSessionPolicy, error)
	SetPolicy(context.Context, uint64, string, string, []types.MCPToolSessionRule) error
	DeletePolicy(context.Context, uint64, string, string) error
}

package datasource

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ConnectorScope identifies the durable owner of a connector invocation. A
// plugin runtime may be replaced at any time; the datasource ID, not a process
// ID, owns credentials and the incremental cursor.
type ConnectorScope struct {
	TenantID        uint64
	KnowledgeBaseID string
	DataSourceID    string
	OperationID     string
	TraceID         string
}

type ConnectorLease interface {
	Connector() Connector
	Close() error
}

type ConnectorFactory func(context.Context, ConnectorScope, *types.DataSourceConfig) (ConnectorLease, error)

type ConnectorResolver interface {
	Resolve(context.Context, string, ConnectorScope, *types.DataSourceConfig) (ConnectorLease, error)
}

type staticConnectorLease struct{ connector Connector }

func (l staticConnectorLease) Connector() Connector { return l.connector }
func (l staticConnectorLease) Close() error         { return nil }

type RegistryResolver struct{ registry *ConnectorRegistry }

func NewRegistryResolver(registry *ConnectorRegistry) *RegistryResolver {
	return &RegistryResolver{registry: registry}
}

func (r *RegistryResolver) Resolve(ctx context.Context, connectorType string, scope ConnectorScope, config *types.DataSourceConfig) (ConnectorLease, error) {
	return r.registry.GetForScope(ctx, connectorType, scope, config)
}

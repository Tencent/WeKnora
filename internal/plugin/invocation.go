package plugin

import (
	"context"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

func invocationFromScope(scope datasource.ConnectorScope) pluginapi.InvocationContext {
	return pluginapi.InvocationContext{
		TenantID: scope.TenantID, KnowledgeBaseID: scope.KnowledgeBaseID,
		DataSourceID: scope.DataSourceID, OperationID: scope.OperationID,
		TraceID: scope.TraceID,
	}
}

func invocationFromContext(ctx context.Context, operationID string) pluginapi.InvocationContext {
	tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
	if operationID == "" {
		if requestID, ok := types.RequestIDFromContext(ctx); ok {
			operationID = requestID
		}
	}
	if operationID == "" {
		operationID = uuid.NewString()
	}
	traceID := ""
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		traceID = spanContext.TraceID().String()
	}
	return pluginapi.InvocationContext{TenantID: tenantID, OperationID: operationID, TraceID: traceID}
}

func acquirePluginCall(ctx context.Context, manager *Manager, pluginID string, base pluginapi.InvocationContext) (context.Context, func(), uint64, error) {
	if manager == nil {
		return pluginapi.WithInvocationContext(ctx, base), func() {}, 0, nil
	}
	lease, err := manager.AcquireInvocation(ctx, pluginID)
	if err != nil {
		return nil, nil, 0, err
	}
	return pluginapi.WithInvocationContext(lease.Context, base), lease.Close, lease.Generation, nil
}

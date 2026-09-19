package pluginapi

import (
	"context"
	"strconv"

	"google.golang.org/grpc/metadata"
)

const (
	invTenantIDKey        = "x-weknora-tenant-id"
	invKnowledgeBaseIDKey = "x-weknora-knowledge-base-id"
	invDataSourceIDKey    = "x-weknora-data-source-id"
	invOperationIDKey     = "x-weknora-operation-id"
	invTraceIDKey         = "x-weknora-trace-id"
)

// InvocationContext is host-owned control-plane metadata for one plugin call.
// It is transported as gRPC metadata so v1 protobuf messages remain wire
// compatible. Plugins must treat it as read-only diagnostic context.
type InvocationContext struct {
	TenantID        uint64
	KnowledgeBaseID string
	DataSourceID    string
	OperationID     string
	TraceID         string
}

// WithInvocationContext attaches host invocation metadata to an outgoing gRPC
// call without overwriting unrelated metadata such as tracing headers.
func WithInvocationContext(ctx context.Context, inv InvocationContext) context.Context {
	pairs := []string{
		invTenantIDKey, strconv.FormatUint(inv.TenantID, 10),
		invKnowledgeBaseIDKey, inv.KnowledgeBaseID,
		invDataSourceIDKey, inv.DataSourceID,
		invOperationIDKey, inv.OperationID,
		invTraceIDKey, inv.TraceID,
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}

// InvocationContextFromContext reads metadata inside a plugin RPC handler.
func InvocationContextFromContext(ctx context.Context) InvocationContext {
	md, _ := metadata.FromIncomingContext(ctx)
	first := func(key string) string {
		values := md.Get(key)
		if len(values) == 0 {
			return ""
		}
		return values[0]
	}
	tenantID, _ := strconv.ParseUint(first(invTenantIDKey), 10, 64)
	return InvocationContext{
		TenantID: tenantID, KnowledgeBaseID: first(invKnowledgeBaseIDKey),
		DataSourceID: first(invDataSourceIDKey), OperationID: first(invOperationIDKey),
		TraceID: first(invTraceIDKey),
	}
}

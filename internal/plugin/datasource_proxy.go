package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

// GRPCConnectorProxy adapts the public external datasource contract to the
// existing WeKnora connector interface. It is intentionally stateless: the
// datasource row owns the cursor, while the runtime may be restarted freely.
type GRPCConnectorProxy struct {
	ConnectorType     string
	Client            pluginapi.DataSourcePluginClient
	ConfigSchema      map[string]any
	Invocation        pluginapi.InvocationContext
	RuntimeGeneration uint64
	RuntimeContext    context.Context
	GenerationValid   func(uint64) bool
}

// GRPCStreamingConnectorProxy is a GRPCConnectorProxy that additionally
// implements datasource.StreamingConnector (FetchStream). It is only
// constructed for plugins that declare the "streaming" capability in their
// manifest, so a unary-only plugin is never routed down the streaming sync
// path — which would otherwise drop its incremental cursor through the SDK's
// unary fallback and force a full re-sync on every manual run.
type GRPCStreamingConnectorProxy struct {
	*GRPCConnectorProxy
	StreamingClient pluginapi.DataSourceStreamingPluginClient
}

func (p *GRPCConnectorProxy) Type() string { return p.ConnectorType }

func (p *GRPCConnectorProxy) invocationContext(ctx context.Context) (context.Context, func(), error) {
	if err := p.validateGeneration(); err != nil {
		return nil, nil, err
	}
	callCtx, cancel := context.WithCancelCause(ctx)
	stop := func() bool { return true }
	if p.RuntimeContext != nil {
		stop = context.AfterFunc(p.RuntimeContext, func() { cancel(context.Cause(p.RuntimeContext)) })
	}
	cleanup := func() { stop(); cancel(nil) }
	return pluginapi.WithInvocationContext(callCtx, p.Invocation), cleanup, nil
}

func (p *GRPCConnectorProxy) validateGeneration() error {
	if p.GenerationValid != nil && !p.GenerationValid(p.RuntimeGeneration) {
		return fmt.Errorf("plugin runtime generation %d is stale", p.RuntimeGeneration)
	}
	return nil
}

func (p *GRPCConnectorProxy) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	ctx, cleanup, err := p.invocationContext(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	response, err := p.call(ctx, "Validate", config, func(r *pluginapi.Response) error { return nil })
	if err != nil {
		return err
	}
	return responseError(response)
}

func (p *GRPCConnectorProxy) ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
	ctx, cleanup, err := p.invocationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	request, err := makeRequestWithSchema(config, nil, p.ConfigSchema)
	if err != nil {
		return nil, err
	}
	request.ParentID = parentID
	wire, err := pluginapi.EncodeRequest(request)
	if err != nil {
		return nil, err
	}
	out, err := p.Client.ListResources(ctx, wire)
	if err != nil {
		return nil, err
	}
	if err := p.validateGeneration(); err != nil {
		return nil, err
	}
	var response pluginapi.Response
	if err := pluginapi.DecodeResponse(out, &response); err != nil {
		return nil, err
	}
	if err := responseError(&response); err != nil {
		return nil, err
	}
	resources := make([]types.Resource, 0, len(response.Resources))
	for _, resource := range response.Resources {
		metadata := make(map[string]interface{}, len(resource.Metadata))
		for key, value := range resource.Metadata {
			metadata[key] = value
		}
		resources = append(resources, types.Resource{ExternalID: resource.ExternalID, Name: resource.Name, ParentID: resource.ParentID, Type: resource.Type, Metadata: metadata})
	}
	return resources, nil
}

func (p *GRPCConnectorProxy) ResolveResourceAncestors(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]string, error) {
	ctx, cleanup, err := p.invocationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	request, err := makeRequestWithSchema(config, nil, p.ConfigSchema)
	if err != nil {
		return nil, err
	}
	request.ResourceIDs = resourceIDs
	wire, err := pluginapi.EncodeRequest(request)
	if err != nil {
		return nil, err
	}
	out, err := p.Client.ResolveResourceAncestors(ctx, wire)
	if err != nil {
		return nil, err
	}
	if err := p.validateGeneration(); err != nil {
		return nil, err
	}
	var response pluginapi.Response
	if err := pluginapi.DecodeResponse(out, &response); err != nil {
		return nil, err
	}
	if err := responseError(&response); err != nil {
		return nil, err
	}
	return response.Ancestors, nil
}

func (p *GRPCConnectorProxy) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	ctx, cleanup, err := p.invocationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	request, err := makeRequestWithSchema(config, nil, p.ConfigSchema)
	if err != nil {
		return nil, err
	}
	request.ResourceIDs = resourceIDs
	return p.fetch(ctx, "FetchAll", request)
}

func (p *GRPCConnectorProxy) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	ctx, cleanup, err := p.invocationContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	request, err := makeRequestWithSchema(config, cursor, p.ConfigSchema)
	if err != nil {
		return nil, nil, err
	}
	wire, err := pluginapi.EncodeRequest(request)
	if err != nil {
		return nil, nil, err
	}
	out, err := p.Client.FetchIncremental(ctx, wire)
	if err != nil {
		return nil, nil, err
	}
	if err := p.validateGeneration(); err != nil {
		return nil, nil, err
	}
	var response pluginapi.Response
	if err := pluginapi.DecodeResponse(out, &response); err != nil {
		return nil, nil, err
	}
	if err := responseError(&response); err != nil {
		return nil, nil, err
	}
	items, err := fromWireItems(response.Items)
	if err != nil {
		return nil, nil, err
	}
	var next *types.SyncCursor
	if response.Cursor != nil {
		next = &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: response.Cursor}
	}
	return items, next, nil
}

// FetchStream adapts the optional gRPC server-streaming contract to the
// existing WeKnora StreamHandler. Each response cursor is a durable progress
// boundary. Unary-only plugins remain supported through a bounded fallback
// that emits all returned items and checkpoints once at the end.
func (p *GRPCStreamingConnectorProxy) FetchStream(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler) (*types.SyncCursor, error) {
	ctx, cleanup, err := p.invocationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	request, err := makeRequestWithSchema(config, cursor, p.ConfigSchema)
	if err != nil {
		return nil, err
	}
	if p.StreamingClient == nil {
		items, next, err := p.fetchUnaryStreamFallback(ctx, config, cursor)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if err := h.Emit(ctx, item); err != nil {
				return nil, err
			}
		}
		if next != nil {
			if err := h.Checkpoint(ctx, next); err != nil {
				return nil, err
			}
		}
		return next, nil
	}
	wire, err := pluginapi.EncodeRequest(request)
	if err != nil {
		return nil, err
	}
	var stream pluginapi.DataSourceResponseClient
	if cursor == nil {
		stream, err = p.StreamingClient.FetchAllStream(ctx, wire)
	} else {
		stream, err = p.StreamingClient.FetchIncrementalStream(ctx, wire)
	}
	if err != nil {
		return nil, err
	}
	var next *types.SyncCursor
	for {
		if p.GenerationValid != nil && !p.GenerationValid(p.RuntimeGeneration) {
			return nil, fmt.Errorf("plugin runtime generation %d became stale", p.RuntimeGeneration)
		}
		out, recvErr := stream.Recv()
		if recvErr == io.EOF {
			if err := p.validateGeneration(); err != nil {
				return nil, err
			}
			break
		}
		if recvErr != nil {
			return nil, recvErr
		}
		var response pluginapi.Response
		if err := pluginapi.DecodeResponse(out, &response); err != nil {
			return nil, err
		}
		if err := responseError(&response); err != nil {
			return nil, err
		}
		items, err := fromWireItems(response.Items)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if err := p.validateGeneration(); err != nil {
				return nil, err
			}
			if err := h.Emit(ctx, item); err != nil {
				return nil, err
			}
		}
		if response.Cursor != nil {
			if err := p.validateGeneration(); err != nil {
				return nil, err
			}
			next = &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: response.Cursor}
			if err := h.Checkpoint(ctx, next); err != nil {
				return nil, err
			}
		}
	}
	return next, nil
}

func (p *GRPCStreamingConnectorProxy) fetchUnaryStreamFallback(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	if cursor == nil {
		items, err := p.FetchAll(ctx, config, nil)
		return items, nil, err
	}
	items, next, err := p.FetchIncremental(ctx, config, cursor)
	return items, next, err
}

func (p *GRPCConnectorProxy) fetch(ctx context.Context, method string, request pluginapi.Request) ([]types.FetchedItem, error) {
	wire, err := pluginapi.EncodeRequest(request)
	if err != nil {
		return nil, err
	}
	if err := p.validateGeneration(); err != nil {
		return nil, err
	}
	var out *pluginproto.DataSourceResponse
	switch method {
	case "FetchAll":
		out, err = p.Client.FetchAll(ctx, wire)
	default:
		return nil, fmt.Errorf("unsupported fetch method %q", method)
	}
	if err != nil {
		return nil, err
	}
	if err := p.validateGeneration(); err != nil {
		return nil, err
	}
	var response pluginapi.Response
	if err := pluginapi.DecodeResponse(out, &response); err != nil {
		return nil, err
	}
	if err := responseError(&response); err != nil {
		return nil, err
	}
	return fromWireItems(response.Items)
}

func (p *GRPCConnectorProxy) call(ctx context.Context, method string, config *types.DataSourceConfig, decode func(*pluginapi.Response) error) (*pluginapi.Response, error) {
	request, err := makeRequestWithSchema(config, nil, p.ConfigSchema)
	if err != nil {
		return nil, err
	}
	wire, err := pluginapi.EncodeRequest(request)
	if err != nil {
		return nil, err
	}
	var out *pluginproto.DataSourceResponse
	switch method {
	case "Validate":
		out, err = p.Client.Validate(ctx, wire)
	default:
		return nil, fmt.Errorf("unsupported method %q", method)
	}
	if err != nil {
		return nil, err
	}
	if err := p.validateGeneration(); err != nil {
		return nil, err
	}
	response := new(pluginapi.Response)
	if err := pluginapi.DecodeResponse(out, response); err != nil {
		return nil, err
	}
	if err := decode(response); err != nil {
		return nil, err
	}
	return response, nil
}

func makeRequestWithSchema(config *types.DataSourceConfig, cursor *types.SyncCursor, schema map[string]any) (pluginapi.Request, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return pluginapi.Request{}, err
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return pluginapi.Request{}, err
	}
	if err := ValidateConfigSchema(schema, fields); err != nil {
		return pluginapi.Request{}, err
	}
	request := pluginapi.Request{Config: fields}
	if cursor != nil {
		request.Cursor = cursor.ConnectorCursor
	}
	return request, nil
}

func fromWireItems(items []pluginapi.FetchedItem) ([]types.FetchedItem, error) {
	result := make([]types.FetchedItem, 0, len(items))
	for _, item := range items {
		var updatedAt time.Time
		if item.UpdatedAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, item.UpdatedAt)
			if err != nil {
				return nil, fmt.Errorf("plugin item %q has invalid updated_at %q: %w", item.ExternalID, item.UpdatedAt, err)
			}
			updatedAt = parsed
		}
		// FileName is the portable filename (with extension) used by the ingest
		// pipeline to select the correct parser. The wire item now carries a
		// dedicated file_name field; fall back to Title for plugins built
		// against an older SDK that does not populate it.
		fileName := item.FileName
		if fileName == "" {
			fileName = item.Title
		}
		result = append(result, types.FetchedItem{ExternalID: item.ExternalID, Title: item.Title, Content: item.Content, FileName: fileName, URL: item.URL, ContentType: item.MIMEType, Metadata: item.Metadata, IsDeleted: item.IsDeleted, ReplacesSubtree: item.ReplacesSubtree, SubtreeKeep: item.SubtreeKeep, UpdatedAt: updatedAt})
	}
	return result, nil
}

func responseError(response *pluginapi.Response) error {
	if response != nil && response.Error != "" {
		return fmt.Errorf("plugin: %s", response.Error)
	}
	return nil
}

var _ datasource.Connector = (*GRPCConnectorProxy)(nil)

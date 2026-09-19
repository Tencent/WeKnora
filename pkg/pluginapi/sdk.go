package pluginapi

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/grpc"
)

// maxPluginMsgSize is the default gRPC message limit for plugin servers. It
// matches the host's docreader limit (50MB) so image-heavy responses fit.
const maxPluginMsgSize = 50 * 1024 * 1024

// gracefulShutdownTimeout bounds how long GracefulStop may wait for in-flight
// RPCs after a shutdown trigger, before the server is force-stopped.
const gracefulShutdownTimeout = 5 * time.Second

type DataSourceHandler struct {
	PluginID                   string
	Capabilities               []string
	OnValidate                 func(context.Context, Request) error
	OnListResources            func(context.Context, Request) ([]Resource, error)
	OnResolveResourceAncestors func(context.Context, Request) ([]string, error)
	OnFetchAll                 func(context.Context, Request) ([]FetchedItem, error)
	OnFetchIncremental         func(context.Context, Request) ([]FetchedItem, map[string]any, error)
	// Streaming callbacks should emit bounded Response values. A response may
	// include items and, at page boundaries, a Cursor checkpoint.
	OnFetchAllStream         func(context.Context, Request, func(Response) error) error
	OnFetchIncrementalStream func(context.Context, Request, func(Response) error) error
	OnHealth                 func(context.Context) HealthResponse
}

func (h DataSourceHandler) Handshake(context.Context, *pluginproto.HandshakeRequest) (*pluginproto.HandshakeResponse, error) {
	return EncodeHandshake(HandshakeResponse{ProtocolVersion: ProtocolVersionV1, PluginID: h.PluginID, Capabilities: h.Capabilities, ExtensionType: ExtensionTypeDataSource, Services: []string{ExtensionTypeDataSource}}), nil
}
func (h DataSourceHandler) Health(ctx context.Context, _ *pluginproto.HealthRequest) (*pluginproto.HealthResponse, error) {
	v := HealthResponse{State: "running"}
	if h.OnHealth != nil {
		v = h.OnHealth(ctx)
	}
	return EncodeHealth(v), nil
}
func (h DataSourceHandler) Validate(ctx context.Context, v *pluginproto.DataSourceRequest) (*pluginproto.DataSourceResponse, error) {
	req, err := requestFromProto(v)
	if err == nil && h.OnValidate != nil {
		err = h.OnValidate(ctx, req)
	}
	return EncodeResponse(Response{Error: errorString(err)})
}
func (h DataSourceHandler) ListResources(ctx context.Context, v *pluginproto.DataSourceRequest) (*pluginproto.DataSourceResponse, error) {
	req, err := requestFromProto(v)
	var values []Resource
	if err == nil && h.OnListResources != nil {
		values, err = h.OnListResources(ctx, req)
	}
	return EncodeResponse(Response{Resources: values, Error: errorString(err)})
}
func (h DataSourceHandler) ResolveResourceAncestors(ctx context.Context, v *pluginproto.DataSourceRequest) (*pluginproto.DataSourceResponse, error) {
	req, err := requestFromProto(v)
	var values []string
	if err == nil && h.OnResolveResourceAncestors != nil {
		values, err = h.OnResolveResourceAncestors(ctx, req)
	}
	return EncodeResponse(Response{Ancestors: values, Error: errorString(err)})
}
func (h DataSourceHandler) FetchAll(ctx context.Context, v *pluginproto.DataSourceRequest) (*pluginproto.DataSourceResponse, error) {
	req, err := requestFromProto(v)
	var values []FetchedItem
	if err == nil && h.OnFetchAll != nil {
		values, err = h.OnFetchAll(ctx, req)
	}
	return EncodeResponse(Response{Items: values, Error: errorString(err)})
}
func (h DataSourceHandler) FetchIncremental(ctx context.Context, v *pluginproto.DataSourceRequest) (*pluginproto.DataSourceResponse, error) {
	req, err := requestFromProto(v)
	var values []FetchedItem
	var cursor map[string]any
	if err == nil && h.OnFetchIncremental != nil {
		values, cursor, err = h.OnFetchIncremental(ctx, req)
	}
	return EncodeResponse(Response{Items: values, Cursor: cursor, Error: errorString(err)})
}

func (h DataSourceHandler) FetchAllStream(ctx context.Context, v *pluginproto.DataSourceRequest, stream DataSourceResponseServer) error {
	return h.fetchStream(ctx, v, stream, h.OnFetchAllStream, h.OnFetchAll)
}

func (h DataSourceHandler) FetchIncrementalStream(ctx context.Context, v *pluginproto.DataSourceRequest, stream DataSourceResponseServer) error {
	if h.OnFetchIncrementalStream != nil {
		return h.fetchStream(ctx, v, stream, h.OnFetchIncrementalStream, nil)
	}
	// Unary fallback keeps a template or an older plugin usable when the host
	// calls the optional streaming endpoint during a migration.
	if h.OnFetchIncremental == nil {
		return fmt.Errorf("streaming incremental callback is not configured")
	}
	return h.fetchStream(ctx, v, stream, nil, func(ctx context.Context, req Request) ([]FetchedItem, error) {
		items, _, err := h.OnFetchIncremental(ctx, req)
		return items, err
	})
}

func (h DataSourceHandler) fetchStream(ctx context.Context, v *pluginproto.DataSourceRequest, stream DataSourceResponseServer, callback func(context.Context, Request, func(Response) error) error, unary func(context.Context, Request) ([]FetchedItem, error)) error {
	req, err := requestFromProto(v)
	if err != nil {
		return err
	}
	emit := func(response Response) error {
		wire, err := EncodeResponse(response)
		if err != nil {
			return err
		}
		return stream.Send(wire)
	}
	if callback != nil {
		return callback(ctx, req, emit)
	}
	if unary == nil {
		return fmt.Errorf("streaming datasource callback is not configured")
	}
	items, err := unary(ctx, req)
	return emit(Response{Items: items, Error: errorString(err)})
}
func requestFromProto(v *pluginproto.DataSourceRequest) (Request, error) {
	var out Request
	return out, DecodeRequest(v, &out)
}
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func Serve(ctx context.Context, address string, handler DataSourceHandler, opts ...grpc.ServerOption) error {
	return servePlugin(ctx, address, opts, func(s *grpc.Server) {
		RegisterPluginControlServer(s, handler)
		RegisterDataSourcePluginServer(s, handler)
	})
}
func servePlugin(ctx context.Context, address string, opts []grpc.ServerOption, register func(*grpc.Server)) error {
	network, listenAddress := "tcp", address
	if strings.HasPrefix(address, "unix://") {
		network, listenAddress = "unix", strings.TrimPrefix(address, "unix://")
	}
	listener, err := net.Listen(network, listenAddress)
	if err != nil {
		return fmt.Errorf("listen plugin: %w", err)
	}
	// A Unix control socket must be connectable by the non-root app user in the
	// shared runtime group; 0660 lets the group (set via the setgid directory)
	// connect without opening the socket to everyone.
	if network == "unix" {
		if err := os.Chmod(listenAddress, 0o660); err != nil {
			_ = listener.Close()
			return fmt.Errorf("chmod plugin socket: %w", err)
		}
	}
	// Raise the gRPC message limits so a parser/data-source plugin can return
	// documents with embedded images without hitting the 4MB default. The
	// defaults are prepended so callers that pass an explicit limit still win.
	opts = append(
		[]grpc.ServerOption{grpc.MaxRecvMsgSize(maxPluginMsgSize), grpc.MaxSendMsgSize(maxPluginMsgSize)},
		opts...,
	)
	server := grpc.NewServer(opts...)
	register(server)

	// Shut down on either trigger: the host cancelling its context, or the
	// process receiving a termination signal. Plugins typically serve with
	// context.Background, so the signal path is what makes graceful stops
	// work in practice. GracefulStop waits for in-flight RPCs — handlers see
	// cancellation as soon as the host closes the connection — and the
	// force-stop bounds the wait for handlers that ignore cancellation.
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	go func() {
		select {
		case <-ctx.Done():
		case <-sigCtx.Done():
		}
		done := make(chan struct{})
		go func() { server.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-time.After(gracefulShutdownTimeout):
			server.Stop()
		}
	}()
	return server.Serve(listener)
}

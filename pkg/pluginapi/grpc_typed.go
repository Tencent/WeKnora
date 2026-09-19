package pluginapi

import (
	"context"
	proto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	dataSourceService   = "weknora.plugin.v1.DataSourcePlugin"
	parserService       = "weknora.plugin.v1.ParserPlugin"
	webSearchService    = "weknora.plugin.v1.WebSearchPlugin"
	pluginControlService = "weknora.plugin.v1.PluginControl"
)

// PluginControlClient is the shared control-plane client. Runtimes depend only
// on this interface, never on type-specific clients such as
// DataSourcePluginClient. Handshake and Health are uniform across datasource,
// parser, search, model and retriever plugins.
type PluginControlClient interface {
	Handshake(context.Context, *proto.HandshakeRequest, ...grpc.CallOption) (*proto.HandshakeResponse, error)
	Health(context.Context, *proto.HealthRequest, ...grpc.CallOption) (*proto.HealthResponse, error)
}

type PluginControlServer interface {
	Handshake(context.Context, *proto.HandshakeRequest) (*proto.HandshakeResponse, error)
	Health(context.Context, *proto.HealthRequest) (*proto.HealthResponse, error)
}

type pluginControlClient struct{ cc grpc.ClientConnInterface }

func NewPluginControlClient(cc grpc.ClientConnInterface) PluginControlClient {
	return &pluginControlClient{cc: cc}
}

func (c *pluginControlClient) Handshake(ctx context.Context, in *proto.HandshakeRequest, opts ...grpc.CallOption) (*proto.HandshakeResponse, error) {
	out := new(proto.HandshakeResponse)
	err := c.cc.Invoke(ctx, "/"+pluginControlService+"/Handshake", in, out, opts...)
	return out, err
}

func (c *pluginControlClient) Health(ctx context.Context, in *proto.HealthRequest, opts ...grpc.CallOption) (*proto.HealthResponse, error) {
	out := new(proto.HealthResponse)
	err := c.cc.Invoke(ctx, "/"+pluginControlService+"/Health", in, out, opts...)
	return out, err
}

func RegisterPluginControlServer(r grpc.ServiceRegistrar, s PluginControlServer) {
	r.RegisterService(&pluginControlServiceDesc, s)
}

var pluginControlServiceDesc = grpc.ServiceDesc{
	ServiceName: pluginControlService,
	HandlerType: (*PluginControlServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Handshake", Handler: controlHandler(func(s PluginControlServer, c context.Context, in *proto.HandshakeRequest) (any, error) {
			return s.Handshake(c, in)
		}, func() *proto.HandshakeRequest { return &proto.HandshakeRequest{} })},
		{MethodName: "Health", Handler: controlHandler(func(s PluginControlServer, c context.Context, in *proto.HealthRequest) (any, error) {
			return s.Health(c, in)
		}, func() *proto.HealthRequest { return &proto.HealthRequest{} })},
	},
}

func controlHandler[T any](call func(PluginControlServer, context.Context, T) (any, error), newReq func() T) grpc.MethodHandler {
	return func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := newReq()
		if err := dec(in); err != nil {
			return nil, err
		}
		s := srv.(PluginControlServer)
		if interceptor == nil {
			return call(s, ctx, in)
		}
		info := &grpc.UnaryServerInfo{Server: srv}
		h := func(ctx context.Context, req any) (any, error) { return call(s, ctx, req.(T)) }
		return interceptor(ctx, in, info, h)
	}
}

type DataSourcePluginServer interface {
	Validate(context.Context, *proto.DataSourceRequest) (*proto.DataSourceResponse, error)
	ListResources(context.Context, *proto.DataSourceRequest) (*proto.DataSourceResponse, error)
	ResolveResourceAncestors(context.Context, *proto.DataSourceRequest) (*proto.DataSourceResponse, error)
	FetchAll(context.Context, *proto.DataSourceRequest) (*proto.DataSourceResponse, error)
	FetchIncremental(context.Context, *proto.DataSourceRequest) (*proto.DataSourceResponse, error)
}

type DataSourcePluginClient interface {
	Validate(context.Context, *proto.DataSourceRequest, ...grpc.CallOption) (*proto.DataSourceResponse, error)
	ListResources(context.Context, *proto.DataSourceRequest, ...grpc.CallOption) (*proto.DataSourceResponse, error)
	ResolveResourceAncestors(context.Context, *proto.DataSourceRequest, ...grpc.CallOption) (*proto.DataSourceResponse, error)
	FetchAll(context.Context, *proto.DataSourceRequest, ...grpc.CallOption) (*proto.DataSourceResponse, error)
	FetchIncremental(context.Context, *proto.DataSourceRequest, ...grpc.CallOption) (*proto.DataSourceResponse, error)
}

// DataSourceStreamingPluginServer is optional. Keeping streaming separate
// lets existing unary-only plugins continue to compile and run unchanged.
type DataSourceStreamingPluginServer interface {
	FetchAllStream(context.Context, *proto.DataSourceRequest, DataSourceResponseServer) error
	FetchIncrementalStream(context.Context, *proto.DataSourceRequest, DataSourceResponseServer) error
}

type DataSourceResponseServer interface {
	Send(*proto.DataSourceResponse) error
}

type DataSourceStreamingPluginClient interface {
	FetchAllStream(context.Context, *proto.DataSourceRequest, ...grpc.CallOption) (DataSourceResponseClient, error)
	FetchIncrementalStream(context.Context, *proto.DataSourceRequest, ...grpc.CallOption) (DataSourceResponseClient, error)
}

type DataSourceResponseClient interface {
	Recv() (*proto.DataSourceResponse, error)
}

type dataSourceResponseClient struct{ grpc.ClientStream }

func (s dataSourceResponseClient) Recv() (*proto.DataSourceResponse, error) {
	response := new(proto.DataSourceResponse)
	if err := s.RecvMsg(response); err != nil {
		return nil, err
	}
	return response, nil
}

type dataSourcePluginClient struct{ cc grpc.ClientConnInterface }

func NewDataSourcePluginClient(cc grpc.ClientConnInterface) DataSourcePluginClient {
	return &dataSourcePluginClient{cc: cc}
}
func (c *dataSourcePluginClient) Validate(ctx context.Context, in *proto.DataSourceRequest, opts ...grpc.CallOption) (*proto.DataSourceResponse, error) {
	out := new(proto.DataSourceResponse)
	err := c.cc.Invoke(ctx, "/"+dataSourceService+"/Validate", in, out, opts...)
	return out, err
}
func (c *dataSourcePluginClient) ListResources(ctx context.Context, in *proto.DataSourceRequest, opts ...grpc.CallOption) (*proto.DataSourceResponse, error) {
	out := new(proto.DataSourceResponse)
	err := c.cc.Invoke(ctx, "/"+dataSourceService+"/ListResources", in, out, opts...)
	return out, err
}
func (c *dataSourcePluginClient) ResolveResourceAncestors(ctx context.Context, in *proto.DataSourceRequest, opts ...grpc.CallOption) (*proto.DataSourceResponse, error) {
	out := new(proto.DataSourceResponse)
	err := c.cc.Invoke(ctx, "/"+dataSourceService+"/ResolveResourceAncestors", in, out, opts...)
	return out, err
}
func (c *dataSourcePluginClient) FetchAll(ctx context.Context, in *proto.DataSourceRequest, opts ...grpc.CallOption) (*proto.DataSourceResponse, error) {
	out := new(proto.DataSourceResponse)
	err := c.cc.Invoke(ctx, "/"+dataSourceService+"/FetchAll", in, out, opts...)
	return out, err
}
func (c *dataSourcePluginClient) FetchIncremental(ctx context.Context, in *proto.DataSourceRequest, opts ...grpc.CallOption) (*proto.DataSourceResponse, error) {
	out := new(proto.DataSourceResponse)
	err := c.cc.Invoke(ctx, "/"+dataSourceService+"/FetchIncremental", in, out, opts...)
	return out, err
}

func (c *dataSourcePluginClient) FetchAllStream(ctx context.Context, in *proto.DataSourceRequest, opts ...grpc.CallOption) (DataSourceResponseClient, error) {
	stream, err := c.cc.NewStream(ctx, &dataSourceServiceDesc.Streams[0], "/"+dataSourceService+"/FetchAllStream", opts...)
	if err != nil {
		return nil, err
	}
	if err := stream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	return dataSourceResponseClient{ClientStream: stream}, nil
}

func (c *dataSourcePluginClient) FetchIncrementalStream(ctx context.Context, in *proto.DataSourceRequest, opts ...grpc.CallOption) (DataSourceResponseClient, error) {
	stream, err := c.cc.NewStream(ctx, &dataSourceServiceDesc.Streams[1], "/"+dataSourceService+"/FetchIncrementalStream", opts...)
	if err != nil {
		return nil, err
	}
	if err := stream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	return dataSourceResponseClient{ClientStream: stream}, nil
}
func RegisterDataSourcePluginServer(r grpc.ServiceRegistrar, s DataSourcePluginServer) {
	r.RegisterService(&dataSourceServiceDesc, s)
}

func dataSourceHandler[T any](decode func(any) error, call func(DataSourcePluginServer, context.Context, T) (any, error), newReq func() T) grpc.MethodHandler {
	return func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := newReq()
		if err := dec(in); err != nil {
			return nil, err
		}
		s := srv.(DataSourcePluginServer)
		if interceptor == nil {
			return call(s, ctx, in)
		}
		info := &grpc.UnaryServerInfo{Server: srv}
		h := func(ctx context.Context, req any) (any, error) { return call(s, ctx, req.(T)) }
		return interceptor(ctx, in, info, h)
	}
}

var dataSourceServiceDesc = grpc.ServiceDesc{ServiceName: dataSourceService, HandlerType: (*DataSourcePluginServer)(nil), Methods: []grpc.MethodDesc{
	{MethodName: "Validate", Handler: dataSourceHandler(func(any) error { return nil }, func(s DataSourcePluginServer, c context.Context, in *proto.DataSourceRequest) (any, error) {
		return s.Validate(c, in)
	}, func() *proto.DataSourceRequest { return &proto.DataSourceRequest{} })},
	{MethodName: "ListResources", Handler: dataSourceHandler(func(any) error { return nil }, func(s DataSourcePluginServer, c context.Context, in *proto.DataSourceRequest) (any, error) {
		return s.ListResources(c, in)
	}, func() *proto.DataSourceRequest { return &proto.DataSourceRequest{} })},
	{MethodName: "ResolveResourceAncestors", Handler: dataSourceHandler(func(any) error { return nil }, func(s DataSourcePluginServer, c context.Context, in *proto.DataSourceRequest) (any, error) {
		return s.ResolveResourceAncestors(c, in)
	}, func() *proto.DataSourceRequest { return &proto.DataSourceRequest{} })},
	{MethodName: "FetchAll", Handler: dataSourceHandler(func(any) error { return nil }, func(s DataSourcePluginServer, c context.Context, in *proto.DataSourceRequest) (any, error) {
		return s.FetchAll(c, in)
	}, func() *proto.DataSourceRequest { return &proto.DataSourceRequest{} })},
	{MethodName: "FetchIncremental", Handler: dataSourceHandler(func(any) error { return nil }, func(s DataSourcePluginServer, c context.Context, in *proto.DataSourceRequest) (any, error) {
		return s.FetchIncremental(c, in)
	}, func() *proto.DataSourceRequest { return &proto.DataSourceRequest{} })},
}, Streams: []grpc.StreamDesc{
	{StreamName: "FetchAllStream", ServerStreams: true, Handler: dataSourceStreamHandler(func(s DataSourceStreamingPluginServer, c context.Context, in *proto.DataSourceRequest, stream DataSourceResponseServer) error {
		return s.FetchAllStream(c, in, stream)
	})},
	{StreamName: "FetchIncrementalStream", ServerStreams: true, Handler: dataSourceStreamHandler(func(s DataSourceStreamingPluginServer, c context.Context, in *proto.DataSourceRequest, stream DataSourceResponseServer) error {
		return s.FetchIncrementalStream(c, in, stream)
	})},
}}

func dataSourceStreamHandler(call func(DataSourceStreamingPluginServer, context.Context, *proto.DataSourceRequest, DataSourceResponseServer) error) grpc.StreamHandler {
	return func(srv any, stream grpc.ServerStream) error {
		in := new(proto.DataSourceRequest)
		if err := stream.RecvMsg(in); err != nil {
			return err
		}
		server, ok := srv.(DataSourceStreamingPluginServer)
		if !ok {
			return status.Error(codes.Unimplemented, "streaming datasource RPC is not implemented")
		}
		return call(server, stream.Context(), in, dataSourceResponseServer{stream})
	}
}

type dataSourceResponseServer struct{ grpc.ServerStream }

func (s dataSourceResponseServer) Send(response *proto.DataSourceResponse) error {
	return s.SendMsg(response)
}

type ParserPluginServer interface {
	Parse(context.Context, *proto.ParserRequest) (*proto.ParserResponse, error)
}
type ParserPluginClient interface {
	Parse(context.Context, *proto.ParserRequest, ...grpc.CallOption) (*proto.ParserResponse, error)
}
type parserPluginClient struct{ cc grpc.ClientConnInterface }

func NewParserPluginClient(cc grpc.ClientConnInterface) ParserPluginClient {
	return &parserPluginClient{cc: cc}
}
func (c *parserPluginClient) Parse(ctx context.Context, in *proto.ParserRequest, o ...grpc.CallOption) (*proto.ParserResponse, error) {
	out := new(proto.ParserResponse)
	err := c.cc.Invoke(ctx, "/"+parserService+"/Parse", in, out, o...)
	return out, err
}
func RegisterParserPluginServer(r grpc.ServiceRegistrar, s ParserPluginServer) {
	r.RegisterService(&parserServiceDesc, s)
}

var parserServiceDesc = grpc.ServiceDesc{ServiceName: parserService, HandlerType: (*ParserPluginServer)(nil), Methods: []grpc.MethodDesc{
	{MethodName: "Parse", Handler: parserHandler(func(s ParserPluginServer, c context.Context, in *proto.ParserRequest) (any, error) {
		return s.Parse(c, in)
	}, func() *proto.ParserRequest { return &proto.ParserRequest{} })},
}}

func parserHandler[T any](call func(ParserPluginServer, context.Context, T) (any, error), newReq func() T) grpc.MethodHandler {
	return func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := newReq()
		if err := dec(in); err != nil {
			return nil, err
		}
		s := srv.(ParserPluginServer)
		if interceptor == nil {
			return call(s, ctx, in)
		}
		info := &grpc.UnaryServerInfo{Server: srv}
		h := func(ctx context.Context, req any) (any, error) { return call(s, ctx, req.(T)) }
		return interceptor(ctx, in, info, h)
	}
}

type WebSearchPluginServer interface {
	Search(context.Context, *proto.WebSearchRequest) (*proto.WebSearchResponse, error)
}
type WebSearchPluginClient interface {
	Search(context.Context, *proto.WebSearchRequest, ...grpc.CallOption) (*proto.WebSearchResponse, error)
}
type webSearchPluginClient struct{ cc grpc.ClientConnInterface }

func NewWebSearchPluginClient(cc grpc.ClientConnInterface) WebSearchPluginClient {
	return &webSearchPluginClient{cc: cc}
}
func (c *webSearchPluginClient) Search(ctx context.Context, in *proto.WebSearchRequest, o ...grpc.CallOption) (*proto.WebSearchResponse, error) {
	out := new(proto.WebSearchResponse)
	err := c.cc.Invoke(ctx, "/"+webSearchService+"/Search", in, out, o...)
	return out, err
}
func RegisterWebSearchPluginServer(r grpc.ServiceRegistrar, s WebSearchPluginServer) {
	r.RegisterService(&webSearchServiceDesc, s)
}

var webSearchServiceDesc = grpc.ServiceDesc{ServiceName: webSearchService, HandlerType: (*WebSearchPluginServer)(nil), Methods: []grpc.MethodDesc{
	{MethodName: "Search", Handler: searchHandler(func(s WebSearchPluginServer, c context.Context, in *proto.WebSearchRequest) (any, error) {
		return s.Search(c, in)
	}, func() *proto.WebSearchRequest { return &proto.WebSearchRequest{} })},
}}

func searchHandler[T any](call func(WebSearchPluginServer, context.Context, T) (any, error), newReq func() T) grpc.MethodHandler {
	return func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := newReq()
		if err := dec(in); err != nil {
			return nil, err
		}
		s := srv.(WebSearchPluginServer)
		if interceptor == nil {
			return call(s, ctx, in)
		}
		info := &grpc.UnaryServerInfo{Server: srv}
		h := func(ctx context.Context, req any) (any, error) { return call(s, ctx, req.(T)) }
		return interceptor(ctx, in, info, h)
	}
}

const modelService = "weknora.plugin.v1.ModelPlugin"

type ModelPluginServer interface {
	ValidateConfig(context.Context, *proto.ModelValidateRequest) (*proto.ModelValidateResponse, error)
	ModelInfo(context.Context, *proto.ModelInfoRequest) (*proto.ModelInfoResponse, error)
	Chat(context.Context, *proto.ModelChatRequest) (*proto.ModelChatResponse, error)
	ChatStream(context.Context, *proto.ModelChatRequest, ModelStreamResponseServer) error
	Embed(context.Context, *proto.ModelEmbedRequest) (*proto.ModelEmbedResponse, error)
	BatchEmbed(context.Context, *proto.ModelBatchEmbedRequest) (*proto.ModelBatchEmbedResponse, error)
	Rerank(context.Context, *proto.ModelRerankRequest) (*proto.ModelRerankResponse, error)
	PredictVLM(context.Context, *proto.ModelVLMRequest) (*proto.ModelVLMResponse, error)
	Transcribe(context.Context, *proto.ModelASRRequest) (*proto.ModelASRResponse, error)
}

type ModelPluginClient interface {
	ValidateConfig(context.Context, *proto.ModelValidateRequest, ...grpc.CallOption) (*proto.ModelValidateResponse, error)
	ModelInfo(context.Context, *proto.ModelInfoRequest, ...grpc.CallOption) (*proto.ModelInfoResponse, error)
	Chat(context.Context, *proto.ModelChatRequest, ...grpc.CallOption) (*proto.ModelChatResponse, error)
	ChatStream(context.Context, *proto.ModelChatRequest, ...grpc.CallOption) (ModelStreamResponseClient, error)
	Embed(context.Context, *proto.ModelEmbedRequest, ...grpc.CallOption) (*proto.ModelEmbedResponse, error)
	BatchEmbed(context.Context, *proto.ModelBatchEmbedRequest, ...grpc.CallOption) (*proto.ModelBatchEmbedResponse, error)
	Rerank(context.Context, *proto.ModelRerankRequest, ...grpc.CallOption) (*proto.ModelRerankResponse, error)
	PredictVLM(context.Context, *proto.ModelVLMRequest, ...grpc.CallOption) (*proto.ModelVLMResponse, error)
	Transcribe(context.Context, *proto.ModelASRRequest, ...grpc.CallOption) (*proto.ModelASRResponse, error)
}

type ModelStreamResponseServer interface {
	Send(*proto.ModelStreamResponse) error
}
type ModelStreamResponseClient interface {
	Recv() (*proto.ModelStreamResponse, error)
}

type modelStreamResponseClient struct{ grpc.ClientStream }

func (s modelStreamResponseClient) Recv() (*proto.ModelStreamResponse, error) {
	r := new(proto.ModelStreamResponse)
	if err := s.RecvMsg(r); err != nil {
		return nil, err
	}
	return r, nil
}

type modelStreamResponseServer struct{ grpc.ServerStream }

func (s modelStreamResponseServer) Send(r *proto.ModelStreamResponse) error { return s.SendMsg(r) }

type modelPluginClient struct{ cc grpc.ClientConnInterface }

func NewModelPluginClient(cc grpc.ClientConnInterface) ModelPluginClient { return &modelPluginClient{cc: cc} }

func (c *modelPluginClient) ValidateConfig(ctx context.Context, in *proto.ModelValidateRequest, opts ...grpc.CallOption) (*proto.ModelValidateResponse, error) {
	out := new(proto.ModelValidateResponse)
	return out, c.cc.Invoke(ctx, "/"+modelService+"/ValidateConfig", in, out, opts...)
}
func (c *modelPluginClient) ModelInfo(ctx context.Context, in *proto.ModelInfoRequest, opts ...grpc.CallOption) (*proto.ModelInfoResponse, error) {
	out := new(proto.ModelInfoResponse)
	return out, c.cc.Invoke(ctx, "/"+modelService+"/ModelInfo", in, out, opts...)
}
func (c *modelPluginClient) Chat(ctx context.Context, in *proto.ModelChatRequest, opts ...grpc.CallOption) (*proto.ModelChatResponse, error) {
	out := new(proto.ModelChatResponse)
	return out, c.cc.Invoke(ctx, "/"+modelService+"/Chat", in, out, opts...)
}
func (c *modelPluginClient) ChatStream(ctx context.Context, in *proto.ModelChatRequest, opts ...grpc.CallOption) (ModelStreamResponseClient, error) {
	stream, err := c.cc.NewStream(ctx, &modelServiceDesc.Streams[0], "/"+modelService+"/ChatStream", opts...)
	if err != nil {
		return nil, err
	}
	if err := stream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	return modelStreamResponseClient{ClientStream: stream}, nil
}
func (c *modelPluginClient) Embed(ctx context.Context, in *proto.ModelEmbedRequest, opts ...grpc.CallOption) (*proto.ModelEmbedResponse, error) {
	out := new(proto.ModelEmbedResponse)
	return out, c.cc.Invoke(ctx, "/"+modelService+"/Embed", in, out, opts...)
}
func (c *modelPluginClient) BatchEmbed(ctx context.Context, in *proto.ModelBatchEmbedRequest, opts ...grpc.CallOption) (*proto.ModelBatchEmbedResponse, error) {
	out := new(proto.ModelBatchEmbedResponse)
	return out, c.cc.Invoke(ctx, "/"+modelService+"/BatchEmbed", in, out, opts...)
}
func (c *modelPluginClient) Rerank(ctx context.Context, in *proto.ModelRerankRequest, opts ...grpc.CallOption) (*proto.ModelRerankResponse, error) {
	out := new(proto.ModelRerankResponse)
	return out, c.cc.Invoke(ctx, "/"+modelService+"/Rerank", in, out, opts...)
}
func (c *modelPluginClient) PredictVLM(ctx context.Context, in *proto.ModelVLMRequest, opts ...grpc.CallOption) (*proto.ModelVLMResponse, error) {
	out := new(proto.ModelVLMResponse)
	return out, c.cc.Invoke(ctx, "/"+modelService+"/PredictVLM", in, out, opts...)
}
func (c *modelPluginClient) Transcribe(ctx context.Context, in *proto.ModelASRRequest, opts ...grpc.CallOption) (*proto.ModelASRResponse, error) {
	out := new(proto.ModelASRResponse)
	return out, c.cc.Invoke(ctx, "/"+modelService+"/Transcribe", in, out, opts...)
}

func RegisterModelPluginServer(r grpc.ServiceRegistrar, s ModelPluginServer) {
	r.RegisterService(&modelServiceDesc, s)
}

func modelHandler[T any](call func(ModelPluginServer, context.Context, T) (any, error), newReq func() T) grpc.MethodHandler {
	return func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := newReq()
		if err := dec(in); err != nil {
			return nil, err
		}
		s := srv.(ModelPluginServer)
		if interceptor == nil {
			return call(s, ctx, in)
		}
		info := &grpc.UnaryServerInfo{Server: srv}
		h := func(ctx context.Context, req any) (any, error) { return call(s, ctx, req.(T)) }
		return interceptor(ctx, in, info, h)
	}
}

func modelStreamHandler(call func(ModelPluginServer, context.Context, *proto.ModelChatRequest, ModelStreamResponseServer) error) grpc.StreamHandler {
	return func(srv any, stream grpc.ServerStream) error {
		in := new(proto.ModelChatRequest)
		if err := stream.RecvMsg(in); err != nil {
			return err
		}
		return call(srv.(ModelPluginServer), stream.Context(), in, modelStreamResponseServer{stream})
	}
}

var modelServiceDesc = grpc.ServiceDesc{ServiceName: modelService, HandlerType: (*ModelPluginServer)(nil), Methods: []grpc.MethodDesc{
	{MethodName: "ValidateConfig", Handler: modelHandler(func(s ModelPluginServer, c context.Context, in *proto.ModelValidateRequest) (any, error) {
		return s.ValidateConfig(c, in)
	}, func() *proto.ModelValidateRequest { return &proto.ModelValidateRequest{} })},
	{MethodName: "ModelInfo", Handler: modelHandler(func(s ModelPluginServer, c context.Context, in *proto.ModelInfoRequest) (any, error) {
		return s.ModelInfo(c, in)
	}, func() *proto.ModelInfoRequest { return &proto.ModelInfoRequest{} })},
	{MethodName: "Chat", Handler: modelHandler(func(s ModelPluginServer, c context.Context, in *proto.ModelChatRequest) (any, error) {
		return s.Chat(c, in)
	}, func() *proto.ModelChatRequest { return &proto.ModelChatRequest{} })},
	{MethodName: "Embed", Handler: modelHandler(func(s ModelPluginServer, c context.Context, in *proto.ModelEmbedRequest) (any, error) {
		return s.Embed(c, in)
	}, func() *proto.ModelEmbedRequest { return &proto.ModelEmbedRequest{} })},
	{MethodName: "BatchEmbed", Handler: modelHandler(func(s ModelPluginServer, c context.Context, in *proto.ModelBatchEmbedRequest) (any, error) {
		return s.BatchEmbed(c, in)
	}, func() *proto.ModelBatchEmbedRequest { return &proto.ModelBatchEmbedRequest{} })},
	{MethodName: "Rerank", Handler: modelHandler(func(s ModelPluginServer, c context.Context, in *proto.ModelRerankRequest) (any, error) {
		return s.Rerank(c, in)
	}, func() *proto.ModelRerankRequest { return &proto.ModelRerankRequest{} })},
	{MethodName: "PredictVLM", Handler: modelHandler(func(s ModelPluginServer, c context.Context, in *proto.ModelVLMRequest) (any, error) {
		return s.PredictVLM(c, in)
	}, func() *proto.ModelVLMRequest { return &proto.ModelVLMRequest{} })},
	{MethodName: "Transcribe", Handler: modelHandler(func(s ModelPluginServer, c context.Context, in *proto.ModelASRRequest) (any, error) {
		return s.Transcribe(c, in)
	}, func() *proto.ModelASRRequest { return &proto.ModelASRRequest{} })},
}, Streams: []grpc.StreamDesc{
	{StreamName: "ChatStream", ServerStreams: true, Handler: modelStreamHandler(func(s ModelPluginServer, c context.Context, in *proto.ModelChatRequest, stream ModelStreamResponseServer) error {
		return s.ChatStream(c, in, stream)
	})},
}}

const retrieverService = "weknora.plugin.v1.RetrieverPlugin"

type RetrieverPluginServer interface {
	Describe(context.Context, *proto.RetrieverDescribeRequest) (*proto.RetrieverDescribeResponse, error)
	OpenStore(context.Context, *proto.RetrieverOpenStoreRequest) (*proto.RetrieverOpenStoreResponse, error)
	CloseStore(context.Context, *proto.RetrieverCloseStoreRequest) (*proto.RetrieverResponse, error)
	BatchPut(context.Context, *proto.RetrieverBatchPutRequest) (*proto.RetrieverBatchPutResponse, error)
	Search(context.Context, *proto.RetrieverSearchRequest) (*proto.RetrieverSearchResponse, error)
	Delete(context.Context, *proto.RetrieverDeleteRequest) (*proto.RetrieverResponse, error)
	Patch(context.Context, *proto.RetrieverPatchRequest) (*proto.RetrieverResponse, error)
}

type RetrieverPluginClient interface {
	Describe(context.Context, *proto.RetrieverDescribeRequest, ...grpc.CallOption) (*proto.RetrieverDescribeResponse, error)
	OpenStore(context.Context, *proto.RetrieverOpenStoreRequest, ...grpc.CallOption) (*proto.RetrieverOpenStoreResponse, error)
	CloseStore(context.Context, *proto.RetrieverCloseStoreRequest, ...grpc.CallOption) (*proto.RetrieverResponse, error)
	BatchPut(context.Context, *proto.RetrieverBatchPutRequest, ...grpc.CallOption) (*proto.RetrieverBatchPutResponse, error)
	Search(context.Context, *proto.RetrieverSearchRequest, ...grpc.CallOption) (*proto.RetrieverSearchResponse, error)
	Delete(context.Context, *proto.RetrieverDeleteRequest, ...grpc.CallOption) (*proto.RetrieverResponse, error)
	Patch(context.Context, *proto.RetrieverPatchRequest, ...grpc.CallOption) (*proto.RetrieverResponse, error)
}

type retrieverPluginClient struct{ cc grpc.ClientConnInterface }

func NewRetrieverPluginClient(cc grpc.ClientConnInterface) RetrieverPluginClient {
	return &retrieverPluginClient{cc: cc}
}

func (c *retrieverPluginClient) Describe(ctx context.Context, in *proto.RetrieverDescribeRequest, opts ...grpc.CallOption) (*proto.RetrieverDescribeResponse, error) {
	out := new(proto.RetrieverDescribeResponse)
	return out, c.cc.Invoke(ctx, "/"+retrieverService+"/Describe", in, out, opts...)
}
func (c *retrieverPluginClient) OpenStore(ctx context.Context, in *proto.RetrieverOpenStoreRequest, opts ...grpc.CallOption) (*proto.RetrieverOpenStoreResponse, error) {
	out := new(proto.RetrieverOpenStoreResponse)
	return out, c.cc.Invoke(ctx, "/"+retrieverService+"/OpenStore", in, out, opts...)
}
func (c *retrieverPluginClient) CloseStore(ctx context.Context, in *proto.RetrieverCloseStoreRequest, opts ...grpc.CallOption) (*proto.RetrieverResponse, error) {
	out := new(proto.RetrieverResponse)
	return out, c.cc.Invoke(ctx, "/"+retrieverService+"/CloseStore", in, out, opts...)
}
func (c *retrieverPluginClient) BatchPut(ctx context.Context, in *proto.RetrieverBatchPutRequest, opts ...grpc.CallOption) (*proto.RetrieverBatchPutResponse, error) {
	out := new(proto.RetrieverBatchPutResponse)
	return out, c.cc.Invoke(ctx, "/"+retrieverService+"/BatchPut", in, out, opts...)
}
func (c *retrieverPluginClient) Search(ctx context.Context, in *proto.RetrieverSearchRequest, opts ...grpc.CallOption) (*proto.RetrieverSearchResponse, error) {
	out := new(proto.RetrieverSearchResponse)
	return out, c.cc.Invoke(ctx, "/"+retrieverService+"/Search", in, out, opts...)
}
func (c *retrieverPluginClient) Delete(ctx context.Context, in *proto.RetrieverDeleteRequest, opts ...grpc.CallOption) (*proto.RetrieverResponse, error) {
	out := new(proto.RetrieverResponse)
	return out, c.cc.Invoke(ctx, "/"+retrieverService+"/Delete", in, out, opts...)
}
func (c *retrieverPluginClient) Patch(ctx context.Context, in *proto.RetrieverPatchRequest, opts ...grpc.CallOption) (*proto.RetrieverResponse, error) {
	out := new(proto.RetrieverResponse)
	return out, c.cc.Invoke(ctx, "/"+retrieverService+"/Patch", in, out, opts...)
}

func RegisterRetrieverPluginServer(r grpc.ServiceRegistrar, s RetrieverPluginServer) {
	r.RegisterService(&retrieverServiceDesc, s)
}

func retrieverHandler[T any](call func(RetrieverPluginServer, context.Context, T) (any, error), newReq func() T) grpc.MethodHandler {
	return func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := newReq()
		if err := dec(in); err != nil {
			return nil, err
		}
		s := srv.(RetrieverPluginServer)
		if interceptor == nil {
			return call(s, ctx, in)
		}
		info := &grpc.UnaryServerInfo{Server: srv}
		h := func(ctx context.Context, req any) (any, error) { return call(s, ctx, req.(T)) }
		return interceptor(ctx, in, info, h)
	}
}

var retrieverServiceDesc = grpc.ServiceDesc{ServiceName: retrieverService, HandlerType: (*RetrieverPluginServer)(nil), Methods: []grpc.MethodDesc{
	{MethodName: "Describe", Handler: retrieverHandler(func(s RetrieverPluginServer, c context.Context, in *proto.RetrieverDescribeRequest) (any, error) {
		return s.Describe(c, in)
	}, func() *proto.RetrieverDescribeRequest { return &proto.RetrieverDescribeRequest{} })},
	{MethodName: "OpenStore", Handler: retrieverHandler(func(s RetrieverPluginServer, c context.Context, in *proto.RetrieverOpenStoreRequest) (any, error) {
		return s.OpenStore(c, in)
	}, func() *proto.RetrieverOpenStoreRequest { return &proto.RetrieverOpenStoreRequest{} })},
	{MethodName: "CloseStore", Handler: retrieverHandler(func(s RetrieverPluginServer, c context.Context, in *proto.RetrieverCloseStoreRequest) (any, error) {
		return s.CloseStore(c, in)
	}, func() *proto.RetrieverCloseStoreRequest { return &proto.RetrieverCloseStoreRequest{} })},
	{MethodName: "BatchPut", Handler: retrieverHandler(func(s RetrieverPluginServer, c context.Context, in *proto.RetrieverBatchPutRequest) (any, error) {
		return s.BatchPut(c, in)
	}, func() *proto.RetrieverBatchPutRequest { return &proto.RetrieverBatchPutRequest{} })},
	{MethodName: "Search", Handler: retrieverHandler(func(s RetrieverPluginServer, c context.Context, in *proto.RetrieverSearchRequest) (any, error) {
		return s.Search(c, in)
	}, func() *proto.RetrieverSearchRequest { return &proto.RetrieverSearchRequest{} })},
	{MethodName: "Delete", Handler: retrieverHandler(func(s RetrieverPluginServer, c context.Context, in *proto.RetrieverDeleteRequest) (any, error) {
		return s.Delete(c, in)
	}, func() *proto.RetrieverDeleteRequest { return &proto.RetrieverDeleteRequest{} })},
	{MethodName: "Patch", Handler: retrieverHandler(func(s RetrieverPluginServer, c context.Context, in *proto.RetrieverPatchRequest) (any, error) {
		return s.Patch(c, in)
	}, func() *proto.RetrieverPatchRequest { return &proto.RetrieverPatchRequest{} })},
}}

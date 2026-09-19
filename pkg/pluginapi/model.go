package pluginapi

import (
	"context"
	"fmt"

	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/grpc"
)

// ModelHandler implements ModelPluginServer for external model-provider
// plugins. A single plugin process may serve any subset of the five model
// capabilities (chat / embedding / rerank / vllm / asr); leave a callback nil
// to signal that the capability is not implemented.
type ModelHandler struct {
	PluginID     string
	Capabilities []string // "chat", "embedding", "rerank", "vllm", "asr"

	OnValidateConfig func(context.Context, map[string]any) error
	OnChat           func(context.Context, ChatRequest) (ChatResult, error)
	OnChatStream     func(context.Context, ChatRequest, func(StreamChunk) error) error
	OnEmbed          func(context.Context, string) ([]float32, error)
	OnBatchEmbed     func(context.Context, []string) ([][]float32, error)
	OnRerank         func(context.Context, string, []string) ([]RerankResult, error)
	OnPredictVLM     func(context.Context, [][]byte, string) (string, error)
	OnTranscribe     func(context.Context, []byte, string) (string, error)
	OnHealth         func(context.Context) HealthResponse
}

// ChatMessage is the SDK view of a chat message.
type ChatMessage struct {
	Role             string // system / user / assistant / tool
	Content          string
	Name             string
	ToolCallID       string
	ToolCalls        []ChatToolCall
	Images           []string
	ReasoningContent string
	MultiContent     []ChatContentPart
}

type ChatContentPart struct {
	Type     string // "text" or "image_url"
	Text     string
	ImageURL string
}

type ChatToolCall struct {
	ID                string
	Type              string
	FunctionName      string
	FunctionArguments string
}

type ChatTool struct {
	Type                string
	FunctionName        string
	FunctionDescription string
	FunctionParameters  string
}

type ChatOptions struct {
	Temperature         float64
	TopP                float64
	Seed                int
	MaxTokens           int
	MaxCompletionTokens int
	FrequencyPenalty    float64
	PresencePenalty     float64
	Thinking            bool
	Tools               []ChatTool
	ToolChoice          string
	ParallelToolCalls   bool
	Format              string
}

type ChatRequest struct {
	Messages  []ChatMessage
	Options   ChatOptions
	ModelID   string // identity of the target model instance (from ModelContext metadata)
	ModelName string
}

type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type ChatResult struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ChatToolCall
	FinishReason     string
	Usage            TokenUsage
}

type StreamChunk struct {
	Content          string
	ReasoningContent string
	Done             bool
	ToolCalls        []ChatToolCall
	FinishReason     string
	Usage            *TokenUsage
}

type RerankResult struct {
	Index          int
	RelevanceScore float64
}

func (h ModelHandler) Handshake(context.Context, *pluginproto.HandshakeRequest) (*pluginproto.HandshakeResponse, error) {
	return EncodeHandshake(HandshakeResponse{
		ProtocolVersion: ProtocolVersionV1,
		PluginID:        h.PluginID,
		Capabilities:    h.Capabilities,
		ExtensionType:   ExtensionTypeModel,
		Services:        []string{ExtensionTypeModel},
	}), nil
}

func (h ModelHandler) Health(ctx context.Context, _ *pluginproto.HealthRequest) (*pluginproto.HealthResponse, error) {
	v := HealthResponse{State: "running"}
	if h.OnHealth != nil {
		v = h.OnHealth(ctx)
	}
	return EncodeHealth(v), nil
}

func (h ModelHandler) ValidateConfig(ctx context.Context, v *pluginproto.ModelValidateRequest) (*pluginproto.ModelValidateResponse, error) {
	var err error
	if h.OnValidateConfig != nil {
		err = h.OnValidateConfig(ctx, mapConfig(v.GetConfig()))
	}
	return &pluginproto.ModelValidateResponse{Error: errorString(err)}, nil
}

func (h ModelHandler) ModelInfo(context.Context, *pluginproto.ModelInfoRequest) (*pluginproto.ModelInfoResponse, error) {
	return &pluginproto.ModelInfoResponse{Capabilities: h.Capabilities}, nil
}

func (h ModelHandler) Chat(ctx context.Context, v *pluginproto.ModelChatRequest) (*pluginproto.ModelChatResponse, error) {
	req := decodeChatRequest(v)
	mc := ModelContextFromContext(ctx)
	req.ModelID = mc.ModelID
	req.ModelName = mc.ModelName
	if h.OnChat == nil {
		return &pluginproto.ModelChatResponse{Error: "chat is not implemented"}, nil
	}
	result, err := h.OnChat(ctx, req)
	return encodeChatResult(result, err), nil
}

func (h ModelHandler) ChatStream(ctx context.Context, v *pluginproto.ModelChatRequest, stream ModelStreamResponseServer) error {
	req := decodeChatRequest(v)
	mc := ModelContextFromContext(ctx)
	req.ModelID = mc.ModelID
	req.ModelName = mc.ModelName
	if h.OnChatStream == nil {
		return fmt.Errorf("chat stream is not implemented")
	}
	return h.OnChatStream(ctx, req, func(chunk StreamChunk) error {
		return stream.Send(encodeStreamChunk(chunk))
	})
}

func (h ModelHandler) Embed(ctx context.Context, v *pluginproto.ModelEmbedRequest) (*pluginproto.ModelEmbedResponse, error) {
	if h.OnEmbed == nil {
		return &pluginproto.ModelEmbedResponse{Error: "embedding is not implemented"}, nil
	}
	vector, err := h.OnEmbed(ctx, v.GetText())
	return &pluginproto.ModelEmbedResponse{Vector: vector, Dimensions: int32(len(vector)), Error: errorString(err)}, nil
}

func (h ModelHandler) BatchEmbed(ctx context.Context, v *pluginproto.ModelBatchEmbedRequest) (*pluginproto.ModelBatchEmbedResponse, error) {
	if h.OnBatchEmbed == nil {
		return &pluginproto.ModelBatchEmbedResponse{Error: "batch embedding is not implemented"}, nil
	}
	vectors, err := h.OnBatchEmbed(ctx, v.GetTexts())
	out := &pluginproto.ModelBatchEmbedResponse{Error: errorString(err)}
	for _, vec := range vectors {
		out.Vectors = append(out.Vectors, &pluginproto.ModelFloatVector{Values: vec})
	}
	if len(vectors) > 0 {
		out.Dimensions = int32(len(vectors[0]))
	}
	return out, nil
}

func (h ModelHandler) Rerank(ctx context.Context, v *pluginproto.ModelRerankRequest) (*pluginproto.ModelRerankResponse, error) {
	if h.OnRerank == nil {
		return &pluginproto.ModelRerankResponse{Error: "rerank is not implemented"}, nil
	}
	results, err := h.OnRerank(ctx, v.GetQuery(), v.GetDocuments())
	out := &pluginproto.ModelRerankResponse{Error: errorString(err)}
	for _, r := range results {
		out.Results = append(out.Results, &pluginproto.ModelRankResult{Index: int32(r.Index), RelevanceScore: r.RelevanceScore})
	}
	return out, nil
}

func (h ModelHandler) PredictVLM(ctx context.Context, v *pluginproto.ModelVLMRequest) (*pluginproto.ModelVLMResponse, error) {
	if h.OnPredictVLM == nil {
		return &pluginproto.ModelVLMResponse{Error: "vllm is not implemented"}, nil
	}
	text, err := h.OnPredictVLM(ctx, v.GetImages(), v.GetPrompt())
	return &pluginproto.ModelVLMResponse{Text: text, Error: errorString(err)}, nil
}

func (h ModelHandler) Transcribe(ctx context.Context, v *pluginproto.ModelASRRequest) (*pluginproto.ModelASRResponse, error) {
	if h.OnTranscribe == nil {
		return &pluginproto.ModelASRResponse{Error: "asr is not implemented"}, nil
	}
	text, err := h.OnTranscribe(ctx, v.GetAudio(), v.GetFileName())
	return &pluginproto.ModelASRResponse{Text: text, Error: errorString(err)}, nil
}

func ServeModel(ctx context.Context, address string, handler ModelHandler, opts ...grpc.ServerOption) error {
	return servePlugin(ctx, address, opts, func(s *grpc.Server) {
		RegisterPluginControlServer(s, handler)
		RegisterModelPluginServer(s, handler)
	})
}

func decodeChatRequest(v *pluginproto.ModelChatRequest) ChatRequest {
	if v == nil {
		return ChatRequest{}
	}
	req := ChatRequest{Options: decodeChatOptions(v.GetOptions())}
	for _, m := range v.GetMessages() {
		req.Messages = append(req.Messages, decodeChatMessage(m))
	}
	return req
}

func decodeChatMessage(m *pluginproto.ModelChatMessage) ChatMessage {
	if m == nil {
		return ChatMessage{}
	}
	out := ChatMessage{
		Role:             m.GetRole(),
		Content:          m.GetContent(),
		Name:             m.GetName(),
		ToolCallID:       m.GetToolCallId(),
		Images:           m.GetImages(),
		ReasoningContent: m.GetReasoningContent(),
	}
	for _, tc := range m.GetToolCalls() {
		out.ToolCalls = append(out.ToolCalls, ChatToolCall{ID: tc.GetId(), Type: tc.GetType(), FunctionName: tc.GetFunctionName(), FunctionArguments: tc.GetFunctionArguments()})
	}
	for _, part := range m.GetMultiContent() {
		out.MultiContent = append(out.MultiContent, ChatContentPart{Type: part.GetType(), Text: part.GetText(), ImageURL: part.GetImageUrl()})
	}
	return out
}

func decodeChatOptions(o *pluginproto.ModelChatOptions) ChatOptions {
	if o == nil {
		return ChatOptions{}
	}
	out := ChatOptions{
		Temperature:         o.GetTemperature(),
		TopP:                o.GetTopP(),
		Seed:                int(o.GetSeed()),
		MaxTokens:           int(o.GetMaxTokens()),
		MaxCompletionTokens: int(o.GetMaxCompletionTokens()),
		FrequencyPenalty:    o.GetFrequencyPenalty(),
		PresencePenalty:     o.GetPresencePenalty(),
		Thinking:            o.GetThinking(),
		ToolChoice:          o.GetToolChoice(),
		ParallelToolCalls:   o.GetParallelToolCalls(),
		Format:              o.GetFormat(),
	}
	for _, t := range o.GetTools() {
		out.Tools = append(out.Tools, ChatTool{Type: t.GetType(), FunctionName: t.GetFunctionName(), FunctionDescription: t.GetFunctionDescription(), FunctionParameters: t.GetFunctionParameters()})
	}
	return out
}

func encodeChatResult(result ChatResult, err error) *pluginproto.ModelChatResponse {
	out := &pluginproto.ModelChatResponse{
		Content:          result.Content,
		ReasoningContent: result.ReasoningContent,
		FinishReason:     result.FinishReason,
		Usage:            encodeTokenUsage(&result.Usage),
		Error:            errorString(err),
	}
	for _, tc := range result.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, &pluginproto.ModelChatToolCall{Id: tc.ID, Type: tc.Type, FunctionName: tc.FunctionName, FunctionArguments: tc.FunctionArguments})
	}
	return out
}

func encodeTokenUsage(u *TokenUsage) *pluginproto.ModelTokenUsage {
	if u == nil {
		return nil
	}
	return &pluginproto.ModelTokenUsage{PromptTokens: int32(u.PromptTokens), CompletionTokens: int32(u.CompletionTokens), TotalTokens: int32(u.TotalTokens)}
}

func encodeStreamChunk(chunk StreamChunk) *pluginproto.ModelStreamResponse {
	out := &pluginproto.ModelStreamResponse{Content: chunk.Content, ReasoningContent: chunk.ReasoningContent, Done: chunk.Done, FinishReason: chunk.FinishReason}
	if chunk.Usage != nil {
		out.Usage = encodeTokenUsage(chunk.Usage)
	}
	for _, tc := range chunk.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, &pluginproto.ModelChatToolCall{Id: tc.ID, Type: tc.Type, FunctionName: tc.FunctionName, FunctionArguments: tc.FunctionArguments})
	}
	return out
}

var _ ModelPluginServer = (*ModelHandler)(nil)

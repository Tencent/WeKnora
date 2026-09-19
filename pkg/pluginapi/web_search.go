package pluginapi

import (
	"context"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/grpc"
)

type WebSearchResult struct {
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
	Content     string `json:"content,omitempty"`
	Source      string `json:"source,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}
type WebSearchRequest struct {
	Query       string            `json:"query,omitempty"`
	MaxResults  int               `json:"max_results,omitempty"`
	IncludeDate bool              `json:"include_date,omitempty"`
	APIKey      string            `json:"api_key,omitempty"`
	EngineID    string            `json:"engine_id,omitempty"`
	BaseURL     string            `json:"base_url,omitempty"`
	ProxyURL    string            `json:"proxy_url,omitempty"`
	ExtraConfig map[string]string `json:"extra_config,omitempty"`
}
type WebSearchResponse struct {
	Results []*WebSearchResult `json:"results,omitempty"`
	Error   string             `json:"error,omitempty"`
}

func EncodeWebSearchRequest(v WebSearchRequest) (*pluginproto.WebSearchRequest, error) {
	return &pluginproto.WebSearchRequest{Query: v.Query, MaxResults: int32(v.MaxResults), IncludeDate: v.IncludeDate, ApiKey: v.APIKey, EngineId: v.EngineID, BaseUrl: v.BaseURL, ProxyUrl: v.ProxyURL, ExtraConfig: v.ExtraConfig}, nil
}
func DecodeWebSearchRequest(v *pluginproto.WebSearchRequest, out *WebSearchRequest) error {
	if v == nil {
		return errNil("web search request")
	}
	out.Query = v.Query
	out.MaxResults = int(v.MaxResults)
	out.IncludeDate = v.IncludeDate
	out.APIKey = v.ApiKey
	out.EngineID = v.EngineId
	out.BaseURL = v.BaseUrl
	out.ProxyURL = v.ProxyUrl
	out.ExtraConfig = v.ExtraConfig
	return nil
}
func EncodeWebSearchResponse(v WebSearchResponse) *pluginproto.WebSearchResponse {
	out := &pluginproto.WebSearchResponse{Error: v.Error}
	for _, r := range v.Results {
		if r != nil {
			out.Results = append(out.Results, &pluginproto.WebSearchResult{Title: r.Title, Url: r.URL, Snippet: r.Snippet, Content: r.Content, Source: r.Source, PublishedAt: r.PublishedAt})
		}
	}
	return out
}
func DecodeWebSearchResponse(v *pluginproto.WebSearchResponse, out *WebSearchResponse) error {
	if v == nil {
		return errNil("web search response")
	}
	out.Error = v.Error
	for _, r := range v.Results {
		if r != nil {
			out.Results = append(out.Results, &WebSearchResult{Title: r.Title, URL: r.Url, Snippet: r.Snippet, Content: r.Content, Source: r.Source, PublishedAt: r.PublishedAt})
		}
	}
	return nil
}

type WebSearchHandler struct {
	PluginID     string
	Capabilities []string
	OnSearch     func(context.Context, WebSearchRequest) (WebSearchResponse, error)
	OnHealth     func(context.Context) HealthResponse
}

func (h WebSearchHandler) Handshake(context.Context, *pluginproto.HandshakeRequest) (*pluginproto.HandshakeResponse, error) {
	return EncodeHandshake(HandshakeResponse{ProtocolVersion: ProtocolVersionV1, PluginID: h.PluginID, Capabilities: h.Capabilities, ExtensionType: ExtensionTypeSearch, Services: []string{ExtensionTypeSearch}}), nil
}
func (h WebSearchHandler) Health(ctx context.Context, _ *pluginproto.HealthRequest) (*pluginproto.HealthResponse, error) {
	v := HealthResponse{State: "running"}
	if h.OnHealth != nil {
		v = h.OnHealth(ctx)
	}
	return EncodeHealth(v), nil
}
func (h WebSearchHandler) Search(ctx context.Context, v *pluginproto.WebSearchRequest) (*pluginproto.WebSearchResponse, error) {
	var req WebSearchRequest
	if err := DecodeWebSearchRequest(v, &req); err != nil {
		return nil, err
	}
	if h.OnSearch == nil {
		return EncodeWebSearchResponse(WebSearchResponse{Error: "web search handler is not implemented"}), nil
	}
	out, err := h.OnSearch(ctx, req)
	if err != nil && out.Error == "" {
		out.Error = err.Error()
	}
	return EncodeWebSearchResponse(out), nil
}
func ServeWebSearch(ctx context.Context, address string, handler WebSearchHandler, opts ...grpc.ServerOption) error {
	return servePlugin(ctx, address, opts, func(s *grpc.Server) {
		RegisterPluginControlServer(s, handler)
		RegisterWebSearchPluginServer(s, handler)
	})
}

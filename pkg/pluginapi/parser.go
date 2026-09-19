package pluginapi

import (
	"context"
	"fmt"
	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
	"google.golang.org/grpc"
)

type ParserImageRef struct {
	Filename    string `json:"filename,omitempty"`
	OriginalRef string `json:"original_ref,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	StorageKey  string `json:"storage_key,omitempty"`
	ImageData   []byte `json:"image_data,omitempty"`
	IsOriginal  bool   `json:"is_original,omitempty"`
}
type ParserRequest struct {
	FileContent           []byte            `json:"file_content,omitempty"`
	FileName              string            `json:"file_name,omitempty"`
	FileType              string            `json:"file_type,omitempty"`
	URL                   string            `json:"url,omitempty"`
	Title                 string            `json:"title,omitempty"`
	ParserEngine          string            `json:"parser_engine,omitempty"`
	RequestID             string            `json:"request_id,omitempty"`
	ParserEngineOverrides map[string]string `json:"parser_engine_overrides,omitempty"`
}
type ParserResponse struct {
	MarkdownContent string            `json:"markdown_content,omitempty"`
	ImageRefs       []ParserImageRef  `json:"image_refs,omitempty"`
	ImageDirPath    string            `json:"image_dir_path,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	Error           string            `json:"error,omitempty"`
	IsAudio         bool              `json:"is_audio,omitempty"`
	AudioData       []byte            `json:"audio_data,omitempty"`
}

func EncodeParserRequest(v ParserRequest) (*pluginproto.ParserRequest, error) {
	return &pluginproto.ParserRequest{FileContent: v.FileContent, FileName: v.FileName, FileType: v.FileType, Url: v.URL, Title: v.Title, ParserEngine: v.ParserEngine, RequestId: v.RequestID, ParserEngineOverrides: v.ParserEngineOverrides}, nil
}
func DecodeParserRequest(v *pluginproto.ParserRequest, out *ParserRequest) error {
	if v == nil {
		return errNil("parser request")
	}
	out.FileContent = v.FileContent
	out.FileName = v.FileName
	out.FileType = v.FileType
	out.URL = v.Url
	out.Title = v.Title
	out.ParserEngine = v.ParserEngine
	out.RequestID = v.RequestId
	out.ParserEngineOverrides = v.ParserEngineOverrides
	return nil
}
func EncodeParserResponse(v ParserResponse) *pluginproto.ParserResponse {
	out := &pluginproto.ParserResponse{MarkdownContent: v.MarkdownContent, ImageDirPath: v.ImageDirPath, Metadata: v.Metadata, Error: v.Error, IsAudio: v.IsAudio, AudioData: v.AudioData}
	for _, i := range v.ImageRefs {
		out.ImageRefs = append(out.ImageRefs, &pluginproto.ParserImageRef{Filename: i.Filename, OriginalRef: i.OriginalRef, MimeType: i.MIMEType, StorageKey: i.StorageKey, ImageData: i.ImageData, IsOriginal: i.IsOriginal})
	}
	return out
}
func DecodeParserResponse(v *pluginproto.ParserResponse, out *ParserResponse) error {
	if v == nil {
		return errNil("parser response")
	}
	out.MarkdownContent = v.MarkdownContent
	out.ImageDirPath = v.ImageDirPath
	out.Metadata = v.Metadata
	out.Error = v.Error
	out.IsAudio = v.IsAudio
	out.AudioData = v.AudioData
	for _, i := range v.ImageRefs {
		if i != nil {
			out.ImageRefs = append(out.ImageRefs, ParserImageRef{Filename: i.Filename, OriginalRef: i.OriginalRef, MIMEType: i.MimeType, StorageKey: i.StorageKey, ImageData: i.ImageData, IsOriginal: i.IsOriginal})
		}
	}
	return nil
}
func errNil(name string) error { return fmt.Errorf("nil %s", name) }

type ParserHandler struct {
	PluginID     string
	Capabilities []string
	OnParse      func(context.Context, ParserRequest) (ParserResponse, error)
	OnHealth     func(context.Context) HealthResponse
}

func (h ParserHandler) Handshake(context.Context, *pluginproto.HandshakeRequest) (*pluginproto.HandshakeResponse, error) {
	return EncodeHandshake(HandshakeResponse{ProtocolVersion: ProtocolVersionV1, PluginID: h.PluginID, Capabilities: h.Capabilities, ExtensionType: ExtensionTypeParser, Services: []string{ExtensionTypeParser}}), nil
}
func (h ParserHandler) Health(ctx context.Context, _ *pluginproto.HealthRequest) (*pluginproto.HealthResponse, error) {
	v := HealthResponse{State: "running"}
	if h.OnHealth != nil {
		v = h.OnHealth(ctx)
	}
	return EncodeHealth(v), nil
}
func (h ParserHandler) Parse(ctx context.Context, v *pluginproto.ParserRequest) (*pluginproto.ParserResponse, error) {
	var req ParserRequest
	if err := DecodeParserRequest(v, &req); err != nil {
		return nil, err
	}
	if h.OnParse == nil {
		return EncodeParserResponse(ParserResponse{Error: "parser handler is not implemented"}), nil
	}
	out, err := h.OnParse(ctx, req)
	if err != nil && out.Error == "" {
		out.Error = err.Error()
	}
	return EncodeParserResponse(out), nil
}
func ServeParser(ctx context.Context, address string, handler ParserHandler, opts ...grpc.ServerOption) error {
	return servePlugin(ctx, address, opts, func(s *grpc.Server) {
		RegisterPluginControlServer(s, handler)
		RegisterParserPluginServer(s, handler)
	})
}

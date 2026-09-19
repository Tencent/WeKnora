package plugin

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
)

// GRPCParserProxy adapts the public parser plugin protocol to the existing
// DocReader interface used by the knowledge ingestion pipeline.
type GRPCParserProxy struct {
	Client   pluginapi.ParserPluginClient
	Manager  *Manager
	PluginID string
}

func (p *GRPCParserProxy) Read(ctx context.Context, request *types.ReadRequest) (*types.ReadResult, error) {
	if request == nil {
		return nil, fmt.Errorf("parser request is nil")
	}
	if p == nil || p.Client == nil {
		return nil, fmt.Errorf("parser plugin client is not connected")
	}
	callCtx, release, generation, err := acquirePluginCall(ctx, p.Manager, p.PluginID, invocationFromContext(ctx, request.RequestID))
	if err != nil {
		return nil, err
	}
	defer release()
	wire, err := pluginapi.EncodeParserRequest(pluginapi.ParserRequest{
		FileContent:           request.FileContent,
		FileName:              request.FileName,
		FileType:              request.FileType,
		URL:                   request.URL,
		Title:                 request.Title,
		ParserEngine:          request.ParserEngine,
		RequestID:             request.RequestID,
		ParserEngineOverrides: request.ParserEngineOverrides,
	})
	if err != nil {
		return nil, err
	}
	responseWire, err := p.Client.Parse(callCtx, wire)
	if err != nil {
		return nil, err
	}
	if p.Manager != nil && !p.Manager.GenerationValid(p.PluginID, generation) {
		return nil, fmt.Errorf("parser plugin runtime generation %d is stale", generation)
	}
	var response pluginapi.ParserResponse
	if err := pluginapi.DecodeParserResponse(responseWire, &response); err != nil {
		return nil, err
	}
	result := &types.ReadResult{
		MarkdownContent: response.MarkdownContent,
		ImageDirPath:    response.ImageDirPath,
		Metadata:        response.Metadata,
		Error:           response.Error,
		IsAudio:         response.IsAudio,
		AudioData:       response.AudioData,
	}
	result.ImageRefs = make([]types.ImageRef, 0, len(response.ImageRefs))
	for _, image := range response.ImageRefs {
		result.ImageRefs = append(result.ImageRefs, types.ImageRef{
			Filename:    image.Filename,
			OriginalRef: image.OriginalRef,
			MimeType:    image.MIMEType,
			StorageKey:  image.StorageKey,
			ImageData:   image.ImageData,
			IsOriginal:  image.IsOriginal,
		})
	}
	return result, nil
}

var _ interfaces.DocReader = (*GRPCParserProxy)(nil)

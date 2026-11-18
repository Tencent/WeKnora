package chatpipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

func isRetryableChatFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var status *api.HTTPError
	if errors.As(err, &status) {
		return status.StatusCode == http.StatusTooManyRequests ||
			status.StatusCode == http.StatusRequestTimeout ||
			(status.StatusCode >= 500 && status.StatusCode < 600)
	}
	var transport *api.TransportError
	var network net.Error
	return errors.As(err, &transport) || errors.As(err, &network) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF)
}

func (p *PluginChatCompletionStream) startFallbackChatStream(
	ctx context.Context, request *types.ChatManage, messages []chat.Message, options *chat.ChatOptions,
	cancelPrimary context.CancelFunc, cause error,
) (<-chan types.StreamResponse, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	// A shared knowledge base grants access to its selected model. It does
	// not authorize forwarding the same request to another provider.
	if !ok || ctx.Err() != nil || (request.ChatModelTenantID != 0 && request.ChatModelTenantID != tenantID) {
		return nil, cause
	}
	primary, err := p.modelService.GetModelByID(ctx, request.ChatModelID)
	if err != nil || primary == nil || primary.IsBuiltin || primary.TenantID != tenantID {
		return nil, cause
	}
	backupID := primary.Parameters.FallbackChatModelID
	if backupID == "" || backupID == primary.ID {
		return nil, cause
	}
	backup, err := p.modelService.GetModelByID(ctx, backupID)
	if err != nil || backup == nil || backup.Type != types.ModelTypeKnowledgeQA ||
		(!backup.IsBuiltin && backup.TenantID != tenantID) {
		return nil, cause
	}
	for _, message := range messages {
		if len(message.Images) > 0 && !backup.Parameters.SupportsVision {
			return nil, cause
		}
	}
	model, err := p.modelService.GetChatModel(ctx, backupID)
	if err != nil {
		return nil, cause
	}
	pipelineWarn(ctx, "Stream", "fallback_model", map[string]interface{}{
		"primary_model_id":  request.ChatModelID,
		"fallback_model_id": backupID,
	})
	cancelPrimary()
	stream, err := model.ChatStream(ctx, messages, options)
	if err != nil {
		return nil, fmt.Errorf("backup model call failed: %w", err)
	}
	if stream == nil {
		return nil, errors.New("backup model returned no stream")
	}
	return stream, nil
}

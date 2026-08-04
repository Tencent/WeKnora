package session

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// resolveResourceRewriter builds the storage-reference rewriter for one response
// from the request's `resource_urls` parameter, falling back to the deployment
// default. An invalid value is a client error the caller must turn into a 400.
//
// In the default handle mode the returned rewriter is disabled, so responses are
// left exactly as before.
func (h *Handler) resolveResourceRewriter(c *gin.Context) (*storageurl.Rewriter, error) {
	ctx := c.Request.Context()
	mode, err := storageurl.ResolveMode(ctx, c.Query(storageurl.QueryParam))
	if err != nil {
		return nil, err
	}
	return storageurl.NewRequestRewriter(ctx, mode, h.fileService, h.storageResolver), nil
}

// resolveStreamRewriter is resolveResourceRewriter plus the holdback buffer an
// SSE response needs, because a storage reference can straddle two deltas. It
// must be called before any SSE header is written so an invalid value is still
// reportable as a normal 400.
func (h *Handler) resolveStreamRewriter(c *gin.Context) (*storageurl.StreamRewriter, error) {
	rewriter, err := h.resolveResourceRewriter(c)
	if err != nil {
		return nil, err
	}
	return storageurl.NewStreamRewriter(rewriter), nil
}

// deltaResponseTypes are the SSE events whose Content is an incremental chunk
// that clients accumulate. A storage reference can straddle two chunks, so these
// go through the holdback buffer; every other event carries a complete value.
var deltaResponseTypes = map[types.ResponseType]bool{
	types.ResponseTypeAnswer:     true,
	types.ResponseTypeThinking:   true,
	types.ResponseTypeReflection: true,
}

// holdbackKey identifies one delta stream. The event id is the key clients
// accumulate on, so interleaved answer and thinking streams hold back
// independently; the type prefix lets a flushed remainder be re-emitted as the
// event type it came from.
func holdbackKey(responseType types.ResponseType, eventID string) string {
	return string(responseType) + "\x00" + eventID
}

func parseHoldbackKey(key string) (types.ResponseType, string) {
	responseType, eventID, _ := strings.Cut(key, "\x00")
	return types.ResponseType(responseType), eventID
}

// buildStreamResponseFor builds the SSE payload for evt and, in public mode,
// replaces storage references with URLs the client can load directly.
func buildStreamResponseFor(
	ctx context.Context,
	evt interfaces.StreamEvent,
	requestID string,
	rewriter *storageurl.StreamRewriter,
) *types.StreamResponse {
	response := buildStreamResponse(evt, requestID)
	if !rewriter.Enabled() {
		return response
	}

	if deltaResponseTypes[evt.Type] {
		response.Content = rewriter.Push(ctx, holdbackKey(evt.Type, evt.ID), response.Content, evt.Done)
	} else {
		response.Content = rewriter.Rewriter().String(ctx, response.Content)
	}
	response.KnowledgeReferences = rewriter.Rewriter().CopyReferences(ctx, response.KnowledgeReferences)
	response.Data = rewriter.Rewriter().CopyData(ctx, response.Data)
	return response
}

// emitStreamEvent writes one SSE payload. Content still sitting in the holdback
// buffer is released first when evt terminates the stream, because clients treat
// the completion marker as the end of the message.
func emitStreamEvent(
	ctx context.Context,
	c *gin.Context,
	evt interfaces.StreamEvent,
	requestID string,
	rewriter *storageurl.StreamRewriter,
) {
	response := buildStreamResponseFor(ctx, evt, requestID, rewriter)
	if evt.Type == types.ResponseTypeComplete {
		flushHeldStreamContent(ctx, c, requestID, rewriter)
	}
	c.SSEvent("message", response)
	c.Writer.Flush()
}

// flushHeldStreamContent emits whatever the holdback buffer still retains, so a
// trailing reference is not dropped when a delta stream ends without a terminal
// chunk. Callers invoke it once the stream is over; if the client has already
// gone there is nobody left to receive it.
func flushHeldStreamContent(
	ctx context.Context,
	c *gin.Context,
	requestID string,
	rewriter *storageurl.StreamRewriter,
) {
	held := rewriter.FlushAll(ctx)
	if len(held) == 0 || c.Request.Context().Err() != nil {
		return
	}
	for key, content := range held {
		if content == "" {
			continue
		}
		responseType, eventID := parseHoldbackKey(key)
		logger.Debugf(ctx, "Flushing held stream fragment, type: %s, event: %s", responseType, eventID)
		c.SSEvent("message", &types.StreamResponse{
			ID:           requestID,
			ResponseType: responseType,
			Content:      content,
			Data:         map[string]interface{}{"event_id": eventID},
		})
		c.Writer.Flush()
	}
}

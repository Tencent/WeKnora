package session

import (
	"context"
	stderrors "errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/storageurl"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// resourceModeError turns a mode-resolution failure into the response the client
// should see: a rejected scope is a 403, a typo in the parameter is a 400.
func resourceModeError(err error) error {
	if stderrors.Is(err, storageurl.ErrPublicModeForbidden) {
		return errors.NewForbiddenError(err.Error())
	}
	return errors.NewBadRequestError(err.Error())
}

// resolveResourceRewriter builds the storage-reference rewriter for one response
// from the request's `resource_urls` parameter, falling back to the deployment
// default. The returned error is already an AppError the caller can hand to
// c.Error.
//
// In the default handle mode the returned rewriter is disabled, so responses are
// left exactly as before.
func (h *Handler) resolveResourceRewriter(c *gin.Context) (*storageurl.Rewriter, error) {
	ctx := c.Request.Context()
	mode, err := storageurl.ResolveMode(ctx, c.Query(storageurl.QueryParam))
	if err != nil {
		return nil, resourceModeError(err)
	}
	return storageurl.NewRequestRewriter(ctx, mode, h.fileService, h.storageResolver), nil
}

// resolveStreamRewriter is resolveResourceRewriter plus the holdback buffer an
// SSE response needs, because a storage reference can straddle two deltas. It
// must be called before any SSE header is written so an invalid value is still
// reportable as a normal JSON error.
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

// terminalResponseTypes end the message as far as the client is concerned, so
// any buffered tail must be released just before them. An error can be the last
// event a run produces, and a completion may or may not follow it; flushing an
// already-empty buffer is a no-op, so covering both is safe.
var terminalResponseTypes = map[types.ResponseType]bool{
	types.ResponseTypeComplete: true,
	types.ResponseTypeError:    true,
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

	response.KnowledgeReferences = rewriter.Rewriter().CopyReferences(ctx, response.KnowledgeReferences)
	response.Data = rewriter.Rewriter().CopyData(ctx, response.Data)
	if deltaResponseTypes[evt.Type] {
		// The rewritten Data rides along with the held tail so a late release
		// carries the same metadata as the event it was cut from.
		response.Content = rewriter.Push(
			ctx, holdbackKey(evt.Type, evt.ID), response.Content, evt.Done, response.Data)
	} else {
		response.Content = rewriter.Rewriter().String(ctx, response.Content)
	}
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
	if terminalResponseTypes[evt.Type] {
		flushHeldStreamContent(ctx, c, requestID, rewriter)
	}
	c.SSEvent("message", response)
	c.Writer.Flush()
}

// flushHeldStreamContent emits whatever the holdback buffer still retains, so a
// trailing reference is not dropped when a delta stream ends without a terminal
// chunk. Every path that stops streaming while the client is still connected
// must call it — completion, a user-requested stop, or giving up on the event
// store — otherwise the tail is silently lost. If the client has already gone
// there is nobody left to receive it.
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
	for key, fragment := range held {
		if fragment.Content == "" {
			continue
		}
		responseType, eventID := parseHoldbackKey(key)
		logger.Debugf(ctx, "Flushing held stream fragment, type: %s, event: %s", responseType, eventID)
		c.SSEvent("message", &types.StreamResponse{
			ID:           requestID,
			ResponseType: responseType,
			Content:      fragment.Content,
			Data:         heldFragmentData(fragment.Meta, eventID),
		})
		c.Writer.Flush()
	}
}

// heldFragmentData rebuilds the metadata for a released tail from the event it
// was cut from, so a client keying off `event_id` — or off anything else the
// original event carried, such as `is_fallback` — sees the same shape. The map
// is copied because an unchanged rewrite returns the stream buffer's own map.
func heldFragmentData(meta interface{}, eventID string) map[string]interface{} {
	original, _ := meta.(map[string]interface{})
	data := make(map[string]interface{}, len(original)+1)
	for key, value := range original {
		data[key] = value
	}
	if _, ok := data["event_id"]; !ok {
		data["event_id"] = eventID
	}
	return data
}

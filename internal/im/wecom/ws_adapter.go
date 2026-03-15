package wecom

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/gin-gonic/gin"
)

// Compile-time checks.
var (
	_ im.Adapter      = (*WSAdapter)(nil)
	_ im.StreamSender = (*WSAdapter)(nil)
)

// WSAdapter implements im.Adapter and im.StreamSender for WeCom in WebSocket
// (long connection) mode. It delegates to the WebSocket LongConnClient.
// The webhook methods (VerifyCallback, ParseCallback, HandleURLVerification) are no-ops
// since messages arrive via WebSocket, not HTTP.
type WSAdapter struct {
	client *LongConnClient
}

// NewWSAdapter creates an adapter backed by a WeCom long connection client.
func NewWSAdapter(client *LongConnClient) *WSAdapter {
	return &WSAdapter{client: client}
}

func (a *WSAdapter) Platform() im.Platform {
	return im.PlatformWeCom
}

func (a *WSAdapter) VerifyCallback(c *gin.Context) error {
	return fmt.Errorf("WeCom bot adapter does not support webhook callbacks")
}

func (a *WSAdapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	return nil, fmt.Errorf("WeCom bot adapter does not support webhook callbacks")
}

func (a *WSAdapter) HandleURLVerification(c *gin.Context) bool {
	return false
}

func (a *WSAdapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	return a.client.SendReply(ctx, incoming, reply)
}

// ── StreamSender implementation ──

func (a *WSAdapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	return a.client.StartStream(ctx, incoming)
}

func (a *WSAdapter) SendStreamChunk(ctx context.Context, incoming *im.IncomingMessage, streamID string, content string) error {
	return a.client.SendStreamChunk(ctx, incoming, streamID, content)
}

func (a *WSAdapter) EndStream(ctx context.Context, incoming *im.IncomingMessage, streamID string) error {
	return a.client.EndStream(ctx, incoming, streamID)
}

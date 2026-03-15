package wecom

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/gin-gonic/gin"
)

// BotAdapter implements im.Adapter for WeCom intelligent bot (long connection mode).
// It delegates SendReply to the WebSocket LongConnClient.
// The webhook methods (VerifyCallback, ParseCallback, HandleURLVerification) are no-ops
// since messages arrive via WebSocket, not HTTP.
type BotAdapter struct {
	client *LongConnClient
}

// NewBotAdapter creates an adapter backed by a WeCom long connection client.
func NewBotAdapter(client *LongConnClient) *BotAdapter {
	return &BotAdapter{client: client}
}

func (a *BotAdapter) Platform() im.Platform {
	return im.PlatformWeCom
}

func (a *BotAdapter) VerifyCallback(c *gin.Context) error {
	return fmt.Errorf("WeCom bot adapter does not support webhook callbacks")
}

func (a *BotAdapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	return nil, fmt.Errorf("WeCom bot adapter does not support webhook callbacks")
}

func (a *BotAdapter) HandleURLVerification(c *gin.Context) bool {
	return false
}

func (a *BotAdapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	return a.client.SendReply(ctx, incoming, reply)
}

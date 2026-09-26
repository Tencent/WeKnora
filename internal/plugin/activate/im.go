package activate

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Bounds on relaying an IM platform's callback to a plugin.
const (
	imCallbackTimeout = 10 * time.Second
	imSendTimeout     = 30 * time.Second
	maxIMCallbackBody = 1 << 20
)

// IMPlatforms offers plugins' IM channels as IM platforms (im.SetPluginPlatforms).
// They connect over webhooks: the platform calls the channel's callback URL,
// WeKnora relays each request to the plugin, answers the message the plugin
// finds in it and has the plugin send the reply.
type IMPlatforms struct {
	iv  *Invoker
	reg *registry.Registry
}

// NewIMPlatforms creates the plugin IM platform source.
func NewIMPlatforms(iv *Invoker, reg *registry.Registry) *IMPlatforms {
	return &IMPlatforms{iv: iv, reg: reg}
}

// Platforms implements im.PluginPlatforms.
func (p *IMPlatforms) Platforms() []im.PlatformInfo {
	var out []im.PlatformInfo
	for _, e := range p.reg.Contributions(manifest.PointIMChannels) {
		if m, ok := p.reg.Plugin(e.PluginID); ok && !m.Builtin {
			out = append(out, platformInfo(e))
		}
	}
	return out
}

// Platform implements im.PluginPlatforms.
func (p *IMPlatforms) Platform(id string) (im.PlatformInfo, im.AdapterFactory, bool) {
	e, ok := p.reg.Resolve(manifest.PointIMChannels, id)
	if !ok || e.QualifiedID != id {
		return im.PlatformInfo{}, nil, false
	}
	m, ok := p.reg.Plugin(e.PluginID)
	if !ok || m.Builtin {
		return im.PlatformInfo{}, nil, false
	}
	factory := func(_ context.Context, channel *im.IMChannel,
		handle func(context.Context, *im.IncomingMessage) error,
	) (im.Adapter, context.CancelFunc, error) {
		return &pluginIMAdapter{
			iv: p.iv, m: m, local: e.Contribution.ID, platform: id, channel: channel, handle: handle,
		}, nil, nil
	}
	return platformInfo(e), factory, true
}

func platformInfo(e registry.Entry) im.PlatformInfo {
	info := im.PlatformInfo{
		ID: e.QualifiedID, Name: e.Contribution.Name.Default, Order: 500 + e.Contribution.Order,
		Modes: []string{im.ModeWebhook},
	}
	if len(e.Contribution.Name.Locales) > 0 {
		info.Names = e.Contribution.Name.Locales
	}
	if len(e.Contribution.InstanceSchemaJSON) > 0 {
		if s, err := configschema.Parse(e.Contribution.InstanceSchemaJSON); err == nil {
			info.ConfigSchema = s
		}
	}
	return info
}

// pluginIMAdapter is one channel of a plugin's IM platform.
type pluginIMAdapter struct {
	iv       *Invoker
	m        *manifest.Manifest
	local    string
	platform string
	channel  *im.IMChannel
	handle   func(context.Context, *im.IncomingMessage) error
}

func (a *pluginIMAdapter) Platform() im.Platform { return im.Platform(a.platform) }

// call reaches the plugin as the channel's workspace, with the channel's
// credentials as the instance configuration.
func (a *pluginIMAdapter) call(ctx context.Context, path string, in, out any) error {
	creds, err := im.ParseCredentials(a.channel.Credentials)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, a.channel.TenantID)
	return a.iv.Call(ctx, a.m, path, creds, in, out)
}

// HandleURLVerification implements im.Adapter. The plugin reads every
// callback whole (verification, signature, the message), so the whole
// request is handled here: the plugin's answer goes back to the platform and
// a message it found is answered in the background.
func (a *pluginIMAdapter) HandleURLVerification(c *gin.Context) bool {
	ctx, cancel := context.WithTimeout(c.Request.Context(), imCallbackTimeout)
	defer cancel()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxIMCallbackBody+1))
	if err != nil || len(body) > maxIMCallbackBody {
		c.Status(http.StatusRequestEntityTooLarge)
		return true
	}
	headers := map[string]string{}
	for k, v := range c.Request.Header {
		if len(v) > 0 && !strings.EqualFold(k, "Cookie") {
			headers[k] = v[0]
		}
	}
	in := pluginapi.WebhookRequest{
		Method: c.Request.Method, Path: "/", Query: c.Request.URL.RawQuery, Headers: headers, Body: body,
	}
	var out pluginapi.IMCallbackOutput
	if err := a.call(ctx, pluginapi.IMCallbackPath(a.local), in, &out); err != nil {
		logger.Warnf(ctx, "[IM] plugin channel %s callback: %v", a.channel.ID, err)
		c.Status(http.StatusBadGateway)
		return true
	}
	if r := out.Response; r != nil {
		status := r.Status
		if status < 100 || status > 599 {
			status = http.StatusOK
		}
		ctype := r.ContentType
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		c.Data(status, ctype, r.Body)
	} else {
		c.Data(http.StatusOK, "application/json", []byte("{}"))
	}
	if msg := a.incoming(out.Message); msg != nil {
		bg := context.WithoutCancel(c.Request.Context())
		go func() {
			if err := a.handle(bg, msg); err != nil {
				logger.Errorf(bg, "[IM] plugin channel %s: handle message: %v", a.channel.ID, err)
			}
		}()
	}
	return true
}

func (a *pluginIMAdapter) incoming(m *pluginapi.IMMessage) *im.IncomingMessage {
	if m == nil || strings.TrimSpace(m.Content) == "" || m.UserID == "" || m.ChatID == "" {
		return nil
	}
	chatType := im.ChatTypeDirect
	if m.ChatType == string(im.ChatTypeGroup) {
		chatType = im.ChatTypeGroup
	}
	return &im.IncomingMessage{
		Platform: a.Platform(), MessageType: im.MessageTypeText, UserID: m.UserID, UserName: m.UserName,
		ChatID: m.ChatID, ChatType: chatType, Content: m.Content, MessageID: m.MessageID, ThreadID: m.ThreadID,
		Extra: m.Extra,
	}
}

// VerifyCallback implements im.Adapter; HandleURLVerification took the
// request, so it is never reached.
func (a *pluginIMAdapter) VerifyCallback(*gin.Context) error { return nil }

// ParseCallback implements im.Adapter; see VerifyCallback.
func (a *pluginIMAdapter) ParseCallback(*gin.Context) (*im.IncomingMessage, error) { return nil, nil }

// SendReply implements im.Adapter.
func (a *pluginIMAdapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	ctx, cancel := context.WithTimeout(ctx, imSendTimeout)
	defer cancel()
	in := pluginapi.IMSendInput{
		Message: pluginapi.IMMessage{
			UserID: incoming.UserID, UserName: incoming.UserName, ChatID: incoming.ChatID,
			ChatType: string(incoming.ChatType), Content: incoming.Content, MessageID: incoming.MessageID,
			ThreadID: incoming.ThreadID, Extra: incoming.Extra,
		},
		Content: reply.Content,
	}
	return a.call(ctx, pluginapi.IMSendPath(a.local), in, &struct{}{})
}

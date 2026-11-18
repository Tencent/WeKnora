package feishu

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var _ im.TopicReplyPolicySetter = (*Adapter)(nil)

// SetTopicReplyPolicy installs a live agent-config reader. A missing reader
// preserves the default mention-only behavior, including during channel startup.
func (a *Adapter) SetTopicReplyPolicy(policy func(context.Context) (bool, error)) {
	a.topicPolicyMu.Lock()
	a.topicPolicy = policy
	a.topicPolicyMu.Unlock()
}

func isGroupChat(kind string) bool { return kind == "group" || kind == "topic_group" }

func (a *Adapter) allowGroupMessage(ctx context.Context, chatID, messageID, rootID, parentID,
	senderType string, mentions []*larkim.MentionEvent,
) (bool, error) {
	botID, err := a.BotOpenID(ctx)
	if err != nil {
		return false, err
	}
	if mentionsBot(botID, mentions) {
		return true, nil
	}
	// Only human top-level messages can expand the default mention-only policy.
	if senderType != "user" || messageID == "" || chatID == "" || parentID != "" ||
		(rootID != "" && rootID != messageID) {
		return false, nil
	}
	a.topicPolicyMu.RLock()
	policy := a.topicPolicy
	a.topicPolicyMu.RUnlock()
	if policy == nil {
		return false, nil
	}
	enabled, err := policy(ctx)
	if err != nil || !enabled {
		return false, err
	}
	return a.isTopicGroup(ctx, chatID)
}

func (a *Adapter) isTopicGroup(ctx context.Context, chatID string) (bool, error) {
	token, err := a.getTenantAccessToken(ctx)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		a.api("/open-apis/im/v1/chats/%s", url.PathEscape(chatID)), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("group info HTTP status %d", resp.StatusCode)
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			ChatMode         string `json:"chat_mode"`
			GroupMessageType string `json:"group_message_type"`
		} `json:"data"`
	}
	if err := im.DecodeIMResponse(resp.Body, &result); err != nil {
		return false, err
	}
	if result.Code != 0 {
		return false, fmt.Errorf("group info error code %d", result.Code)
	}
	// Read the current mode: administrators can switch between chat and threads.
	return result.Data.ChatMode == "topic" ||
		(result.Data.ChatMode == "group" && result.Data.GroupMessageType == "thread"), nil
}

func (c *LongConnClient) convertIncoming(ctx context.Context, event *larkim.P2MessageReceiveV1) *im.IncomingMessage {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}
	msg := event.Event.Message
	if !isGroupChat(ptrStr(msg.ChatType)) {
		return convertEvent(c.region, "", event)
	}
	// The same adapter owns the webhook and WebSocket admission rules.
	filter, ok := c.botOpenIDProvider.(interface {
		allowGroupMessage(context.Context, string, string, string, string, string, []*larkim.MentionEvent) (bool, error)
	})
	if !ok {
		return convertEvent(c.region, c.botOpenID(ctx), event)
	}
	senderType := ""
	if event.Event.Sender != nil {
		senderType = ptrStr(event.Event.Sender.SenderType)
	}
	allow, err := filter.allowGroupMessage(ctx, ptrStr(msg.ChatId), ptrStr(msg.MessageId),
		ptrStr(msg.RootId), ptrStr(msg.ParentId), senderType, msg.Mentions)
	if err != nil {
		logger.Warnf(ctx, "[%s] group reply policy unavailable: %v", c.region.Label, err)
		return nil
	}
	if !allow {
		return nil
	}
	return convertAllowedEvent(c.region, "", event, true)
}

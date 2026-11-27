package im

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Admin digest (issue #5152): after an IM QA round completes, copy a short
// digest of the user's question and the model's reply to the platform
// admins configured on the channel, so admins no longer have to open
// WeKnora to notice incoming user requests.
//
// Design notes (kept minimal on purpose):
//   - Config lives on the channel (im_channels.admin_digest, JSON):
//     {"enabled": true, "user_ids": ["<platform user id>", ...]}.
//     "user_ids" are platform user IDs (e.g. a Feishu open_id), because
//     WeKnora tenant roles (owner/admin) have no mapping to IM platform
//     identities. Disabled by default.
//   - Delivery reuses the existing Adapter.SendReply with a synthetic
//     direct message to each admin. No per-platform send API is added.
//     Platforms whose SendReply can address a DM by user ID work
//     (feishu/lark via the plain send-message fallback, slack, telegram,
//     qqbot, yunzhijia, wecom in webhook mode). Platforms that need
//     per-conversation state (dingtalk session webhook, wechat
//     context_token, mattermost channel id, wecom websocket req_id) are
//     skipped, not retried -- see adminDigestSupportedPlatform.
//   - One digest per completed QA round. Failures are logged only and
//     never affect the round or its reply; each send has a 2s budget,
//     mirroring the reaction-send budget in PR #5752.

// adminDigestSendTimeout bounds a single digest send, mirroring the
// per-call budget used for IM reactions.
const adminDigestSendTimeout = 2 * time.Second

// adminDigestMaxQuestionRunes / adminDigestMaxAnswerRunes cap the digest
// body: a digest carries excerpts, never a full transcript dump.
const (
	adminDigestMaxQuestionRunes = 300
	adminDigestMaxAnswerRunes   = 500
)

// adminDigestMaxAdmins caps the configured admin list so a typo cannot
// turn one round into a fan-out.
const adminDigestMaxAdmins = 10

// adminDigestConfig is the parsed form of IMChannel.AdminDigest.
type adminDigestConfig struct {
	// Enabled turns the digest on for the channel. Default false.
	Enabled bool `json:"enabled"`
	// UserIDs are platform user IDs of the admins to notify.
	UserIDs []string `json:"user_ids"`
}

// parseAdminDigestConfig parses the channel's admin_digest JSON. Invalid,
// empty or missing JSON disables the digest. IDs are trimmed, empties
// dropped, duplicates removed (first occurrence wins), and the list is
// capped at adminDigestMaxAdmins.
func parseAdminDigestConfig(raw types.JSON) adminDigestConfig {
	var cfg adminDigestConfig
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "{}" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return adminDigestConfig{}
	}
	seen := make(map[string]bool, len(cfg.UserIDs))
	ids := make([]string, 0, len(cfg.UserIDs))
	for _, id := range cfg.UserIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		if len(ids) >= adminDigestMaxAdmins {
			break
		}
	}
	cfg.UserIDs = ids
	return cfg
}

// adminDigestSupportedPlatform reports whether a synthetic direct message
// via Adapter.SendReply can reach an admin on this channel. WeCom only
// supports it in webhook mode: the websocket/long-connection client
// answers through the live request (req_id) and cannot push to an
// arbitrary user.
func adminDigestSupportedPlatform(platform, mode string) bool {
	switch platform {
	case "feishu", "lark", "slack", "telegram", "qqbot", "yunzhijia":
		return true
	case "wecom":
		return mode == "webhook"
	default:
		return false
	}
}

// truncateDigestRunes shortens s to at most limit runes, appending an
// ellipsis when truncated. A non-positive limit keeps the string unchanged.
func truncateDigestRunes(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}

// buildAdminDigestText renders one digest message: channel, sender,
// truncated question and answer excerpts, and the session ID so the
// admin can find the full record in WeKnora.
func buildAdminDigestText(channel *IMChannel, msg *IncomingMessage, sessionID, answer string) string {
	name := strings.TrimSpace(channel.Name)
	if name == "" {
		name = channel.ID
	}
	user := strings.TrimSpace(msg.UserName)
	if user == "" {
		user = msg.UserID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "【管理员抄送】渠道「%s」（%s）收到新的用户提问\n", name, channel.Platform)
	fmt.Fprintf(&b, "用户：%s\n", user)
	fmt.Fprintf(&b, "问题：%s\n", truncateDigestRunes(strings.TrimSpace(msg.Content), adminDigestMaxQuestionRunes))
	fmt.Fprintf(&b, "回答摘要：%s\n", truncateDigestRunes(strings.TrimSpace(answer), adminDigestMaxAnswerRunes))
	fmt.Fprintf(&b, "会话：%s（完整记录见 WeKnora）", sessionID)
	return b.String()
}

// sendAdminDigest sends the round digest to the channel's configured
// admins. It never returns an error and never fails the round: every
// skip/failure is logged at most, and each send is bounded by
// adminDigestSendTimeout. It must be called after the user's reply has
// been delivered, with the final answer text.
func (s *Service) sendAdminDigest(
	ctx context.Context, adapter Adapter, channel *IMChannel,
	msg *IncomingMessage, sessionID, answer string,
) {
	if adapter == nil || channel == nil || msg == nil {
		return
	}
	cfg := parseAdminDigestConfig(channel.AdminDigest)
	if !cfg.Enabled || len(cfg.UserIDs) == 0 {
		return
	}
	if !adminDigestSupportedPlatform(channel.Platform, channel.Mode) {
		logger.Debugf(ctx, "[IM] Admin digest skipped: platform %s (mode %s) cannot receive a synthetic DM",
			channel.Platform, channel.Mode)
		return
	}
	if strings.TrimSpace(msg.Content) == "" || strings.TrimSpace(answer) == "" {
		return
	}
	text := buildAdminDigestText(channel, msg, sessionID, answer)
	for _, adminID := range cfg.UserIDs {
		if adminID == msg.UserID {
			// Never notify an admin about their own message.
			continue
		}
		// The digest is a fresh direct message, not a reply: no message
		// ID (feishu then uses the plain send-message API) and no chat
		// ID (slack/telegram/qqbot address the DM by user ID).
		adminMsg := &IncomingMessage{
			Platform:    Platform(channel.Platform),
			MessageType: MessageTypeText,
			UserID:      adminID,
			ChatType:    ChatTypeDirect,
		}
		sendCtx, cancel := context.WithTimeout(imOutboundContext(ctx), adminDigestSendTimeout)
		err := adapter.SendReply(sendCtx, adminMsg, &ReplyMessage{Content: text, IsFinal: true})
		cancel()
		if err != nil {
			logger.Warnf(ctx, "[IM] Failed to send admin digest to %s on channel %s: %v", adminID, channel.ID, err)
		}
	}
}

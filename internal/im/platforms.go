package im

import (
	"sort"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
)

// Channel modes. The first mode a platform lists is its default.
const (
	ModeWebSocket = "websocket"
	ModeWebhook   = "webhook"
	ModeLongPoll  = "longpoll"
)

// PlatformInfo describes one IM platform for the channel editor: how it is
// named, which connection modes it supports and what credentials it takes.
type PlatformInfo struct {
	ID string `json:"id"`
	// Name is the default (English) name; Names holds localized variants.
	Name  string            `json:"name"`
	Names map[string]string `json:"names,omitempty"`
	// Order sorts platforms in the UI (lower first).
	Order int `json:"order"`
	// Modes lists the supported connection modes, default first.
	Modes []string `json:"modes"`
	// ModeLabels renames a mode for this platform (DingTalk calls its
	// WebSocket connection "Stream").
	ModeLabels map[string]string `json:"mode_labels,omitempty"`
	// ModeHintKey is the frontend locale key of the hint under the mode
	// choice; empty uses the generic hint.
	ModeHintKey string `json:"mode_hint_key,omitempty"`
	// SupportsThread reports whether replies can stay in a thread, which
	// enables the per-thread session mode.
	SupportsThread bool `json:"supports_thread"`
	// Links point at the platform's console and docs.
	Links []PlatformLink `json:"links,omitempty"`
	// ConfigSchema describes IMChannel.Credentials. Fields that apply to one
	// connection mode carry x-visible-if on "$mode", read from the channel
	// rather than from the credentials.
	ConfigSchema *configschema.Schema `json:"config_schema,omitempty"`
}

// PlatformLink is a link shown above the credential form.
type PlatformLink struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// TitleKey is the frontend locale key of the title.
	TitleKey string `json:"title_key,omitempty"`
}

// LookupPlatformInfo returns the metadata of a platform. A platform without
// an entry gets its ID as name and WebSocket as its only mode, so a newly
// registered adapter still shows up.
func LookupPlatformInfo(id string) PlatformInfo {
	if build, ok := platformInfos[id]; ok {
		return build()
	}
	if info, _, ok := pluginPlatform(id); ok {
		return info
	}
	return PlatformInfo{ID: id, Name: id, Order: 1000, Modes: []string{ModeWebSocket}}
}

// KnownPlatformInfos returns the metadata of every platform this build
// describes, whether or not its adapter is registered, sorted by ID.
func KnownPlatformInfos() []PlatformInfo {
	out := make([]PlatformInfo, 0, len(platformInfos))
	for _, build := range platformInfos {
		out = append(out, build())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Platforms returns the platforms that have a registered adapter factory,
// sorted by ID.
func (s *Service) Platforms() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.adapterFactories))
	for p := range s.adapterFactories {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// PlatformInfos returns the metadata of every registered platform in display
// order, plugins' after the builtins.
func (s *Service) PlatformInfos() []PlatformInfo {
	ids := s.Platforms()
	out := make([]PlatformInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, LookupPlatformInfo(id))
	}
	out = append(out, listPluginPlatforms()...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// platformInfos builds each platform's metadata. Names follow the frontend's
// agentEditor.im.* labels; field hints reuse its locale keys.
var platformInfos = map[string]func() PlatformInfo{
	"wecom": func() PlatformInfo {
		wsEndpoint := text("WebSocket Endpoint", "wss://openws.work.weixin.qq.com", 3)
		describe(wsEndpoint, "Optional. Custom WebSocket address for private WeCom deployments.",
			"agentEditor.im.wecomWSEndpointHint")
		apiBase := text("API Base URL", "https://qyapi.weixin.qq.com", 16)
		describe(apiBase, "Optional. Custom API base URL for private WeCom deployments.",
			"agentEditor.im.wecomAPIBaseURLHint")
		agentID := &configschema.Schema{Type: configschema.TypeInteger, Title: "Corp Agent ID", Order: 15}
		return PlatformInfo{
			ID: "wecom", Name: "WeCom", Names: map[string]string{"zh-CN": "企业微信"}, Order: 10,
			Modes: []string{ModeWebSocket, ModeWebhook},
			Links: []PlatformLink{console("https://work.weixin.qq.com/", "WeCom Admin Console", "wecomConsole")},
			ConfigSchema: configschema.Object().
				Set("bot_id", onlyIn(ModeWebSocket, text("Bot ID", "Bot ID", 1)), false).
				Set("bot_secret", onlyIn(ModeWebSocket, secret("Bot Secret", "Bot Secret", 2)), false).
				Set("ws_endpoint", onlyIn(ModeWebSocket, wsEndpoint), false).
				Set("corp_id", onlyIn(ModeWebhook, text("Corp ID", "Corp ID", 11)), false).
				Set("agent_secret", onlyIn(ModeWebhook, secret("Agent Secret", "Agent Secret", 12)), false).
				Set("token", onlyIn(ModeWebhook, text("Token", "Token", 13)), false).
				Set("encoding_aes_key", onlyIn(ModeWebhook, text("EncodingAESKey", "EncodingAESKey", 14)), false).
				Set("corp_agent_id", onlyIn(ModeWebhook, agentID), false).
				Set("api_base_url", onlyIn(ModeWebhook, apiBase), false),
		}
	},
	"feishu": func() PlatformInfo {
		return feishuPlatform("feishu", "Feishu", "飞书", 20,
			console("https://open.feishu.cn/", "Feishu Open Platform", "feishuConsole"))
	},
	// Lark is Feishu's international cloud: same credentials, separate console.
	"lark": func() PlatformInfo {
		return feishuPlatform("lark", "Lark", "Lark（飞书国际版）", 30,
			console("https://open.larksuite.com/", "Lark Open Platform", "larkConsole"))
	},
	"dingtalk": func() PlatformInfo {
		card := text("Card Template ID (optional)", "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx.schema", 3)
		card.I18nKeys = map[string]string{"title": "agentEditor.im.dingtalkCardTemplateId"}
		describe(card, "Create an AI Card template to stream replies with a typewriter effect.",
			"agentEditor.im.dingtalkCardTemplateIdHint")
		return PlatformInfo{
			ID: "dingtalk", Name: "DingTalk", Names: map[string]string{"zh-CN": "钉钉"}, Order: 60,
			Modes:      []string{ModeWebSocket},
			ModeLabels: map[string]string{ModeWebSocket: "Stream"},
			Links: []PlatformLink{
				console("https://open.dingtalk.com/", "DingTalk Open Platform", "dingtalkConsole"),
			},
			ConfigSchema: configschema.Object().
				Set("client_id", text("Client ID (AppKey)", "Client ID / AppKey", 1), false).
				Set("client_secret", secret("Client Secret (AppSecret)", "Client Secret / AppSecret", 2), false).
				Set("card_template_id", card, false),
		}
	},
	// WeChat binds by scanning a QR code; the editor runs that flow itself
	// and there is nothing to type. The bound fields are declared hidden so
	// the token is still redacted and kept like any other secret.
	"wechat": func() PlatformInfo {
		hidden := func(title string, secret bool) *configschema.Schema {
			return &configschema.Schema{Type: configschema.TypeString, Title: title, Widget: "hidden", Secret: secret}
		}
		return PlatformInfo{
			ID: "wechat", Name: "WeChat", Names: map[string]string{"zh-CN": "微信"}, Order: 80,
			Modes: []string{ModeLongPoll},
			ConfigSchema: configschema.Object().
				Set("bot_token", hidden("Bot Token", true), false).
				Set("ilink_bot_id", hidden("iLink Bot ID", false), false).
				Set("ilink_user_id", hidden("iLink User ID", false), false),
		}
	},
	"qqbot": func() PlatformInfo {
		apiBase := text("API Base URL", "https://api.sgroup.qq.com", 3)
		describe(apiBase, "Optional. Leave empty to use the default production API endpoint.",
			"agentEditor.im.qqbotAPIBaseURLHint")
		gateway := text("Gateway URL", "wss://api.sgroup.qq.com/websocket/", 4)
		describe(gateway, "Optional. Leave empty to fetch the common WebSocket gateway automatically.",
			"agentEditor.im.qqbotGatewayURLHint")
		return PlatformInfo{
			ID: "qqbot", Name: "QQBot", Order: 90,
			Modes: []string{ModeWebSocket},
			Links: []PlatformLink{console("https://q.qq.com/", "QQ Open Platform", "qqbotConsole")},
			ConfigSchema: configschema.Object().
				Set("app_id", text("App ID", "QQBot App ID", 1), false).
				Set("client_secret", secret("App Secret", "QQBot App Secret", 2), false).
				Set("api_base_url", apiBase, false).
				Set("gateway_url", gateway, false),
		}
	},
	"yunzhijia": yunzhijiaPlatform,
	"slack": func() PlatformInfo {
		return PlatformInfo{
			ID: "slack", Name: "Slack", Order: 40,
			Modes:          []string{ModeWebSocket, ModeWebhook},
			SupportsThread: true,
			Links:          []PlatformLink{console("https://api.slack.com/apps", "Slack API Console", "slackConsole")},
			ConfigSchema: configschema.Object().
				Set("app_token", onlyIn(ModeWebSocket, secret("App Token", "xapp-...", 1)), false).
				Set("bot_token", secret("Bot Token", "xoxb-...", 2), false).
				Set("signing_secret", onlyIn(ModeWebhook, secret("Signing Secret", "Signing Secret", 3)), false),
		}
	},
	"telegram": func() PlatformInfo {
		return PlatformInfo{
			ID: "telegram", Name: "Telegram", Order: 50,
			Modes:          []string{ModeWebSocket, ModeWebhook},
			SupportsThread: true,
			Links:          []PlatformLink{console("https://t.me/BotFather", "Telegram BotFather", "telegramConsole")},
			ConfigSchema: configschema.Object().
				Set("bot_token", secret("Bot Token", "123456789:AABBccdd...", 1), false).
				Set("secret_token", onlyIn(ModeWebhook, secret("Secret Token", "Secret Token (optional)", 2)), false),
		}
	},
	"mattermost": func() PlatformInfo {
		postToMain := &configschema.Schema{
			Type:    configschema.TypeBoolean,
			Title:   "Post replies in channel timeline",
			Default: false,
			Order:   5,
		}
		postToMain.I18nKeys = map[string]string{"title": "agentEditor.im.mattermostPostToMain"}
		describe(postToMain, "When on, bot replies are new top-level posts instead of thread replies.",
			"agentEditor.im.mattermostPostToMainHint")
		return PlatformInfo{
			ID: "mattermost", Name: "Mattermost", Order: 70,
			Modes:          []string{ModeWebhook},
			ModeHintKey:    "agentEditor.im.mattermostModeHint",
			SupportsThread: true,
			Links: []PlatformLink{console(
				"https://developers.mattermost.com/integrate/webhooks/outgoing/",
				"Mattermost integrations", "mattermostConsole",
			)},
			ConfigSchema: configschema.Object().
				Set("site_url", text("Site URL", "https://mattermost.example.com", 1), false).
				Set("bot_token", secret("Bot Token", "Bot Token", 2), false).
				Set("outgoing_token", secret("Outgoing Webhook Token", "Token from Outgoing Webhook", 3), false).
				Set("bot_user_id", text("Bot User ID", "Optional — filter bot self-messages", 4), false).
				Set("post_to_main", postToMain, false),
		}
	},
}

func feishuPlatform(id, name, zhName string, order int, link PlatformLink) PlatformInfo {
	apiBase := text("Base URL", "https://open.feishu.cn", 3)
	describe(apiBase, "Optional. Enter a reverse proxy URL if the server reaches Feishu through one.",
		"agentEditor.im.feishuAPIBaseURLHint")
	return PlatformInfo{
		ID: id, Name: name, Names: map[string]string{"zh-CN": zhName}, Order: order,
		Modes:          []string{ModeWebSocket, ModeWebhook},
		SupportsThread: true,
		Links:          []PlatformLink{link},
		ConfigSchema: configschema.Object().
			Set("app_id", text("App ID", "App ID", 1), false).
			Set("app_secret", secret("App Secret", "App Secret", 2), false).
			Set("api_base_url", apiBase, false).
			Set("verification_token", onlyIn(ModeWebhook, text("Verification Token", "Verification Token", 4)), false).
			Set("encrypt_key", onlyIn(ModeWebhook, secret("Encrypt Key", "Encrypt Key", 5)), false),
	}
}

func yunzhijiaPlatform() PlatformInfo {
	sendURL := text("Send Message URL",
		"https://www.yunzhijia.com/gateway/robot/webhook/send?yzjtype=0&yzjtoken=...", 1)
	sendURL.I18nKeys = map[string]string{"title": "agentEditor.im.yunzhijiaSendMsgUrl"}
	describe(sendURL, "Used for robot replies.", "agentEditor.im.yunzhijiaSendMsgUrlHint")

	sign := secret("Signature Secret (optional)", "HmacSHA1 signature secret from Yunzhijia robot settings", 2)
	localize(sign, "agentEditor.im.yunzhijiaSecret", "agentEditor.im.yunzhijiaSecretPlaceholder")
	describe(sign, "If set, incoming callbacks are verified with an HmacSHA1 signature.",
		"agentEditor.im.yunzhijiaSecretHint")

	appID := text("App ID (image download)", "Yunzhijia Open Platform App ID", 3)
	localize(appID, "agentEditor.im.yunzhijiaAppId", "agentEditor.im.yunzhijiaAppIdPlaceholder")
	describe(appID, "Used to obtain appAccessToken and download images users send.",
		"agentEditor.im.yunzhijiaAppCredentialHint")

	appSecret := secret("App Secret (image download)", "Yunzhijia Open Platform App Secret", 4)
	localize(appSecret, "agentEditor.im.yunzhijiaAppSecret", "agentEditor.im.yunzhijiaAppSecretPlaceholder")

	minTimeout, maxTimeout := 1.0, 60.0
	timeout := &configschema.Schema{
		Type: configschema.TypeInteger, Title: "HTTP Timeout (seconds)", Default: 10,
		Minimum: &minTimeout, Maximum: &maxTimeout, Placeholder: "10", Order: 5,
		I18nKeys: map[string]string{"title": "agentEditor.im.yunzhijiaTimeout"},
	}
	describe(timeout, "Timeout for sending replies via Send Message URL.", "agentEditor.im.yunzhijiaTimeoutHint")

	suffix := text("Allowed Host Suffix", "yunzhijia.com", 6)
	suffix.Default = "yunzhijia.com"
	suffix.I18nKeys = map[string]string{"title": "agentEditor.im.yunzhijiaAllowedHostSuffix"}
	describe(suffix, "Restrict the Send Message URL host to this suffix.",
		"agentEditor.im.yunzhijiaAllowedHostSuffixHint")

	return PlatformInfo{
		ID: "yunzhijia", Name: "Yunzhijia", Names: map[string]string{"zh-CN": "云之家"}, Order: 100,
		Modes:          []string{ModeWebhook, ModeWebSocket},
		ModeHintKey:    "agentEditor.im.yunzhijiaModeHint",
		SupportsThread: true,
		Links: []PlatformLink{
			console("https://www.yunzhijia.com/opendocs/docs.html#/guide/im/robot",
				"Robot messaging docs", "yunzhijiaRobotDoc"),
			console("https://www.yunzhijia.com/developers/", "Developer platform (images)", "yunzhijiaImageDoc"),
		},
		ConfigSchema: configschema.Object().
			Set("send_msg_url", sendURL, true).
			Set("secret", sign, false).
			Set("app_id", appID, false).
			Set("app_secret", appSecret, false).
			Set("timeout_seconds", timeout, false).
			Set("allowed_webhook_host_suffix", suffix, true),
	}
}

func text(title, placeholder string, order int) *configschema.Schema {
	return &configschema.Schema{Type: configschema.TypeString, Title: title, Placeholder: placeholder, Order: order}
}

// secret is a password input. IM credentials are returned to admins as
// stored, so the form shows the saved value masked rather than redacted.
func secret(title, placeholder string, order int) *configschema.Schema {
	s := text(title, placeholder, order)
	s.Secret = true
	s.Widget = "password"
	return s
}

func onlyIn(mode string, s *configschema.Schema) *configschema.Schema {
	s.VisibleIf = map[string]any{"$mode": mode}
	return s
}

func describe(s *configschema.Schema, description, key string) {
	s.Description = description
	if s.I18nKeys == nil {
		s.I18nKeys = map[string]string{}
	}
	s.I18nKeys["description"] = key
}

func localize(s *configschema.Schema, titleKey, placeholderKey string) {
	s.I18nKeys = map[string]string{"title": titleKey, "placeholder": placeholderKey}
}

func console(url, title, key string) PlatformLink {
	return PlatformLink{URL: url, Title: title, TitleKey: "agentEditor.im." + key}
}

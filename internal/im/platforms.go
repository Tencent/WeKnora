package im

import "sort"

// PlatformInfo is the display metadata of one IM platform.
type PlatformInfo struct {
	ID string
	// Name is the default (English) name; Names holds localized variants.
	Name  string
	Names map[string]string
	// Order sorts platforms in the UI (lower first).
	Order int
}

// platformInfos lists every platform that has an adapter. Names follow the
// frontend's integrations.im.* labels.
var platformInfos = map[string]PlatformInfo{
	"wecom":      {ID: "wecom", Name: "WeCom", Names: map[string]string{"zh-CN": "企业微信"}, Order: 10},
	"feishu":     {ID: "feishu", Name: "Feishu", Names: map[string]string{"zh-CN": "飞书"}, Order: 20},
	"lark":       {ID: "lark", Name: "Lark", Names: map[string]string{"zh-CN": "Lark（飞书国际版）"}, Order: 30},
	"dingtalk":   {ID: "dingtalk", Name: "DingTalk", Names: map[string]string{"zh-CN": "钉钉"}, Order: 40},
	"wechat":     {ID: "wechat", Name: "WeChat", Names: map[string]string{"zh-CN": "微信"}, Order: 50},
	"qqbot":      {ID: "qqbot", Name: "QQBot", Order: 60},
	"yunzhijia":  {ID: "yunzhijia", Name: "Yunzhijia", Names: map[string]string{"zh-CN": "云之家"}, Order: 70},
	"slack":      {ID: "slack", Name: "Slack", Order: 80},
	"telegram":   {ID: "telegram", Name: "Telegram", Order: 90},
	"mattermost": {ID: "mattermost", Name: "Mattermost", Order: 100},
}

// LookupPlatformInfo returns the display metadata of a platform. A platform
// without an entry gets its ID as name, so a newly registered adapter still
// shows up.
func LookupPlatformInfo(id string) PlatformInfo {
	if info, ok := platformInfos[id]; ok {
		return info
	}
	return PlatformInfo{ID: id, Name: id, Order: 1000}
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

package im

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Files uploaded through an IM channel are tagged with a knowledge Channel.
// Lark deliberately shares Feishu's channel (types.ChannelFeishu documents
// itself as "Feishu / Lark"), so Lark uploads group with Feishu ones in the KB
// UI rather than falling through to the generic "im" channel.
func TestIMPlatformToChannel(t *testing.T) {
	cases := map[string]string{
		"feishu": types.ChannelFeishu,
		"lark":   types.ChannelFeishu,
		"Lark":   types.ChannelFeishu, // matching is case-insensitive
		"wechat": types.ChannelWechat,
		"wecom":  types.ChannelWecom,
		"wxwork": types.ChannelWecom,

		"dingtalk": types.ChannelDingtalk,
		"slack":    types.ChannelSlack,
		// Platforms without a dedicated channel fall back to the generic one.
		"telegram": types.ChannelIM,
		"":         types.ChannelIM,
	}

	for platform, want := range cases {
		if got := imPlatformToChannel(platform); got != want {
			t.Errorf("imPlatformToChannel(%q) = %q, want %q", platform, got, want)
		}
	}
}

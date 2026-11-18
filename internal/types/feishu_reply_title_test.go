package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFeishuReplyTitleLegacyAndExplicitValues(t *testing.T) {
	for _, tc := range []struct{ name, config, want string }{
		{"legacy", `{}`, DefaultFeishuReplyTitle},
		{"null", `{"feishu_reply_title":null}`, DefaultFeishuReplyTitle},
		{"custom", `{"feishu_reply_title":"  Network assistant  "}`, "Network assistant"},
		{"follow_name", `{"feishu_reply_title":""}`, "智能体名称"},
		{"whitespace", `{"feishu_reply_title":"  "}`, "智能体名称"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &CustomAgent{Name: "智能体名称"}
			require.NoError(t, json.Unmarshal([]byte(tc.config), &a.Config))
			require.Equal(t, tc.want, a.FeishuCardTitle())
			a.EnsureDefaults()
			require.NotNil(t, a.Config.FeishuReplyTitle)
			require.Equal(t, tc.want, a.FeishuCardTitle())
			value, err := a.Config.Value()
			require.NoError(t, err)
			var restored CustomAgentConfig
			require.NoError(t, restored.Scan(value))
			a.Config = restored
			require.Equal(t, tc.want, a.FeishuCardTitle())
		})
	}
}

func TestFeishuReplyTitleFollowsRenamesAndBoundsLongNames(t *testing.T) {
	empty := ""
	a := &CustomAgent{Name: "数通助手", Config: CustomAgentConfig{FeishuReplyTitle: &empty}}
	a.EnsureDefaults()
	a.Name = "光产品助手"
	require.Equal(t, a.Name, a.FeishuCardTitle())
	a.Name = strings.Repeat("界", 101)
	require.Equal(t, strings.Repeat("界", 100), a.FeishuCardTitle())
	a.Name = "  "
	require.Equal(t, DefaultFeishuReplyTitle, a.FeishuCardTitle())
	var missing *CustomAgent
	require.Equal(t, DefaultFeishuReplyTitle, missing.FeishuCardTitle())
}

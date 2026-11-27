package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPToolSessionRuleValidation(t *testing.T) {
	for _, rule := range []MCPToolSessionRule{
		{ChatType: "group", ChatID: "ops"},
		{Platform: "*", ChatType: "group", ChatID: "ops"},
		{Platform: "feishu", ChatID: "ops"},
		{Platform: "feishu", ChatType: "*", ChatID: "ops"},
		{Platform: "feishu", ChatType: "group"},
		{Platform: "feishu", ChatType: "group", ChatID: "ops*"},
		{Platform: "feishu", ChatType: "group", ChatID: "ops\n"},
	} {
		require.ErrorIs(t, rule.Validate(), ErrInvalidMCPToolSessionPolicy)
		require.False(t, rule.Matches(IMConversation{Platform: "feishu", ChatType: "group", ChatID: "ops"}))
	}
	rule := MCPToolSessionRule{Platform: "feishu", ChatType: "direct", ChatID: "*"}
	require.NoError(t, rule.Validate())
	require.True(t, rule.Matches(IMConversation{Platform: "feishu", ChatType: "direct"}))
	require.False(t, rule.Matches(IMConversation{Platform: "feishu", ChatType: "group"}))
}

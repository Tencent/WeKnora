package chatpipeline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestImagePolicyUsesStableSystemPrefixAndPreservesUserRequest(t *testing.T) {
	cm := &types.ChatManage{}
	cm.SummaryConfig.Prompt = "Custom prompt"
	cm.UserContent = "Return JSON only"
	without := prepareMessagesWithHistory(cm)
	cm.RenderedContexts = "![流程图](resource://AbCdEfGhIjKlMnOpQrStUv)"
	with := prepareMessagesWithHistory(cm)
	require.Equal(t, without[0], with[0], "retrieving images must not change the system prefix")
	require.Contains(t, with[0].Content, types.SourcedAnswerOutputPrompt)
	require.Contains(t, with[0].Content, "requested format supports images")
	require.Equal(t, cm.UserContent, with[1].Content,
		"no generated instruction may masquerade as part of the user request")
}

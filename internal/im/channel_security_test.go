package im

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDingtalkOnlySupportsStream(t *testing.T) {
	service := &Service{}
	channel := &IMChannel{Platform: "dingtalk", Mode: "webhook"}
	// No database/connection factory is installed: reject before persistence or IO.
	require.Error(t, service.CreateChannel(channel))
	require.Error(t, service.UpdateChannel(channel))
	require.Error(t, service.StartChannel(channel))
	channel.Mode = "websocket"
	require.NoError(t, validateChannelTransport(channel))
	channel.Mode = ""
	require.NoError(t, validateChannelTransport(channel))
	channel.Platform = "slack"
	channel.Mode = "webhook"
	require.NoError(t, validateChannelTransport(channel))
}

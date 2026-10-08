package im

import "fmt"

// DingTalk's HTTP signature authenticates a timestamp, not the message body.
// Stream provides the authenticated transport required for message integrity.
func validateChannelTransport(channel *IMChannel) error {
	if channel.Platform == "dingtalk" && ResolveMode(channel, "websocket") != "websocket" {
		return fmt.Errorf("DingTalk requires Stream mode; enable Stream in the DingTalk developer console")
	}
	return nil
}

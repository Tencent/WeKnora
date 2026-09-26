package host

import (
	"os"
	"strings"
)

// envNetns runs host plugins in their own network namespace (Linux), where
// the egress proxy and the Host API are the only ways out: set it to 1.
// It needs unprivileged user namespaces, which some container runtimes
// block; plugins then fail to start rather than run unconfined. On a
// standalone plugin host the Host API is on another node: the plugin
// reaches it through the egress proxy, which lets that host pass.
const envNetns = "WEKNORA_PLUGIN_NETNS"

func netnsEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(envNetns)))
	return v == "1" || v == "true" || v == "yes"
}

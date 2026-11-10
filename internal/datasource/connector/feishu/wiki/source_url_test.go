package wiki

import (
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWikiNodeURLUsesConnectorRegion(t *testing.T) {
	for _, host := range []string{"https://feishu.cn", "https://larksuite.com"} {
		ops := &wikiOps{region: core.Region{WebBaseURL: host}}
		require.Equal(t, host+"/wiki/node", ops.NodeURL(core.WikiNode{NodeToken: "node"}))
	}
}

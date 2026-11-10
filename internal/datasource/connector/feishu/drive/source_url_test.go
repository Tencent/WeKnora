package drive

import (
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDriveNodeURLPreservesProviderLink(t *testing.T) {
	for _, url := range []string{"", "https://team.feishu.cn/docx/document", "https://team.larksuite.com/file/document"} {
		require.Equal(t, url, (&driveOps{}).NodeURL(core.DriveFile{URL: url}))
	}
}

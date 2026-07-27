package notion

import (
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func resetSSRFWhitelistForTest(t *testing.T) {
	t.Helper()
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func allowLoopbackForNotionTest(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	resetSSRFWhitelistForTest(t)
}

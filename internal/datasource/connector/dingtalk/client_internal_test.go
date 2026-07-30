package dingtalk

import (
	"os"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

// resetTokenCacheForTest wipes the shared token cache so each test starts fresh.
func resetTokenCacheForTest() {
	tokenCacheMu.Lock()
	tokenCache = make(map[string]*tokenCacheEntry)
	tokenCacheMu.Unlock()
}

func asAPIError(err error, target **apiError) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*apiError); ok {
		*target = e
		return true
	}
	return false
}

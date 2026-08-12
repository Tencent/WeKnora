package opensearch

import (
	"os"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	// End-to-end repository tests intentionally use loopback httptest servers.
	secutils.SetSSRFWhitelistFromRaw("127.0.0.1,::1,localhost,opensearch.example.com")
	code := m.Run()
	secutils.SetSSRFWhitelistFromRaw("")
	os.Exit(code)
}

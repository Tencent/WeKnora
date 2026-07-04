package docparser

import (
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetMinerUSSRFWhitelistForTest(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "")
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestValidateMinerUOutboundURL_RejectsLoopback(t *testing.T) {
	resetMinerUSSRFWhitelistForTest(t)

	err := validateMinerUOutboundURL("http://127.0.0.1:8080")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSRF")
}

func TestPingMinerU_RejectsPrivateEndpoint(t *testing.T) {
	resetMinerUSSRFWhitelistForTest(t)

	ok, msg := PingMinerU("http://127.0.0.1:8080")
	assert.False(t, ok)
	assert.Contains(t, msg, "SSRF")
}

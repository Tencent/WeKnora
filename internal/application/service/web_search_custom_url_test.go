package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestWebSearchCustomURLValidation(t *testing.T) {
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("SSRF_WHITELIST", "")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, provider := range []types.WebSearchProviderType{
		types.WebSearchProviderTypeBing, types.WebSearchProviderTypeGoogle,
	} {
		params := types.WebSearchProviderParameters{APIKey: "key", EngineID: "engine"}
		if err := validateProviderParameters(provider, params); err != nil {
			t.Fatalf("%s default: %v", provider, err)
		}
		params.BaseURL = "http://127.0.0.1/private"
		if err := validateProviderParameters(provider, params); err == nil {
			t.Fatalf("%s accepted private URL", provider)
		}
	}
}

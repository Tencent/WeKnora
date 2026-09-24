package yunzhijia

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

func whitelistOnly(t *testing.T, whitelist, only string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", whitelist)
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	t.Setenv("SSRF_DNS_WHITELIST_ONLY", only)
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
}

// The webhook endpoint is tenant-supplied, so its dialer resolves whatever it
// is given; in whitelist-only mode a host outside the whitelist is refused
// before that lookup (#3378).
func TestSafeDialContextRefusesNonWhitelistedHostBeforeResolving(t *testing.T) {
	whitelistOnly(t, "yunzhijia.example", "true")

	_, err := safeDialContext(context.Background(), "tcp", "outside.invalid:443")
	if !errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("expected the whitelist refusal, got %v", err)
	}
	// An address reaches the endpoint's own public-IP check instead.
	_, err = safeDialContext(context.Background(), "tcp", "198.51.100.7:443")
	if errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("an address must be left to the address checks, got %v", err)
	}

	whitelistOnly(t, "yunzhijia.example", "false")
	_, err = safeDialContext(context.Background(), "tcp", "outside.invalid:443")
	if errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("with the mode off the host is resolved as before, got %v", err)
	}
}

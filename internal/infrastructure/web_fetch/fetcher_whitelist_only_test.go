package web_fetch

import (
	"context"
	"errors"
	"net"
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

// countingResolver stands in for DNS so a test can prove the lookup never ran.
func countingResolver(ips ...string) (func(context.Context, string) ([]net.IP, error), *int) {
	calls := 0
	return func(context.Context, string) ([]net.IP, error) {
		calls++
		parsed := make([]net.IP, 0, len(ips))
		for _, ip := range ips {
			parsed = append(parsed, net.ParseIP(ip))
		}
		return parsed, nil
	}, &calls
}

func TestResolvePinnedTargetRefusesNonWhitelistedHostWithoutDNS(t *testing.T) {
	whitelistOnly(t, "docs.internal", "true")
	resolve, calls := countingResolver("93.184.216.34")
	f := &Fetcher{resolveIPs: resolve}

	_, err := f.resolvePinnedTarget(context.Background(), "https://outside.invalid/page")

	if !errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("expected the whitelist refusal, got %v", err)
	}
	var fetchErr *FetchError
	if !errors.As(err, &fetchErr) || fetchErr.Code != ErrorSSRFRejected {
		t.Fatalf("the refusal must be classified as an SSRF rejection, got %v", err)
	}
	if fetchErr.Retryable {
		t.Fatal("a whitelist refusal is permanent, not retryable")
	}
	if *calls != 0 {
		t.Fatalf("the host must not be resolved at all, got %d lookups", *calls)
	}
}

// The dialer behind the fetch client is a second entry point into DNS.
func TestPinnedDialContextRefusesNonWhitelistedHostWithoutDNS(t *testing.T) {
	whitelistOnly(t, "docs.internal", "true")
	resolve, calls := countingResolver("93.184.216.34")
	f := &Fetcher{resolveIPs: resolve}

	_, err := f.pinnedDialContext()(context.Background(), "tcp", "outside.invalid:443")

	if !errors.Is(err, utils.ErrSSRFHostNotWhitelisted) {
		t.Fatalf("expected the whitelist refusal, got %v", err)
	}
	if *calls != 0 {
		t.Fatalf("the host must not be resolved at all, got %d lookups", *calls)
	}
}

func TestResolvePinnedTargetStillResolvesWhitelistedAndOrdinaryHosts(t *testing.T) {
	whitelistOnly(t, "docs.internal", "true")
	resolve, calls := countingResolver("93.184.216.34")
	f := &Fetcher{resolveIPs: resolve}

	// A whitelisted host is still resolved: the render pins one address.
	if _, err := f.resolvePinnedTarget(context.Background(), "https://docs.internal/page"); err != nil {
		t.Fatalf("a whitelisted host must be fetchable: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected one lookup for the whitelisted host, got %d", *calls)
	}

	whitelistOnly(t, "docs.internal", "false")
	if _, err := f.resolvePinnedTarget(context.Background(), "https://outside.invalid/page"); err != nil {
		t.Fatalf("with the mode off an ordinary public host must still fetch: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("expected the ordinary host to be resolved, got %d lookups", *calls)
	}
}

package utils

import (
	"net/http"
	"testing"
	"time"
)

func TestOutboundSSRFValidationCacheReusesSameOrigin(t *testing.T) {
	ResetSSRFWhitelistForTest()
	ResetSSRFOutboundValidationCacheForTest()
	SetSSRFWhitelistFromRaw("cache-test.example.com")

	if err := validateURLForSSRFForOutbound("https://cache-test.example.com/path/a"); err != nil {
		t.Fatalf("first validation failed: %v", err)
	}
	if err := validateURLForSSRFForOutbound("https://cache-test.example.com/path/b"); err != nil {
		t.Fatalf("second validation failed: %v", err)
	}
	if got := SSRFOutboundValidationMissesForTest(); got != 1 {
		t.Fatalf("expected 1 cache miss for same origin, got %d", got)
	}
}

func TestOutboundSSRFValidationCacheInvalidatesOnWhitelistChange(t *testing.T) {
	ResetSSRFWhitelistForTest()
	ResetSSRFOutboundValidationCacheForTest()
	SetSSRFWhitelistFromRaw("cache-test.example.com")

	if err := validateURLForSSRFForOutbound("https://cache-test.example.com/a"); err != nil {
		t.Fatalf("initial validation failed: %v", err)
	}
	if SSRFOutboundValidationMissesForTest() != 1 {
		t.Fatalf("expected one miss before whitelist change, got %d", SSRFOutboundValidationMissesForTest())
	}

	SetSSRFWhitelistFromRaw("cache-test.example.com,other.example.com")
	if err := validateURLForSSRFForOutbound("https://cache-test.example.com/b"); err != nil {
		t.Fatalf("validation after whitelist change failed: %v", err)
	}
	if got := SSRFOutboundValidationMissesForTest(); got != 2 {
		t.Fatalf("expected cache miss after whitelist bump, got %d misses", got)
	}
}

func TestOutboundSSRFValidationCacheExpires(t *testing.T) {
	ResetSSRFWhitelistForTest()
	ResetSSRFOutboundValidationCacheForTest()
	SetSSRFWhitelistFromRaw("cache-test.example.com")

	if err := validateURLForSSRFForOutbound("https://cache-test.example.com/a"); err != nil {
		t.Fatalf("initial validation failed: %v", err)
	}

	cacheKey, ok := outboundSSRFCacheKey("https://cache-test.example.com/a")
	if !ok {
		t.Fatal("expected cache key")
	}
	raw, ok := ssrfOutboundCache.Load(cacheKey)
	if !ok {
		t.Fatal("expected cached entry")
	}
	entry := raw.(*ssrfOutboundCacheEntry)
	entry.expiresAt = time.Now().Add(-time.Second)
	ssrfOutboundCache.Store(cacheKey, entry)

	if err := validateURLForSSRFForOutbound("https://cache-test.example.com/b"); err != nil {
		t.Fatalf("validation after expiry failed: %v", err)
	}
	if got := SSRFOutboundValidationMissesForTest(); got != 2 {
		t.Fatalf("expected revalidation after TTL expiry, got %d misses", got)
	}
}

func TestSSRFValidatingRoundTripperUsesOutboundCache(t *testing.T) {
	ResetSSRFWhitelistForTest()
	ResetSSRFOutboundValidationCacheForTest()
	SetSSRFWhitelistFromRaw("cache-test.example.com")

	base := &recordingRoundTripper{}
	rt := &SSRFValidatingRoundTripper{Base: base}

	req1, err := http.NewRequest(http.MethodGet, "https://cache-test.example.com/a", nil)
	if err != nil {
		t.Fatal(err)
	}
	req2, err := http.NewRequest(http.MethodGet, "https://cache-test.example.com/b", nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.RoundTrip(req1); err == nil {
		t.Fatal("expected base transport error, validation should pass")
	}
	if _, err := rt.RoundTrip(req2); err == nil {
		t.Fatal("expected base transport error, validation should pass")
	}
	if got := SSRFOutboundValidationMissesForTest(); got != 1 {
		t.Fatalf("round tripper expected one outbound validation miss, got %d", got)
	}
}
